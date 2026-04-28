package main

import "strings"

// Discord interaction component custom_id encoding.
//
// Two shapes share one "sq::"-prefixed namespace so the dispatcher in
// handlers.go can route the right way without ambiguity:
//
//   - sq::<tool_call_id>::<choice>      → button click (single-select);
//                                         the chosen value is in the id
//   - sq::<tool_call_id>::__select__    → select-menu submission
//                                         (multi-select); the chosen
//                                         values arrive in
//                                         interaction.MessageComponentData.Values
//
// Discord caps custom_id at 100 chars on the wire, so encodeCustomID
// truncates aggressively if the choice text is long. The button label
// remains the source of truth for display.

const (
	customIDSep      = "::"
	customIDPrefix   = "sq"
	selectMenuMarker = "__select__"
	customIDMaxLen   = 100
)

func encodeCustomID(toolCallID, choice string) string {
	id := customIDPrefix + customIDSep + toolCallID + customIDSep + choice
	if len(id) > customIDMaxLen {
		id = id[:customIDMaxLen]
	}
	return id
}

func decodeCustomID(s string) (toolCallID, choice string, ok bool) {
	parts := strings.SplitN(s, customIDSep, 3)
	if len(parts) != 3 || parts[0] != customIDPrefix {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// encodeSelectMenuCustomID encodes a tool-call-id for a multi-select
// dropdown. Selected values arrive in
// interaction.MessageComponentData.Values rather than the custom_id,
// so we only need the tool-call-id here.
func encodeSelectMenuCustomID(toolCallID string) string {
	return customIDPrefix + customIDSep + toolCallID + customIDSep + selectMenuMarker
}

// decodeSelectMenuCustomID is the inverse — returns the tool-call-id
// and ok=true iff the id is in select-menu shape. Disjoint from button
// custom_ids by construction (the marker isn't a valid choice string).
func decodeSelectMenuCustomID(s string) (toolCallID string, ok bool) {
	tc, marker, decoded := decodeCustomID(s)
	if !decoded || marker != selectMenuMarker {
		return "", false
	}
	return tc, true
}
