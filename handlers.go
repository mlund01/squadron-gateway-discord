package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/bwmarrin/discordgo"
)

// Inbound: Discord events the bot reacts to. Two surfaces:
//
//   - Component interactions (button clicks + select-menu submissions)
//   - Plain message replies (free-text answers)
//
// Both end at api.ResolveHumanInput; the squadron-side state machine
// is the single source of truth.

// onInteraction handles button clicks (single-select) and select-menu
// submissions (multi-select). The custom_id picks which:
//
//   - sq::<tool_call_id>::__select__ → multi-select; values come from
//     data.Values, JSON-encoded into the response
//   - sq::<tool_call_id>::<choice>   → single-select button click
//
// Anything else is ignored — Discord may deliver interactions for
// components we didn't render.
func (g *discordGateway) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	data := i.MessageComponentData()

	toolCallID, response, ok := decodeInteractionResponse(data.CustomID, data.Values)
	if !ok {
		return
	}

	res, err := g.api.ResolveHumanInput(context.Background(), toolCallID, response, discordResponderID(i))
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("Could not submit: %v", err),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Always silent-ack except when the row is genuinely gone. The
	// edited message is the operator's confirmation; an extra ephemeral
	// is noise. AlreadyResolved (race / duplicate / commander beat us)
	// also goes silent — the message edit will follow shortly.
	if res.NotFound {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "That request is no longer in squadron's store — the row may have been trimmed.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})
}

// decodeInteractionResponse maps a component interaction's custom_id
// + values into the (toolCallID, response) pair we send to squadron.
// Returns ok=false for any custom_id that didn't originate from this
// gateway. JSON-encodes the multi-select values so the agent gets a
// canonical array string instead of a Go-printed slice.
func decodeInteractionResponse(customID string, values []string) (toolCallID, response string, ok bool) {
	if tc, ok := decodeSelectMenuCustomID(customID); ok {
		encoded, err := json.Marshal(values)
		if err != nil {
			log.Printf("encode select-menu values: %v", err)
			return "", "", false
		}
		return tc, string(encoded), true
	}
	if tc, choice, ok := decodeCustomID(customID); ok {
		return tc, choice, true
	}
	return "", "", false
}

// onMessage handles free-text replies. A user replying to a question
// message in the channel sends the message body as the response —
// useful when there are no choices, or the user wants to override the
// quick-reply options.
func (g *discordGateway) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}
	if m.ChannelID != g.channelID {
		return
	}
	if m.MessageReference == nil || m.MessageReference.MessageID == "" {
		return
	}

	g.mu.Lock()
	matched := lookupToolCallByMessageID(g.messages, m.MessageReference.MessageID)
	g.mu.Unlock()
	if matched == "" {
		return
	}

	_, err := g.api.ResolveHumanInput(context.Background(), matched, m.Content, "discord:"+m.Author.Username)
	if err != nil {
		_, _ = s.ChannelMessageSendReply(m.ChannelID, fmt.Sprintf("Could not submit: %v", err), m.Reference())
	}
	// AlreadyResolved is silent — the message edit will reflect the
	// final state. Races are rare and the popup is more confusing than
	// useful.
}

// lookupToolCallByMessageID finds the tool-call-id whose posted message
// matches messageID, or "" if none. Caller holds g.mu.
func lookupToolCallByMessageID(messages map[string]string, messageID string) string {
	for toolCallID, msgID := range messages {
		if msgID == messageID {
			return toolCallID
		}
	}
	return ""
}

// discordResponderID picks the most stable identity for the user who
// clicked. Falls back to the username when interaction-member info is
// absent (DMs, missing intents, …).
func discordResponderID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return "discord:" + i.Member.User.Username
	}
	if i.User != nil {
		return "discord:" + i.User.Username
	}
	return "discord:unknown"
}
