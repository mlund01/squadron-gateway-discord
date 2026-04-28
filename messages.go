package main

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	gatewaysdk "github.com/mlund01/squadron-gateway-sdk"
)

// postQuestion posts a fresh request to Discord. Idempotent on
// tool_call_id so catch-up replays don't double-post.
func (g *discordGateway) postQuestion(rec gatewaysdk.HumanInputRecord) error {
	g.mu.Lock()
	if _, exists := g.messages[rec.ToolCallID]; exists {
		g.mu.Unlock()
		return nil
	}
	sess := g.session
	channel := g.channelID
	g.mu.Unlock()
	if sess == nil {
		return fmt.Errorf("discord session not initialized")
	}

	msg, err := sess.ChannelMessageSendComplex(channel, &discordgo.MessageSend{
		Content:    buildMessageBody(rec),
		Components: buildComponents(rec),
	})
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}

	g.mu.Lock()
	g.messages[rec.ToolCallID] = msg.ID
	g.mu.Unlock()

	g.advanceCheckpoint(rec.RequestedAt)
	return nil
}

// markResolved edits the original message: strikethrough question +
// ✅ answer, components cleared so a stale click can't re-resolve.
// No-op if we never posted this question or the session is shut down.
func (g *discordGateway) markResolved(rec gatewaysdk.HumanInputRecord) error {
	g.mu.Lock()
	msgID, ok := g.messages[rec.ToolCallID]
	sess := g.session
	channel := g.channelID
	g.mu.Unlock()
	if !ok || sess == nil {
		return nil
	}

	body := buildResolvedBody(rec)
	empty := []discordgo.MessageComponent{}
	if _, err := sess.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    channel,
		ID:         msgID,
		Content:    &body,
		Components: &empty,
	}); err != nil {
		return fmt.Errorf("edit message: %w", err)
	}
	return nil
}
