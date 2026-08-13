// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package ai

import (
	"fmt"
	"strings"
)

// ErrToolCallUnparsed marks a round the runtime threw out because the model
// wrote a tool call it could not parse.
//
// Ollama parses tool calls out of the model's raw output using the model's own
// chat template, and gpt-oss gets it wrong often enough to matter. What comes
// back is a 200 with an error frame:
//
//	error parsing tool call: raw='{"datasheet_id":"42a2…","page":...}',
//	err=invalid character '.' looking for beginning of value
//
// Those are the model's arguments with an ellipsis left in them, which nothing
// downstream can repair. But it is a sampling accident, not a property of the
// question: asked again, the same model usually writes the call correctly. So
// this is worth another attempt, and it is the one provider error that is,
// which is why it has a sentinel of its own rather than a general retry policy.
var ErrToolCallUnparsed = fmt.Errorf("the runtime could not parse the model's tool call")

// classifyProviderError wraps a provider's error message with a sentinel when
// it is one the caller can do something about.
//
// Matching on the runtime's wording is unpleasant and it is the only handle
// there is: Ollama returns this as a plain string with no code, no type, and
// the same 200 as a good response. The phrase is specific enough that no other
// provider's error will collide with it, and a miss costs the retry, not
// correctness.
func classifyProviderError(msg string) error {
	if strings.Contains(msg, "error parsing tool call") {
		return fmt.Errorf("%s: %w", msg, ErrToolCallUnparsed)
	}
	return fmt.Errorf("%s", msg)
}
