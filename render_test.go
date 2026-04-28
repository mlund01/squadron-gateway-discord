package main

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	gatewaysdk "github.com/mlund01/squadron-gateway-sdk"
)

func TestDisplayResponder(t *testing.T) {
	if got := displayResponder(""); got != "another operator" {
		t.Errorf("empty responder: got %q, want %q", got, "another operator")
	}
	if got := displayResponder("discord:alice"); got != "discord:alice" {
		t.Errorf("non-empty responder: got %q, want input back", got)
	}
}

// TestTruncate pins the byte-budget contract: the result must always
// fit within `max` bytes. Discord's component label limits are
// measured in bytes-on-the-wire, so a naive `s[:max-1] + "…"` would
// overshoot by 2 (the ellipsis is 3 bytes in UTF-8).
func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "shorter than max returns input verbatim", in: "short", max: 10, want: "short"},
		{name: "exact-fit returns input verbatim", in: "exactly10!", max: 10, want: "exactly10!"},
		{name: "longer than max gets ellipsis with byte budget reserved", in: "exactly11!!", max: 10, want: "exactly…"},
		{name: "truncated to fit including the ellipsis bytes", in: "long string here", max: 5, want: "lo…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("truncate(%q, %d): got %q, want %q", tc.in, tc.max, got, tc.want)
			}
			if len(got) > tc.max {
				t.Errorf("truncate(%q, %d) returned %d bytes; must be ≤ %d",
					tc.in, tc.max, len(got), tc.max)
			}
		})
	}
}

