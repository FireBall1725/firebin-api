// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package assistant

import (
	"encoding/json"
	"strings"

	"github.com/firelabsca/firebin-api/internal/ai"
)

// A tool call the model wrote into its answer instead of calling the tool.
//
// This is a real failure mode of local runtimes, not a hypothetical. gpt-oss on
// Ollama does it several times an hour: the round comes back with no tool_calls,
// no reasoning, and a content of
//
//	{"datasheet_id":"42a5336e-…","page":64}
//
// which is the arguments to read_datasheet_page with the call around them lost
// somewhere in the chat template. The loop saw no tool calls, took the content
// for the answer, and showed the user a JSON object where the voltage rating
// should have been.
//
// The model did the hard part. It picked the tool and it picked the arguments;
// only the encoding failed. So this reads the call back out rather than failing
// the turn, which turns a dead question into a slightly slower one.

// strayToolCall recognises a tool call written as prose and returns it.
//
// Deliberately strict. A false positive runs a tool the user did not ask for
// and hides an answer that was fine, which is worse than the bug being fixed,
// so anything ambiguous returns false and the text is treated as an answer.
func strayToolCall(text string, defs []ai.ToolDef) (ai.ToolCall, bool) {
	body := unfence(strings.TrimSpace(text))
	if !isJSONObject(body) {
		return ai.ToolCall{}, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &obj); err != nil || len(obj) == 0 {
		return ai.ToolCall{}, false
	}

	// The wrapped form: the model named the tool but wrote the whole call as
	// content. Every key it might have used for the two halves, because the
	// wording varies by runtime and by template.
	if name, args, ok := wrappedCall(obj); ok {
		for _, d := range defs {
			if d.Name == name {
				return ai.ToolCall{ID: "recovered", Name: name, Input: args}, true
			}
		}
		// It named something that is not a tool here. Not recoverable, and not an
		// answer either; the caller reports it.
		return ai.ToolCall{}, false
	}

	// The bare form: arguments with no call around them. The only thing left to
	// identify the tool with is the shape of the arguments, so match on that and
	// insist the answer is unique.
	var match *ai.ToolDef
	for i := range defs {
		if !argsFit(obj, defs[i].Schema) {
			continue
		}
		if match != nil {
			return ai.ToolCall{}, false // two tools take these arguments; do not guess
		}
		match = &defs[i]
	}
	if match == nil {
		return ai.ToolCall{}, false
	}
	return ai.ToolCall{ID: "recovered", Name: match.Name, Input: json.RawMessage(body)}, true
}

// wrappedCall pulls a name and arguments out of {"name":…,"arguments":{…}} and
// the several spellings of it that different templates emit.
func wrappedCall(obj map[string]json.RawMessage) (string, json.RawMessage, bool) {
	var name string
	for _, k := range []string{"name", "tool", "tool_name", "function"} {
		if raw, ok := obj[k]; ok {
			if json.Unmarshal(raw, &name) == nil && name != "" {
				break
			}
		}
	}
	if name == "" {
		return "", nil, false
	}
	for _, k := range []string{"arguments", "parameters", "input", "args"} {
		if raw, ok := obj[k]; ok {
			// Some templates double-encode the arguments as a JSON string.
			var s string
			if json.Unmarshal(raw, &s) == nil && isJSONObject(strings.TrimSpace(s)) {
				return name, json.RawMessage(s), true
			}
			if isJSONObject(strings.TrimSpace(string(raw))) {
				return name, raw, true
			}
		}
	}
	// A name with no arguments is still a call, for a tool that takes none.
	return name, json.RawMessage(`{}`), true
}

// argsFit reports whether an object could be the arguments to a tool: every key
// it carries is one the tool declares, and every parameter the tool requires is
// present. Both halves matter. Without the first, a two-key object matches a
// tool that takes twenty; without the second, it matches every tool whose
// parameters happen to be optional.
func argsFit(obj map[string]json.RawMessage, schema json.RawMessage) bool {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(schema, &s); err != nil || len(s.Properties) == 0 {
		return false
	}
	// A tool with no required parameters is matched by too much to be safe: an
	// object of optional keys would fit several of them, and the uniqueness check
	// cannot tell which was meant.
	if len(s.Required) == 0 {
		return false
	}
	for k := range obj {
		if _, ok := s.Properties[k]; !ok {
			return false
		}
	}
	for _, k := range s.Required {
		if _, ok := obj[k]; !ok {
			return false
		}
	}
	return true
}

// isJSONObject is a shape test, not a parse: it decides whether text that came
// back as an answer is worth trying to read as a call at all.
func isJSONObject(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")
}

// unfence strips a ```json … ``` wrapper, which a model reaching for "show some
// JSON" often adds to a call it meant to make.
func unfence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		// Drop the language tag on the fence line, but only if that is all it is.
		if head := strings.TrimSpace(s[:i]); head == "" || !strings.ContainsAny(head, "{ \t") {
			s = s[i+1:]
		}
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}

// resolveName works out which tool a call meant when the name it arrived with
// is not one of ours.
//
// Two repairs, in order of confidence. A namespaced name keeps its last
// segment, so "functions.read_datasheet_page" resolves by stripping a prefix
// the model was never supposed to send. Failing that, the arguments identify
// the tool: they are the model's own and they survive the mangling intact,
// because whatever loses the name is parsing the JSON body separately.
//
// The same uniqueness rule as strayToolCall applies, and for the same reason.
// Running the wrong tool because two of them take similar arguments would turn
// a legible failure into a wrong answer.
func resolveToolName(call ai.ToolCall, defs []ai.ToolDef) (string, bool) {
	for _, d := range defs {
		if d.Name == call.Name {
			return d.Name, true
		}
	}

	if i := strings.LastIndexAny(call.Name, ".:/"); i >= 0 && i+1 < len(call.Name) {
		tail := call.Name[i+1:]
		for _, d := range defs {
			if d.Name == tail {
				return d.Name, true
			}
		}
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(call.Input, &obj); err != nil || len(obj) == 0 {
		return "", false
	}
	match := ""
	for _, d := range defs {
		if !argsFit(obj, d.Schema) {
			continue
		}
		if match != "" {
			return "", false
		}
		match = d.Name
	}
	return match, match != ""
}
