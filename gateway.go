package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	gatewaysdk "github.com/mlund01/squadron-gateway-sdk"
)

type discordGateway struct {
	api gatewaysdk.SquadronAPI

	mu        sync.Mutex
	session   *discordgo.Session
	channelID string

	// tool_call_id → posted Discord message id, so we can edit the
	// message when the request resolves (regardless of which surface
	// resolved it).
	messages map[string]string

	checkpointPath string
}

func newDiscordGateway() *discordGateway {
	return &discordGateway{messages: map[string]string{}}
}

func (g *discordGateway) Configure(ctx context.Context, settings map[string]string, api gatewaysdk.SquadronAPI) error {
	g.api = api

	botToken := strings.TrimSpace(settings["bot_token"])
	channelID := strings.TrimSpace(settings["channel_id"])
	channelName := strings.TrimPrefix(strings.TrimSpace(settings["channel_name"]), "#")
	guildName := strings.TrimSpace(settings["guild_name"])
	if botToken == "" {
		return fmt.Errorf("missing setting: bot_token")
	}
	if channelID == "" && channelName == "" {
		return fmt.Errorf("missing setting: one of channel_id or channel_name is required")
	}
	if channelID != "" && channelName != "" {
		return fmt.Errorf("conflicting settings: set channel_id or channel_name, not both")
	}
	g.checkpointPath = strings.TrimSpace(settings["checkpoint_path"])
	if g.checkpointPath == "" {
		g.checkpointPath = ".squadron-discord-gateway.json"
	}

	sess, err := discordgo.New("Bot " + botToken)
	if err != nil {
		return fmt.Errorf("discord: new session: %w", err)
	}
	sess.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent
	sess.AddHandler(g.onInteraction)
	sess.AddHandler(g.onMessage)

	if err := sess.Open(); err != nil {
		return fmt.Errorf("discord: open: %w", err)
	}

	if channelID == "" {
		resolved, err := resolveChannelByName(sess, channelName, guildName)
		if err != nil {
			_ = sess.Close()
			return err
		}
		channelID = resolved
		log.Printf("resolved channel_name=%q to channel_id=%s", channelName, channelID)
	}
	g.channelID = channelID

	g.mu.Lock()
	g.session = sess
	g.mu.Unlock()

	if err := g.catchUp(ctx); err != nil {
		// Best-effort: a missed catch-up doesn't block live events.
		log.Printf("catch-up failed: %v", err)
	}

	log.Printf("discord gateway ready (channel=%s)", channelID)
	return nil
}

func (g *discordGateway) OnHumanInputRequested(ctx context.Context, rec gatewaysdk.HumanInputRecord) error {
	return g.postQuestion(rec)
}

func (g *discordGateway) OnHumanInputResolved(ctx context.Context, rec gatewaysdk.HumanInputRecord) error {
	g.advanceCheckpoint(rec.ResolvedAt)
	return g.markResolved(rec)
}

func (g *discordGateway) OnNotification(ctx context.Context, rec gatewaysdk.NotificationRecord) error {
	return g.postNotification(rec)
}

func (g *discordGateway) PostMessage(ctx context.Context, req gatewaysdk.PostMessageRequest) error {
	return g.postMessage(req.Payload, req.Attachments)
}

func (g *discordGateway) MessageToolSpec(ctx context.Context) (gatewaysdk.MessageToolSpec, error) {
	return gatewaysdk.MessageToolSpec{
		Description: discordPostDescription,
		ParamsSchema: discordPostSchema,
	}, nil
}

func (g *discordGateway) Shutdown(ctx context.Context) error {
	g.mu.Lock()
	sess := g.session
	g.session = nil
	g.mu.Unlock()
	if sess != nil {
		_ = sess.Close()
	}
	return nil
}

// catchUp replays everything since the local checkpoint through the
// live-event handlers. Per-row failures are logged so one bad row
// doesn't block the rest.
func (g *discordGateway) catchUp(ctx context.Context) error {
	rows, _, err := g.api.ListHumanInputs(ctx, gatewaysdk.HumanInputFilter{
		Since:       g.readCheckpoint(),
		OldestFirst: true,
		Limit:       200,
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.State == gatewaysdk.HumanInputStateResolved {
			if err := g.markResolved(r); err != nil {
				log.Printf("catch-up resolved %s: %v", r.ToolCallID, err)
			}
			g.advanceCheckpoint(r.ResolvedAt)
			continue
		}
		if err := g.postQuestion(r); err != nil {
			log.Printf("catch-up post %s: %v", r.ToolCallID, err)
		}
		g.advanceCheckpoint(r.RequestedAt)
	}
	return nil
}
