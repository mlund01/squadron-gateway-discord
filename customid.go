package main

import "strings"

// Custom_id encoding for component interactions. Two shapes share one
// "sq::"-prefixed namespace:
//
//   sq::<tool_call_id>::<choice>     button click — choice is the value
//   sq::<tool_call_id>::__select__   select-menu  — values arrive in
//                                                  data.Values instead
//
// Discord caps custom_id at 100 bytes on the wire.

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

func encodeSelectMenuCustomID(toolCallID string) string {
	return customIDPrefix + customIDSep + toolCallID + customIDSep + selectMenuMarker
}

func decodeSelectMenuCustomID(s string) (toolCallID string, ok bool) {
	tc, marker, decoded := decodeCustomID(s)
	if !decoded || marker != selectMenuMarker {
		return "", false
	}
	return tc, true
}
