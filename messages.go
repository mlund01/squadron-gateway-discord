package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strings"

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

// postNotification posts a one-way mission-lifecycle notification. Unlike
// questions there is nothing to track or edit, so no idempotency map. When the
// record carries a per-mission channel override it is resolved here, falling
// back to the configured default channel.
func (g *discordGateway) postNotification(rec gatewaysdk.NotificationRecord) error {
	g.mu.Lock()
	sess := g.session
	channel := g.channelID
	g.mu.Unlock()
	if sess == nil {
		return fmt.Errorf("discord session not initialized")
	}
	if rec.Channel != "" {
		channel = g.resolveNotifyChannel(sess, rec.Channel, channel)
	}

	if _, err := sess.ChannelMessageSend(channel, buildNotificationBody(rec)); err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	return nil
}

// discordPostDescription + discordPostSchema are advertised to squadron via
// MessageToolSpec so the LLM knows how to format a Discord post.
const discordPostDescription = "Post a message to the Discord channel. " +
	"`text` is the message body and supports Discord markdown (**bold**, _italics_, `code`, > quotes, lists, links). " +
	"`channel` optionally overrides the destination — a channel name (with or without a leading #) or id. " +
	"`embeds` is an optional array of rich embed cards."

const discordPostSchema = `{
  "type": "object",
  "properties": {
    "text": {"type": "string", "description": "Message body (Discord markdown supported)."},
    "channel": {"type": "string", "description": "Optional channel name or id override."},
    "embeds": {
      "type": "array",
      "description": "Optional rich embed cards.",
      "items": {
        "type": "object",
        "properties": {
          "title": {"type": "string"},
          "description": {"type": "string"},
          "url": {"type": "string"},
          "color": {"type": "integer", "description": "Decimal RGB color, e.g. 5763719."}
        }
      }
    }
  },
  "required": ["text"]
}`

type discordPostPayload struct {
	Text    string         `json:"text"`
	Channel string         `json:"channel,omitempty"`
	Embeds  []discordEmbed `json:"embeds,omitempty"`
}

type discordEmbed struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
	Color       int    `json:"color,omitempty"`
}

// postMessage renders a builtins.gateway.post payload (text + markdown,
// embeds) plus squadron-supplied file attachments and posts it, honoring an
// optional channel override (falling back to the configured default channel).
func (g *discordGateway) postMessage(payload string, attachments []gatewaysdk.FileAttachment) error {
	var p discordPostPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("parse message payload: %w", err)
	}
	g.mu.Lock()
	sess := g.session
	channel := g.channelID
	g.mu.Unlock()
	if sess == nil {
		return fmt.Errorf("discord session not initialized")
	}
	if p.Channel != "" {
		channel = g.resolveNotifyChannel(sess, p.Channel, channel)
	}

	msg := &discordgo.MessageSend{Content: p.Text}
	for _, e := range p.Embeds {
		msg.Embeds = append(msg.Embeds, &discordgo.MessageEmbed{
			Title:       e.Title,
			Description: e.Description,
			URL:         e.URL,
			Color:       e.Color,
		})
	}
	for _, a := range attachments {
		name := a.Filename
		if name == "" {
			name = "attachment"
		}
		msg.Files = append(msg.Files, &discordgo.File{
			Name:        name,
			ContentType: a.MimeType,
			Reader:      bytes.NewReader(a.Content),
		})
	}
	if _, err := sess.ChannelMessageSendComplex(channel, msg); err != nil {
		return fmt.Errorf("post message: %w", err)
	}
	return nil
}

// resolveNotifyChannel turns a per-mission channel override into a channel ID.
// A numeric override is treated as an ID; anything else is resolved by name
// (leading '#' stripped). On any failure it logs and falls back to def.
func (g *discordGateway) resolveNotifyChannel(sess *discordgo.Session, override, def string) string {
	if isNumericID(override) {
		return override
	}
	name := strings.TrimPrefix(override, "#")
	resolved, err := resolveChannelByName(sess, name, "")
	if err != nil {
		log.Printf("notification channel override %q: %v — falling back to default", override, err)
		return def
	}
	return resolved
}

func isNumericID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
