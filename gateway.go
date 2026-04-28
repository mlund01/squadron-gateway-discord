package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	gatewaysdk "github.com/mlund01/squadron-gateway-sdk"
)

// discordGateway is the SDK-facing implementation. Almost all the
// state-shuffling lives in component-specific helpers (postMessage,
// handleInteraction, …); this struct mostly holds dependencies.
type discordGateway struct {
	api gatewaysdk.SquadronAPI

	mu        sync.Mutex
	session   *discordgo.Session
	channelID string

	// messages maps tool_call_id → Discord message id so we can edit
	// the message when squadron tells us the request was resolved
	// (possibly by another surface, e.g. commander).
	messages map[string]string

	// Checkpoint for catch-up on restart. Persists the latest
	// requested_at OR resolved_at the gateway has processed.
	checkpointPath string
}

func newDiscordGateway() *discordGateway {
	return &discordGateway{messages: map[string]string{}}
}

// Configure runs once at gateway startup. We open the Discord
// session, register an interaction handler for button clicks, run
// the catch-up against squadron's API, and finally hand control
// back so squadron can begin pushing live events.
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
// ask_human request. We post it to the configured Discord channel.
func (g *discordGateway) OnHumanInputRequested(ctx context.Context, rec gatewaysdk.HumanInputRecord) error {
	return g.postQuestion(rec)
}

// OnHumanInputResolved is called whenever a request transitions to
// resolved, regardless of who resolved it. We edit the original
// Discord message so the audit trail (and the buttons) reflect the
// answer.
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

