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

// discordGateway implements gatewaysdk.Gateway. The struct holds the
// minimum dependencies the handlers need; the heavy lifting lives in
// component-specific files:
//
//   - messages.go : postQuestion, markResolved (outbound to Discord)
//   - handlers.go : onInteraction, onMessage (inbound from Discord)
//   - render.go   : message body + components (pure functions)
//   - customid.go : button/select-menu custom_id encoding
//   - checkpoint.go : disconnect-recovery cursor on disk
//   - channel.go  : channel-name → channel-id resolution
type discordGateway struct {
	api gatewaysdk.SquadronAPI

	mu        sync.Mutex
	session   *discordgo.Session
	channelID string

	// messages maps tool_call_id → Discord message id so we can edit
	// the message when squadron tells us the request was resolved
	// (possibly by another surface, e.g. commander).
	messages map[string]string

	// checkpointPath is the file the gateway uses to remember the
	// latest event timestamp it has processed, so a restart catches
	// up rather than replaying from the beginning of time.
	checkpointPath string
}

func newDiscordGateway() *discordGateway {
	return &discordGateway{messages: map[string]string{}}
}

// Configure runs once at gateway startup. It validates settings,
// opens the Discord session, registers handlers, resolves channel-by-
// name (if requested), runs catch-up against squadron's API, and
// hands control back so squadron can begin pushing live events.
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
		// Don't abort startup — the gateway can keep running on live
		// events alone, and operators can resolve from another surface.
		log.Printf("catch-up failed: %v", err)
	}

	log.Printf("discord gateway ready (channel=%s)", channelID)
	return nil
}

// OnHumanInputRequested is called when squadron observes a new
// builtins.human.ask request. We post it to the configured channel.
func (g *discordGateway) OnHumanInputRequested(ctx context.Context, rec gatewaysdk.HumanInputRecord) error {
	return g.postQuestion(rec)
}

// OnHumanInputResolved is called whenever a request transitions to
// resolved, regardless of who resolved it. We edit the original
// Discord message so the audit trail (and the buttons) reflect the
// answer, and advance the catch-up cursor.
func (g *discordGateway) OnHumanInputResolved(ctx context.Context, rec gatewaysdk.HumanInputRecord) error {
	g.advanceCheckpoint(rec.ResolvedAt)
	return g.markResolved(rec)
}

// Shutdown is called when squadron tears down the subprocess.
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

// catchUp is the reconnect-friendly bootstrap. Squadron returns
// everything that happened since our local checkpoint and we replay
// each row through the same handlers we use for live events. Failures
// are logged per-row rather than aborting startup so a single bad row
// doesn't block the rest of the catch-up.
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
