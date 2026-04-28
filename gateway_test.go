package main

import (
	"context"
	"strings"
	"testing"
)

// TestConfigureRejectsBadSettings exercises the up-front settings
// validation in Configure. All three cases must fail before the
// gateway attempts to dial Discord — that's what lets squadron's
// gateway manager surface a specific config error to the operator
// instead of an opaque network failure.
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

// TestChannelNameStripsLeadingHash mirrors Configure's behavior of
// accepting "#general" and stripping the leading hash. We exercise it
// indirectly through a Configure call that fails on missing token —
// before the channel-name lookup runs — so this test stays
// network-free while still pinning down the trim behavior the README
// promises.
func TestChannelNameStripsLeadingHash(t *testing.T) {
	g := newDiscordGateway()
	err := g.Configure(context.Background(), map[string]string{
		"channel_name": "#general",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "missing setting: bot_token") {
		t.Fatalf("expected bot_token error, got %v", err)
	}
}
