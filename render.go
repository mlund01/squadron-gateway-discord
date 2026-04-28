package main

import (
	"encoding/json"
	"strings"

	"github.com/bwmarrin/discordgo"
	gatewaysdk "github.com/mlund01/squadron-gateway-sdk"
)

// Pure rendering of a HumanInputRecord into the Discord wire shape:
// the message body (markdown text) and the components (buttons or
// select-menu). All functions here are side-effect-free — they take a
// record and return strings or component slices, so they're trivial
// to unit-test in isolation.

// buildMessageBody is the markdown text for a freshly-posted question.
// Leads with the bold short_summary (or falls back to the question
// when no summary), inlines the question and any additional_context,
// and ends with a code-fenced "mission › task" trail so operators
// can locate where the question came from.
func buildMessageBody(rec gatewaysdk.HumanInputRecord) string {
	var b strings.Builder
	if rec.ShortSummary != "" {
		b.WriteString("**")
		b.WriteString(rec.ShortSummary)
		b.WriteString("**\n")
	}
	b.WriteString(rec.Question)
	if rec.MultiSelect && len(rec.Choices) > 0 {
		b.WriteString("\n_Pick one or more from the dropdown below._")
	}
	if rec.AdditionalContext != "" {
		b.WriteString("\n\n_Context_\n")
		b.WriteString(rec.AdditionalContext)
	}
	if rec.MissionName != "" {
		b.WriteString("\n\n`")
		b.WriteString(rec.MissionName)
		if rec.TaskName != "" {
			b.WriteString(" › ")
			b.WriteString(rec.TaskName)
		}
		b.WriteString("`")
	}
	return b.String()
}

// buildResolvedBody is the markdown text the original message gets
// edited to once the request is resolved: the question struck-through
// and a ✅ + answer line.
func buildResolvedBody(rec gatewaysdk.HumanInputRecord) string {
	var b strings.Builder
	if rec.ShortSummary != "" {
		b.WriteString("~~**")
		b.WriteString(rec.ShortSummary)
		b.WriteString("**~~\n")
	} else {
		b.WriteString("~~")
		b.WriteString(truncate(rec.Question, 200))
		b.WriteString("~~\n")
	}
	b.WriteString("✅ ")
	b.WriteString(formatResolvedResponse(rec))
	if rec.ResponderUserID != "" {
		b.WriteString(" — _")
		b.WriteString(displayResponder(rec.ResponderUserID))
		b.WriteString("_")
	}
	return b.String()
}

// formatResolvedResponse renders the answer for the resolved-body
// line. For multi-select the on-the-wire response is a JSON array
// string (`["A","C"]`) which we expand to a friendly comma list so
// the channel doesn't show raw JSON. Falls back to the literal
// response on parse failure — the audit trail keeps whatever squadron
// stored.
func formatResolvedResponse(rec gatewaysdk.HumanInputRecord) string {
	if !rec.MultiSelect {
		return rec.Response
	}
	var picks []string
	if err := json.Unmarshal([]byte(rec.Response), &picks); err != nil || len(picks) == 0 {
		return rec.Response
	}
	return strings.Join(picks, ", ")
}

// buildComponents returns the action rows for a question.
//
//   - Single-select: a row of choice buttons (Discord caps 5 per row,
//     5 rows, 25 total).
//   - Multi-select: one StringSelect dropdown with MinValues=1,
//     MaxValues=len(choices). Discord submits the interaction once
//     the user closes the dropdown after picking.
//   - No choices (free-text only): no components.
func buildComponents(rec gatewaysdk.HumanInputRecord) []discordgo.MessageComponent {
	if len(rec.Choices) == 0 {
		return nil
	}
	if rec.MultiSelect {
		return buildSelectMenuComponents(rec)
	}
	return buildButtonComponents(rec)
}

// Discord component limits, named for readability.
const (
	maxButtonsPerRow      = 5
	maxRows               = 5
	maxButtons            = maxButtonsPerRow * maxRows
	maxButtonLabelBytes   = 80  // Discord's per-button label cap
	maxSelectOptionBytes  = 100 // Discord's per-option label cap
	maxSelectMenuOptions  = 25
)

func buildButtonComponents(rec gatewaysdk.HumanInputRecord) []discordgo.MessageComponent {
	rows := []discordgo.MessageComponent{}
	row := []discordgo.MessageComponent{}
	for i, choice := range rec.Choices {
		if i >= maxButtons {
			break
		}
		row = append(row, discordgo.Button{
			Label:    truncate(choice, maxButtonLabelBytes),
			Style:    discordgo.SecondaryButton,
			CustomID: encodeCustomID(rec.ToolCallID, choice),
		})
		if len(row) == maxButtonsPerRow {
			rows = append(rows, discordgo.ActionsRow{Components: row})
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, discordgo.ActionsRow{Components: row})
	}
	return rows
}

func buildSelectMenuComponents(rec gatewaysdk.HumanInputRecord) []discordgo.MessageComponent {
	options := make([]discordgo.SelectMenuOption, 0, len(rec.Choices))
	for i, choice := range rec.Choices {
		if i >= maxSelectMenuOptions {
			break
		}
		options = append(options, discordgo.SelectMenuOption{
			Label: truncate(choice, maxSelectOptionBytes),
			// Value is what Discord sends back in data.Values when the
			// user picks this option. We use the choice text itself so
			// the gateway can echo it without an option-id lookup table.
			Value: truncate(choice, maxSelectOptionBytes),
		})
	}
	maxVals := len(options)
	if maxVals > maxSelectMenuOptions {
		maxVals = maxSelectMenuOptions
	}
	minVals := 1
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				MenuType:    discordgo.StringSelectMenu,
				CustomID:    encodeSelectMenuCustomID(rec.ToolCallID),
				Placeholder: "Pick one or more…",
				MinValues:   &minVals,
				MaxValues:   maxVals,
				Options:     options,
			},
		}},
	}
}

// displayResponder renders a responder id for human display. Empty
// responders happen when commander resolves with auth disabled (local
// dev) or any future surface that doesn't track identity — in that
// case we say "another operator" so the audit line still reads.
func displayResponder(s string) string {
	if s == "" {
		return "another operator"
	}
	return s
}

// truncate caps a string at `max` BYTES, replacing the tail with "…"
// when truncation is needed. Discord rejects component labels that
// exceed declared limits measured in bytes-on-the-wire, so a naive
// `s[:max-1] + "…"` is wrong: "…" is 3 bytes in UTF-8 and would
// overshoot by 2.
func truncate(s string, max int) string {
	const ellipsis = "…"
	if len(s) <= max {
		return s
	}
	if max <= len(ellipsis) {
		return s[:max]
	}
	return s[:max-len(ellipsis)] + ellipsis
}
