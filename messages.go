package main

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	gatewaysdk "github.com/mlund01/squadron-gateway-sdk"
)

// Outbound: posts and edits Discord messages in response to squadron
// state changes (a new request appearing, an existing one resolving
// from any surface). Inbound — operator clicks and replies — lives in
// handlers.go.

// postQuestion creates a Discord message representing an open
// request. Idempotent on tool_call_id: if we've already posted this
// one, we skip rather than double-post (catch-up replays may overlap
// with live events).
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

// markResolved edits the original Discord message: strikethrough on
// the question, ✅ + answer underneath, components cleared so a stale
// click can't try to re-resolve. No-op when we never posted the
// question (e.g. the resolution arrived before we caught up) or when
// the session is torn down mid-shutdown.
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
