package main

import (
	"encoding/json"
	"strings"

	"github.com/bwmarrin/discordgo"
	gatewaysdk "github.com/mlund01/squadron-gateway-sdk"
)

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

// notifyEmoji picks a leading glyph for the notification by event.
func notifyEmoji(event string) string {
	switch event {
	case "mission_completed":
		return "✅"
	case "mission_failed":
		return "❌"
	case "mission_stopped":
		return "⏹️"
	default:
		return "🔔"
	}
}

// buildNotificationBody renders a one-way mission-lifecycle notification. For
// completed missions it appends a compact, truncated rendering of the outputs.
func buildNotificationBody(rec gatewaysdk.NotificationRecord) string {
	var b strings.Builder
	b.WriteString(notifyEmoji(rec.Event))
	b.WriteString(" **")
	if rec.Title != "" {
		b.WriteString(rec.Title)
	} else {
		b.WriteString(rec.Event)
	}
	b.WriteString("**")
	if rec.Message != "" {
		b.WriteString("\n")
		b.WriteString(rec.Message)
	}
	if rec.Error != "" {
		b.WriteString("\n```\n")
		b.WriteString(truncate(rec.Error, 1500))
		b.WriteString("\n```")
	}
	if rec.OutputsJSON != "" {
		b.WriteString("\n**Outputs**\n```json\n")
		b.WriteString(truncate(prettyJSON(rec.OutputsJSON), 1500))
		b.WriteString("\n```")
	}
	return b.String()
}

// prettyJSON re-indents a JSON string for display; returns the input
// unchanged when it isn't valid JSON.
func prettyJSON(s string) string {
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return s
	}
	return string(out)
}

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

// formatResolvedResponse expands a multi-select JSON array into a
// comma list. Falls back to the raw response on parse failure so the
// audit trail keeps whatever squadron stored.
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

// buildComponents picks the picker shape:
//   - no choices → no components (free-text)
//   - multi_select → a single StringSelect dropdown
//   - else → action rows of buttons
func buildComponents(rec gatewaysdk.HumanInputRecord) []discordgo.MessageComponent {
	if len(rec.Choices) == 0 {
		return nil
	}
	if rec.MultiSelect {
		return buildSelectMenuComponents(rec)
	}
	return buildButtonComponents(rec)
}

// Discord component limits.
const (
	maxButtonsPerRow     = 5
	maxRows              = 5
	maxButtons           = maxButtonsPerRow * maxRows
	maxButtonLabelBytes  = 80
	maxSelectOptionBytes = 100
	maxSelectMenuOptions = 25
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
		// Value=label so handlers.go can echo the choice back without an
		// option-id lookup table.
		label := truncate(choice, maxSelectOptionBytes)
		options = append(options, discordgo.SelectMenuOption{Label: label, Value: label})
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

// displayResponder substitutes a generic label when no responder id
// was recorded — happens when commander resolves with auth disabled.
func displayResponder(s string) string {
	if s == "" {
		return "another operator"
	}
	return s
}

// truncate caps a string at `max` BYTES (not runes), reserving space
// for the 3-byte UTF-8 ellipsis. Discord enforces component label
// limits on bytes-on-the-wire.
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
