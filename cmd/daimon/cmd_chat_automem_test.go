package main

import (
	"strings"
	"testing"
)

// TestParseProposedFacts_LLMOutputShapes covers the wire-shape sloppiness
// real LLMs produce: code fences, prose before/after JSON, missing fields,
// unknown kinds, empty content. parseProposedFacts must be defensive on
// all of these because we run it on every chat turn — silent skip beats
// crash, but valid facts buried in junk MUST still be extracted.
func TestParseProposedFacts_LLMOutputShapes(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantLen  int
		wantKind string // first fact's kind, "" if wantLen == 0
	}{
		{
			name:     "bare array",
			input:    `[{"kind":"fact","content":"I live in Berlin"}]`,
			wantLen:  1,
			wantKind: "fact",
		},
		{
			name: "code-fenced JSON (Claude's default style)",
			input: "```json\n" + `[{"kind":"preference","content":"I prefer concise explanations"}]` + "\n```",
			wantLen:  1,
			wantKind: "preference",
		},
		{
			name: "prose before + after array",
			input: `Sure, here are the facts I extracted:

[{"kind":"fact","content":"I'm vegan"},{"kind":"fact","content":"I work as a designer"}]

Hope that helps!`,
			wantLen:  2,
			wantKind: "fact",
		},
		{
			name:    "empty array",
			input:   `[]`,
			wantLen: 0,
		},
		{
			name:    "malformed JSON",
			input:   `not even close to JSON`,
			wantLen: 0,
		},
		{
			name: "unknown kind coerced to observation, not dropped",
			input: `[{"kind":"random-thing","content":"I drink coffee"}]`,
			wantLen:  1,
			wantKind: "observation",
		},
		{
			name:    "empty content filtered out",
			input:   `[{"kind":"fact","content":""},{"kind":"fact","content":"  "},{"kind":"fact","content":"I'm vegan"}]`,
			wantLen: 1,
		},
		{
			name:    "well-formed JSON with no recognizable array",
			input:   `{"facts":[{"kind":"fact","content":"nope"}]}`,
			wantLen: 1, // parser picks up the inner [...] — acceptable since payload is still valid
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseProposedFacts(tc.input)
			if len(got) != tc.wantLen {
				t.Fatalf("parse(%q): got %d facts, want %d (parsed=%+v)", tc.input, len(got), tc.wantLen, got)
			}
			if tc.wantLen > 0 && tc.wantKind != "" && got[0].Kind != tc.wantKind {
				t.Errorf("first fact kind: got %q want %q", got[0].Kind, tc.wantKind)
			}
		})
	}
}

// TestParseProposedFacts_NoTrailingPunctuationInjected verifies the parser
// doesn't add or strip content beyond what the model wrote. The fact body
// is faithful to the LLM output — we don't try to "clean" it. This pins
// the contract for downstream memory.write.
func TestParseProposedFacts_FaithfulContent(t *testing.T) {
	in := `[{"kind":"fact","content":"I prefer concise technical explanations, no fluff!"}]`
	got := parseProposedFacts(in)
	if len(got) != 1 {
		t.Fatalf("want 1 fact, got %d", len(got))
	}
	want := "I prefer concise technical explanations, no fluff!"
	if got[0].Content != want {
		t.Errorf("content not preserved: got %q want %q", got[0].Content, want)
	}
}

// TestParseProposedFacts_HandlesNestedBrackets covers the case where the
// extracted JSON contains nested brackets (e.g. a fact body that contains
// "[1]" or "[note]"). LastIndex("]") matters here.
func TestParseProposedFacts_HandlesNestedBrackets(t *testing.T) {
	in := `[{"kind":"observation","content":"User cited [SPEC §11] as the source"}]`
	got := parseProposedFacts(in)
	if len(got) != 1 {
		t.Fatalf("want 1 fact, got %d (parsed=%+v)", len(got), got)
	}
	if !strings.Contains(got[0].Content, "[SPEC §11]") {
		t.Errorf("nested brackets lost: %q", got[0].Content)
	}
}
