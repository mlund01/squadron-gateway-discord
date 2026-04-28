package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// resolveChannelByName looks up a text channel by name across the
// bot's guilds. guildHint, when set, narrows the search to one guild
// — required when the bot is in multiple guilds with the same channel
// name. Ambiguity (multiple matches with no hint) is a startup error,
// not a silent pick.
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
