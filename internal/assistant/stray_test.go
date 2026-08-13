// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package assistant

import (
	"encoding/json"
	"testing"

	"github.com/firelabsca/firebin-api/internal/ai"
)

func testDefs() []ai.ToolDef {
	return []ai.ToolDef{
		{
			Name:   "read_datasheet_page",
			Schema: json.RawMessage(`{"type":"object","properties":{"datasheet_id":{"type":"string"},"page":{"type":"integer"}},"required":["datasheet_id","page"]}`),
		},
		{
			Name:   "search_datasheet",
			Schema: json.RawMessage(`{"type":"object","properties":{"datasheet_id":{"type":"string"},"query":{"type":"string"}},"required":["datasheet_id","query"]}`),
		},
		{
			Name:   "search_parts",
			Schema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}},"required":["query"]}`),
		},
		{
			Name:   "inventory_stats",
			Schema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}
}

// The exact content that reached the screen instead of a voltage rating.
func TestStrayToolCallRecoversBareArguments(t *testing.T) {
	call, ok := strayToolCall(`{"datasheet_id":"42a5336e-feda-4450-8e87-2d0a92464d28","page":64}`, testDefs())
	if !ok {
		t.Fatal("did not recognise the call")
	}
	if call.Name != "read_datasheet_page" {
		t.Errorf("Name = %q, want read_datasheet_page", call.Name)
	}
	var args struct {
		DatasheetID string `json:"datasheet_id"`
		Page        int    `json:"page"`
	}
	if err := json.Unmarshal(call.Input, &args); err != nil {
		t.Fatalf("Input is not usable as arguments: %v", err)
	}
	if args.Page != 64 || args.DatasheetID == "" {
		t.Errorf("arguments did not survive: %+v", args)
	}
}

func TestStrayToolCallRecoversWrappedForms(t *testing.T) {
	cases := []string{
		`{"name":"search_parts","arguments":{"query":"220 ohm"}}`,
		`{"tool":"search_parts","parameters":{"query":"220 ohm"}}`,
		`{"tool_name":"search_parts","input":{"query":"220 ohm"}}`,
		// Some templates double-encode the arguments as a string.
		`{"name":"search_parts","arguments":"{\"query\":\"220 ohm\"}"}`,
		"```json\n{\"name\":\"search_parts\",\"arguments\":{\"query\":\"220 ohm\"}}\n```",
	}
	for _, in := range cases {
		call, ok := strayToolCall(in, testDefs())
		if !ok {
			t.Errorf("%s: not recognised", in)
			continue
		}
		if call.Name != "search_parts" {
			t.Errorf("%s: Name = %q", in, call.Name)
		}
	}
}

// A false positive runs a tool nobody asked for and hides a good answer, so
// anything short of certain has to be left alone.
func TestStrayToolCallLeavesAnswersAlone(t *testing.T) {
	cases := map[string]string{
		"ordinary prose":     "The maximum supply voltage is 3.6 V on page 64.",
		"prose with braces":  "Use the {reset} macro and check page 64.",
		"a JSON answer":      `{"note":"this is not any tool's arguments"}`,
		"unknown tool named": `{"name":"launch_missiles","arguments":{"target":"x"}}`,
		"empty":              "",
		"an array":           `[{"datasheet_id":"x","page":1}]`,
		// Extra keys mean it is not this tool's argument object, whatever else
		// it is.
		"superset of a tool": `{"datasheet_id":"x","page":1,"colour":"red"}`,
		// Missing a required parameter.
		"subset of a tool": `{"datasheet_id":"x"}`,
		// inventory_stats takes no parameters, so it must never be the answer to
		// "which tool do these arguments belong to".
		"no-parameter tool": `{}`,
	}
	for name, in := range cases {
		if call, ok := strayToolCall(in, testDefs()); ok {
			t.Errorf("%s: wrongly recovered %s from %q", name, call.Name, in)
		}
	}
}

// Two tools that take the same arguments make the shape ambiguous, and a guess
// would run the wrong one.
func TestStrayToolCallRefusesWhenAmbiguous(t *testing.T) {
	defs := []ai.ToolDef{
		{Name: "a", Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)},
		{Name: "b", Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)},
	}
	if call, ok := strayToolCall(`{"id":"x"}`, defs); ok {
		t.Errorf("guessed %s where two tools fit", call.Name)
	}
}

func TestIsJSONObject(t *testing.T) {
	yes := []string{`{}`, ` {"a":1} `, "{\n\"a\":1\n}"}
	no := []string{"", "hello", `[1,2]`, `{"a":1`, `"a":1}`}
	for _, s := range yes {
		if !isJSONObject(s) {
			t.Errorf("isJSONObject(%q) = false", s)
		}
	}
	for _, s := range no {
		if isJSONObject(s) {
			t.Errorf("isJSONObject(%q) = true", s)
		}
	}
}

// The name the runtime hands back is not always the one the model chose.
// gpt-oss addresses tools as "functions.read_datasheet_page" and Ollama's
// parser sometimes loses the half after the dot, so a call arrives named
// "functions?" carrying perfectly good arguments. Verbatim from a failed turn.
func TestResolveNameRepairsAMangledName(t *testing.T) {
	cases := map[string]string{
		"functions?":                    "read_datasheet_page",
		"functions.read_datasheet_page": "read_datasheet_page",
		"functions:read_datasheet_page": "read_datasheet_page",
		"namespace/read_datasheet_page": "read_datasheet_page",
		"read_datasheet_page":           "read_datasheet_page",
	}
	for given, want := range cases {
		call := ai.ToolCall{
			Name:  given,
			Input: json.RawMessage(`{"datasheet_id":"6c346460-72c1-4113-958f-203e36180538","page":12}`),
		}
		got, ok := resolveToolName(call, testDefs())
		if !ok || got != want {
			t.Errorf("%q resolved to %q (%v), want %q", given, got, ok, want)
		}
	}
}

// A name nobody can place, with arguments that fit nothing, stays unresolved so
// the model is told the truth rather than having a tool picked for it.
func TestResolveNameGivesUpWhenItCannotTell(t *testing.T) {
	for _, in := range []ai.ToolCall{
		{Name: "functions?", Input: json.RawMessage(`{"colour":"red"}`)},
		{Name: "functions?", Input: json.RawMessage(`not json`)},
		{Name: "functions?", Input: json.RawMessage(`{}`)},
	} {
		if got, ok := resolveToolName(in, testDefs()); ok {
			t.Errorf("%s resolved to %q, want no guess", in.Input, got)
		}
	}
}
