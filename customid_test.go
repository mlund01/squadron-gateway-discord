package main

import "testing"

func TestEncodeDecodeCustomID(t *testing.T) {
	cases := []struct {
		toolCallID, choice string
	}{
		{"tc-1", "yes"},
		{"tc-with-dashes", "Option A"},
		{"abc123", "long choice with spaces and 123"},
	}
	for _, c := range cases {
		id := encodeCustomID(c.toolCallID, c.choice)
		gotTC, gotChoice, ok := decodeCustomID(id)
		if !ok {
			t.Fatalf("decode failed for %q (encoded as %q)", c.toolCallID, id)
		}
		if gotTC != c.toolCallID || gotChoice != c.choice {
			t.Errorf("round-trip mismatch: encoded(%q,%q) → %q → (%q,%q)",
				c.toolCallID, c.choice, id, gotTC, gotChoice)
		}
	}
}

func TestDecodeCustomIDRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"not-our-prefix",
		"sq::onlytwoparts",
		"sq:::missing-tool-call-id",
	}
	for _, in := range cases {
		if _, _, ok := decodeCustomID(in); ok {
			t.Errorf("decodeCustomID(%q) returned ok=true; expected false", in)
		}
	}
}

// TestSelectMenuCustomIDRoundTrip pins the namespace separation
// between buttons and select-menus: a button's id must NOT
// accidentally decode as a select-menu id, otherwise the dispatcher
// in handlers.go would misclassify clicks as menu submissions.
func TestSelectMenuCustomIDRoundTrip(t *testing.T) {
	id := encodeSelectMenuCustomID("tc-abc")
	tc, ok := decodeSelectMenuCustomID(id)
	if !ok || tc != "tc-abc" {
		t.Errorf("round-trip failed: encoded %q → decoded (%q, %v)", id, tc, ok)
	}
	buttonID := encodeCustomID("tc-abc", "Option A")
	if _, ok := decodeSelectMenuCustomID(buttonID); ok {
		t.Errorf("button id %q decoded as select-menu id; the two namespaces must not collide", buttonID)
	}
}