func TestFormatResolvedResponseExpandsMultiSelectJSON(t *testing.T) {
	cases := []struct {
		name string
		rec  gatewaysdk.HumanInputRecord
		want string
	}{
		{
			name: "single-select returns response verbatim",
			rec:  gatewaysdk.HumanInputRecord{Response: "Option A"},
			want: "Option A",
		},
		{
			name: "multi-select expands JSON array to comma list",
			rec:  gatewaysdk.HumanInputRecord{MultiSelect: true, Response: `["A","C"]`},
			want: "A, C",
		},
		{
			name: "multi-select with malformed JSON falls back to raw",
			rec:  gatewaysdk.HumanInputRecord{MultiSelect: true, Response: "not json"},
			want: "not json",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatResolvedResponse(tc.rec); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildMessageBody(t *testing.T) {
	cases := []struct {
		name        string
		rec         gatewaysdk.HumanInputRecord
		wantContain []string
		wantOmit    []string
	}{
		{
			name: "short summary becomes the bold title; question follows",
			rec: gatewaysdk.HumanInputRecord{
				ShortSummary: "Vibe?",
				Question:     "Pick a vibe.",
			},
			wantContain: []string{"**Vibe?**", "Pick a vibe."},
		},
		{
			name: "multi-select adds the dropdown hint",
			rec: gatewaysdk.HumanInputRecord{
				Question:    "Pick any.",
				Choices:     []string{"A", "B"},
				MultiSelect: true,
			},
			wantContain: []string{"Pick one or more from the dropdown"},
		},
		{
			name: "single-select does NOT add the dropdown hint",
			rec: gatewaysdk.HumanInputRecord{
				Question: "Pick one.",
				Choices:  []string{"A", "B"},
			},
			wantOmit: []string{"dropdown"},
		},
		{
			name: "additional context renders under a Context header",
			rec: gatewaysdk.HumanInputRecord{
				Question:          "Q",
				AdditionalContext: "background _markdown_",
			},
			wantContain: []string{"_Context_", "background _markdown_"},
		},
		{
			name: "mission and task render as a code-fenced trail",
			rec: gatewaysdk.HumanInputRecord{
				Question:    "Q",
				MissionName: "trip_plan",
				TaskName:    "interview",
			},
			wantContain: []string{"`trip_plan › interview`"},
		},
		{
			name: "mission alone (no task) renders without separator",
			rec: gatewaysdk.HumanInputRecord{
				Question:    "Q",
				MissionName: "trip_plan",
			},
			wantContain: []string{"`trip_plan`"},
			wantOmit:    []string{"›"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := buildMessageBody(tc.rec)
			for _, sub := range tc.wantContain {
				if !strings.Contains(body, sub) {
					t.Errorf("body missing %q\nfull body:\n%s", sub, body)
				}
			}
			for _, sub := range tc.wantOmit {
				if strings.Contains(body, sub) {
					t.Errorf("body should NOT contain %q\nfull body:\n%s", sub, body)
				}
			}
		})
	}
}

func TestBuildResolvedBodyStrikesThroughOriginal(t *testing.T) {
	body := buildResolvedBody(gatewaysdk.HumanInputRecord{
		ShortSummary:    "Vibe?",
		Question:        "Pick a vibe.",
		Response:        "Relaxed",
		ResponderUserID: "discord:alice",
	})
	for _, want := range []string{"~~", "Vibe?", "✅", "Relaxed", "discord:alice"} {
		if !strings.Contains(body, want) {
			t.Errorf("resolved body missing %q\nfull body:\n%s", want, body)
		}
	}
}

func TestBuildResolvedBodyExpandsMultiSelect(t *testing.T) {
	body := buildResolvedBody(gatewaysdk.HumanInputRecord{
		ShortSummary: "Avoid?",
		Response:     `["crowds","long drives"]`,
		MultiSelect:  true,
	})
	if !strings.Contains(body, "crowds, long drives") {
		t.Errorf("multi-select resolved body should expand JSON to comma list, got:\n%s", body)
	}
	if strings.Contains(body, "[") {
		t.Errorf("resolved body should NOT show raw JSON brackets, got:\n%s", body)
	}
}

func TestBuildComponentsReturnsNilForFreeText(t *testing.T) {
	if rows := buildComponents(gatewaysdk.HumanInputRecord{ToolCallID: "tc"}); rows != nil {
		t.Errorf("free-text question (no choices) must produce no components, got %d", len(rows))
	}
}

func TestBuildComponentsSingleSelectUsesButtons(t *testing.T) {
	rows := buildComponents(gatewaysdk.HumanInputRecord{
		ToolCallID: "tc-1",
		Choices:    []string{"A", "B", "C"},
	})
	if len(rows) != 1 {
		t.Fatalf("expected 1 action row for 3 buttons, got %d", len(rows))
	}
	row := rows[0].(discordgo.ActionsRow)
	if len(row.Components) != 3 {
		t.Fatalf("expected 3 buttons, got %d", len(row.Components))
	}
	for i, comp := range row.Components {
		if _, ok := comp.(discordgo.Button); !ok {
			t.Errorf("component %d is %T, want Button", i, comp)
		}
	}
}

func TestBuildComponentsMultiSelectUsesSelectMenu(t *testing.T) {
	rows := buildComponents(gatewaysdk.HumanInputRecord{
		ToolCallID:  "tc-1",
		Choices:     []string{"A", "B", "C"},
		MultiSelect: true,
	})
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 row for a select-menu, got %d", len(rows))
	}
	row := rows[0].(discordgo.ActionsRow)
	if len(row.Components) != 1 {
		t.Fatalf("expected 1 select-menu in the row, got %d components", len(row.Components))
	}
	menu, ok := row.Components[0].(discordgo.SelectMenu)
	if !ok {
		t.Fatalf("expected SelectMenu, got %T", row.Components[0])
	}
	if menu.MenuType != discordgo.StringSelectMenu {
		t.Errorf("expected StringSelectMenu, got %v", menu.MenuType)
	}
	if menu.MaxValues != 3 {
		t.Errorf("MaxValues should match the number of options (3), got %d", menu.MaxValues)
	}
	if menu.MinValues == nil || *menu.MinValues != 1 {
		t.Errorf("MinValues should be 1 to require at least one selection, got %v", menu.MinValues)
	}
	if len(menu.Options) != 3 {
		t.Errorf("expected 3 options, got %d", len(menu.Options))
	}
	if _, ok := decodeSelectMenuCustomID(menu.CustomID); !ok {
		t.Errorf("select-menu custom_id %q must round-trip through decodeSelectMenuCustomID", menu.CustomID)
	}
}

func TestBuildButtonComponentsCapsAt25Buttons(t *testing.T) {
	choices := make([]string, 30)
	for i := range choices {
		choices[i] = "C" + string(rune('A'+i%26))
	}
	rows := buildComponents(gatewaysdk.HumanInputRecord{
		ToolCallID: "tc-1",
		Choices:    choices,
	})
	totalButtons := 0
	for _, row := range rows {
		ar, ok := row.(discordgo.ActionsRow)
		if !ok {
			t.Fatalf("expected ActionsRow, got %T", row)
		}
		if len(ar.Components) > maxButtonsPerRow {
			t.Errorf("row has %d buttons; Discord allows max %d per row", len(ar.Components), maxButtonsPerRow)
		}
		totalButtons += len(ar.Components)
	}
	if totalButtons != maxButtons {
		t.Errorf("expected exactly %d buttons (Discord cap), got %d", maxButtons, totalButtons)
	}
}

func TestBuildSelectMenuTruncatesLongChoices(t *testing.T) {
	long := strings.Repeat("a", 200)
	rows := buildComponents(gatewaysdk.HumanInputRecord{
		ToolCallID:  "tc",
		Choices:     []string{long},
		MultiSelect: true,
	})
	menu := rows[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
	if len(menu.Options[0].Label) > maxSelectOptionBytes {
		t.Errorf("select-menu option label %d bytes; Discord caps at %d",
			len(menu.Options[0].Label), maxSelectOptionBytes)
	}
}