// catchUp is the reconnect-friendly bootstrap: squadron returns
// everything that happened since our local checkpoint, and we replay
// it through the same handlers we use for live events.
func (g *discordGateway) catchUp(ctx context.Context) error {
	since := g.readCheckpoint()
	rows, _, err := g.api.ListHumanInputs(ctx, gatewaysdk.HumanInputFilter{
		Since:       since,
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

// postQuestion creates a Discord message representing an open
// request. Idempotent on tool_call_id — if we already posted this
// one we skip rather than double-post (catch-up replays may overlap
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

	body := buildMessageBody(rec)
	components := buildComponents(rec)

	msg, err := sess.ChannelMessageSendComplex(channel, &discordgo.MessageSend{
		Content:    body,
		Components: components,
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

// markResolved edits the Discord message to show the answer + clears
// the buttons so a stale click can't try to re-resolve.
func (g *discordGateway) markResolved(rec gatewaysdk.HumanInputRecord) error {
	g.mu.Lock()
	msgID, ok := g.messages[rec.ToolCallID]
	sess := g.session
	channel := g.channelID
	g.mu.Unlock()
	if !ok || sess == nil {
		return nil // nothing to edit (we never posted, or session torn down)
	}

	body := buildResolvedBody(rec)
	empty := []discordgo.MessageComponent{}
	_, err := sess.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    channel,
		ID:         msgID,
		Content:    &body,
		Components: &empty,
	})
	if err != nil {
		return fmt.Errorf("edit message: %w", err)
	}
	return nil
}

// onInteraction handles both button clicks (single-select) and select-
// menu submissions (multi-select). The custom_id picks which:
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

	var toolCallID, response string
	if tc, ok := decodeSelectMenuCustomID(data.CustomID); ok {
		toolCallID = tc
		// Values are the option `Value` strings the user picked. We set
		// Value=label when building the menu, so this is already the
		// human-readable choice strings.
		encoded, err := json.Marshal(data.Values)
		if err != nil {
			log.Printf("encode select-menu values: %v", err)
			return
		}
		response = string(encoded)
	} else if tc, choice, ok := decodeCustomID(data.CustomID); ok {
		toolCallID = tc
		response = choice
	} else {
		return
	}

	responder := discordResponderID(i)

	res, err := g.api.ResolveHumanInput(context.Background(), toolCallID, response, responder)
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

// onMessage handles free-text replies. A user replying to a question
// message in the channel sends the message body as the response —
// useful when there are no choices, or the user wants to override.
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
	var matched string
	for toolCallID, msgID := range g.messages {
		if msgID == m.MessageReference.MessageID {
			matched = toolCallID
			break
		}
	}
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

// ─────────────────────────────────────────────────────────────────────────────
// Message building
// ─────────────────────────────────────────────────────────────────────────────

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

// formatResolvedResponse renders the answer for the resolved message
// body. For multi-select questions the on-the-wire response is a JSON
// array string ([]"A","C"]) which we expand to a human-friendly comma
// list so the channel doesn't show raw JSON. If parsing fails for any
// reason we fall back to the raw string — the audit trail keeps
// whatever squadron stored.
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

// buildComponents returns the action rows for a question.
//
// Single-select: a row of choice buttons (Discord caps 5 per row, 5
// rows, 25 total).
//
// Multi-select: a single StringSelect dropdown with MinValues=1 and
// MaxValues=len(choices). Discord submits the interaction once the
// user closes the dropdown after picking, with all values in
// data.Values — no follow-up "submit" button needed.
func buildComponents(rec gatewaysdk.HumanInputRecord) []discordgo.MessageComponent {
	if len(rec.Choices) == 0 {
		return nil
	}
	if rec.MultiSelect {
		return buildSelectMenuComponents(rec)
	}
	return buildButtonComponents(rec)
}

func buildButtonComponents(rec gatewaysdk.HumanInputRecord) []discordgo.MessageComponent {
	rows := []discordgo.MessageComponent{}
	row := []discordgo.MessageComponent{}
	for i, choice := range rec.Choices {
		if i >= 25 {
			break
		}
		row = append(row, discordgo.Button{
			Label:    truncate(choice, 80),
			Style:    discordgo.SecondaryButton,
			CustomID: encodeCustomID(rec.ToolCallID, choice),
		})
		if len(row) == 5 {
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
		if i >= 25 {
			break
		}
		options = append(options, discordgo.SelectMenuOption{
			Label: truncate(choice, 100),
			// `Value` is what Discord sends back in data.Values when the
			// user picks this option. Use the choice text directly so the
			// gateway can echo it without a lookup table.
			Value: truncate(choice, 100),
		})
	}
	maxVals := len(options)
	if maxVals > 25 {
		maxVals = 25
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

// ─────────────────────────────────────────────────────────────────────────────
// Checkpoint persistence
// ─────────────────────────────────────────────────────────────────────────────

type checkpoint struct {
	Latest time.Time `json:"latest"`
}

func (g *discordGateway) readCheckpoint() time.Time {
	if g.checkpointPath == "" {
		return time.Time{}
	}
	data, err := os.ReadFile(g.checkpointPath)
	if err != nil {
		return time.Time{}
	}
	var cp checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return time.Time{}
	}
	return cp.Latest
}

func (g *discordGateway) advanceCheckpoint(t time.Time) {
	if t.IsZero() || g.checkpointPath == "" {
		return
	}
	cp := checkpoint{Latest: t}
	data, err := json.Marshal(cp)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(g.checkpointPath), 0755); err != nil {
		return
	}
	_ = os.WriteFile(g.checkpointPath, data, 0644)
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

const (
	customIDSep      = "::"
	selectMenuMarker = "__select__"
)

func encodeCustomID(toolCallID, choice string) string {
	// Discord caps custom_id at 100 chars. Tool call ids are uuid-shaped
	// (~36 chars); the choice is everything after.
	id := "sq" + customIDSep + toolCallID + customIDSep + choice
	if len(id) > 100 {
		id = id[:100]
	}
	return id
}

func decodeCustomID(s string) (toolCallID, choice string, ok bool) {
	parts := strings.SplitN(s, customIDSep, 3)
	if len(parts) != 3 || parts[0] != "sq" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// encodeSelectMenuCustomID encodes a tool-call-id for a multi-select
// dropdown. Decoded values arrive in interaction.MessageComponentData.Values
// rather than the custom_id, so we only need the tool-call-id here.
func encodeSelectMenuCustomID(toolCallID string) string {
	return "sq" + customIDSep + toolCallID + customIDSep + selectMenuMarker
}

// decodeSelectMenuCustomID is the inverse — returns the tool-call-id and
// ok=true if the id is in our select-menu shape.
func decodeSelectMenuCustomID(s string) (toolCallID string, ok bool) {
	tc, marker, decoded := decodeCustomID(s)
	if !decoded || marker != selectMenuMarker {
		return "", false
	}
	return tc, true
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

func displayResponder(s string) string {
	if s == "" {
		return "another operator"
	}
	return s
}

// truncate caps a string at `max` BYTES, replacing the tail with "…"
// when truncation is needed. The ellipsis is 3 bytes in UTF-8, so a
// naive `s[:max-1] + "…"` produces a result that overruns the byte
// limit by 2 — important for Discord, which rejects component labels
// over its declared limit (80 for buttons, 100 for select-menu
// options) measured in characters/bytes-on-the-wire.
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

// resolveChannelByName walks the guilds the bot is a member of and
// returns the ID of the text channel matching channelName (case
// insensitive). channel_name is a friendlier knob than channel_id but
// trades a startup REST round-trip and the risk that a rename in
// Discord breaks the gateway until config is refreshed.
//
// If guildHint is set we only consider the matching guild (also case
// insensitive). Without it, the bot may legitimately be in multiple
// guilds with the same channel name — those collisions surface as a
// startup error so the operator picks one explicitly rather than
// silently posting to the wrong server.
func resolveChannelByName(sess *discordgo.Session, channelName, guildHint string) (string, error) {
	guilds, err := sess.UserGuilds(200, "", "", false)
	if err != nil {
		return "", fmt.Errorf("list bot guilds: %w", err)
	}
	if len(guilds) == 0 {
		return "", fmt.Errorf("bot is not a member of any guild — invite it first")
	}

	type match struct {
		guildName, channelID, channelName string
	}
	var matches []match
	for _, gg := range guilds {
		if guildHint != "" && !strings.EqualFold(gg.Name, guildHint) {
			continue
		}
		channels, err := sess.GuildChannels(gg.ID)
		if err != nil {
			log.Printf("list channels for guild %q: %v", gg.Name, err)
			continue
		}
		for _, c := range channels {
			if c.Type != discordgo.ChannelTypeGuildText {
				continue
			}
			if strings.EqualFold(c.Name, channelName) {
				matches = append(matches, match{gg.Name, c.ID, c.Name})
			}
		}
	}

	switch len(matches) {
	case 0:
		if guildHint != "" {
			return "", fmt.Errorf("no text channel %q in guild %q (bot may not have View Channel permission)", channelName, guildHint)
		}
		return "", fmt.Errorf("no text channel %q found in any guild the bot is in", channelName)
	case 1:
		return matches[0].channelID, nil
	default:
		labels := make([]string, 0, len(matches))
		for _, m := range matches {
			labels = append(labels, fmt.Sprintf("%s/#%s", m.guildName, m.channelName))
		}
		return "", fmt.Errorf("channel %q matches in multiple guilds (%s) — set guild_name to disambiguate", channelName, strings.Join(labels, ", "))
	}
}
