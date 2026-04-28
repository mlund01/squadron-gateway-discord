package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/bwmarrin/discordgo"
)

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

	// Silent ack on success and on AlreadyResolved — the message edit
	// is the operator's confirmation. NotFound surfaces a popup since
	// the row is genuinely gone and the message won't be edited.
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
// Returns ok=false for any custom_id that didn't originate here.
// Multi-select values are JSON-encoded so the agent gets a canonical
// array string instead of a Go-printed slice.
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

// onMessage routes a free-text reply (a Discord reply to one of our
// posted question messages) through to ResolveHumanInput.
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
}

// lookupToolCallByMessageID — caller holds g.mu.
func lookupToolCallByMessageID(messages map[string]string, messageID string) string {
	for toolCallID, msgID := range messages {
		if msgID == messageID {
			return toolCallID
		}
	}
	return ""
}

// discordResponderID prefers Member.User over Interaction.User
// (Member is set in guild contexts; User in DMs / missing intents).
func discordResponderID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return "discord:" + i.Member.User.Username
	}
	if i.User != nil {
		return "discord:" + i.User.Username
	}
	return "discord:unknown"
}
