package main

import (
	"context"
	"strings"
	"testing"
)

// Settings validation must fail before any network call so squadron
// surfaces a specific config error instead of an opaque dial failure.
func TestConfigureRejectsBadSettings(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]string
		wantErr  string
	}{
		{
			name:     "missing bot_token",
			settings: map[string]string{"channel_id": "123"},
			wantErr:  "missing setting: bot_token",
		},
		{
			name:     "missing both channel_id and channel_name",
			settings: map[string]string{"bot_token": "xyz"},
			wantErr:  "channel_id or channel_name",
		},
		{
			name: "channel_id and channel_name both set",
			settings: map[string]string{
				"bot_token":    "xyz",
				"channel_id":   "123",
				"channel_name": "general",
			},
			wantErr: "not both",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newDiscordGateway()
			err := g.Configure(context.Background(), tc.settings, nil)
			if err == nil {
				t.Fatalf("expected error %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

// channel_name accepts a leading "#"; we don't actually resolve here
// (no network) — the test just pins that the leading "#" is parsed
// off without panicking and validation reaches the bot_token check.
func TestChannelNameStripsLeadingHash(t *testing.T) {
	g := newDiscordGateway()
	err := g.Configure(context.Background(), map[string]string{
		"channel_name": "#general",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "missing setting: bot_token") {
		t.Fatalf("expected bot_token error, got %v", err)
	}
}
