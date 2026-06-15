package main

import (
	"encoding/json"
	"strings"
	"testing"

	gatewaysdk "github.com/mlund01/squadron-gateway-sdk"
)

// TestPostPayloadParsing pins the JSON contract the LLM produces against
// the Go struct postMessage decodes. A renamed/dropped json tag here means
// the agent's text, channel, or embeds silently never reach Discord — so
// every field of a full payload must survive the unmarshal. Attachments are
// NOT part of the payload: squadron resolves local files and ships them as
// PostMessageRequest.Attachments bytes, so they never appear in this JSON.
func TestPostPayloadParsing(t *testing.T) {
	raw := `{
		"text": "deploy done",
		"channel": "#ops",
		"embeds": [{"title": "v2", "description": "shipped", "url": "https://x", "color": 5}]
	}`

	var p discordPostPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Text != "deploy done" {
		t.Errorf("Text: got %q", p.Text)
	}
	if p.Channel != "#ops" {
		t.Errorf("Channel: got %q", p.Channel)
	}
	if len(p.Embeds) != 1 {
		t.Fatalf("Embeds: got %d, want 1", len(p.Embeds))
	}
	e := p.Embeds[0]
	if e.Title != "v2" || e.Description != "shipped" || e.URL != "https://x" || e.Color != 5 {
		t.Errorf("embed fields lost: %+v", e)
	}
}

func TestPostPayloadTextOnly(t *testing.T) {
	var p discordPostPayload
	if err := json.Unmarshal([]byte(`{"text":"hi"}`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Text != "hi" {
		t.Errorf("Text: got %q", p.Text)
	}
	if p.Channel != "" || len(p.Embeds) != 0 {
		t.Errorf("optional fields should be empty: %+v", p)
	}
}

func TestBuildNotificationBody(t *testing.T) {
	cases := []struct {
		name     string
		rec      gatewaysdk.NotificationRecord
		contains []string
		excludes []string
	}{
		{
			name:     "completed with title and message",
			rec:      gatewaysdk.NotificationRecord{Event: "mission_completed", Title: "All done", Message: "3 tasks ok"},
			contains: []string{"✅", "**All done**", "3 tasks ok"},
		},
		{
			name:     "failed renders error in a code fence",
			rec:      gatewaysdk.NotificationRecord{Event: "mission_failed", Title: "Failed", Error: "boom"},
			contains: []string{"❌", "**Failed**", "```", "boom"},
		},
		{
			name:     "no title falls back to the event name",
			rec:      gatewaysdk.NotificationRecord{Event: "mission_completed"},
			contains: []string{"**mission_completed**"},
		},
		{
			name:     "completed carries no error fence",
			rec:      gatewaysdk.NotificationRecord{Event: "mission_completed", Title: "Done"},
			excludes: []string{"```"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := buildNotificationBody(c.rec)
			for _, want := range c.contains {
				if !strings.Contains(body, want) {
					t.Errorf("body %q missing %q", body, want)
				}
			}
			for _, no := range c.excludes {
				if strings.Contains(body, no) {
					t.Errorf("body %q should not contain %q", body, no)
				}
			}
		})
	}
}

func TestBuildNotificationBodyTruncatesLongError(t *testing.T) {
	huge := strings.Repeat("x", 5000)
	body := buildNotificationBody(gatewaysdk.NotificationRecord{Event: "mission_failed", Error: huge})
	if len(body) >= 5000 {
		t.Errorf("long error should be truncated; body len %d", len(body))
	}
}
