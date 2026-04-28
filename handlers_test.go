package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bwmarrin/discordgo"
	gatewaysdk "github.com/mlund01/squadron-gateway-sdk"
)

func TestDiscordResponderID(t *testing.T) {
	cases := []struct {
		name string
		in   *discordgo.InteractionCreate
		want string
	}{
		{
			name: "uses Member.User.Username when present",
			in: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
				Member: &discordgo.Member{User: &discordgo.User{Username: "alice"}},
			}},
			want: "discord:alice",
		},
		{
			name: "falls back to Interaction.User in DMs (no Member)",
			in: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
				User: &discordgo.User{Username: "bob"},
			}},
			want: "discord:bob",
		},
		{
			name: "discord:unknown when neither is set",
			in:   &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{}},
			want: "discord:unknown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := discordResponderID(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLookupToolCallByMessageID(t *testing.T) {
	messages := map[string]string{
		"tc-1": "msg-A",
		"tc-2": "msg-B",
	}
	if got := lookupToolCallByMessageID(messages, "msg-B"); got != "tc-2" {
		t.Errorf("got %q; want tc-2", got)
	}
	if got := lookupToolCallByMessageID(messages, "msg-Z"); got != "" {
		t.Errorf("unmatched reply should return empty string, got %q", got)
	}
	if got := lookupToolCallByMessageID(messages, ""); got != "" {
		t.Errorf("empty reply id should return empty string, got %q", got)
	}
}

func TestDecodeInteractionResponseRoutesByCustomID(t *testing.T) {
	cases := []struct {
		name       string
		customID   string
		values     []string
		wantTC     string
		wantResp   string
		wantOK     bool
	}{
		{
			name:     "button click → choice from custom_id",
			customID: encodeCustomID("tc-1", "Option A"),
			values:   nil,
			wantTC:   "tc-1",
			wantResp: "Option A",
			wantOK:   true,
		},
		{
			name:     "select-menu submission → JSON-encoded values",
			customID: encodeSelectMenuCustomID("tc-1"),
			values:   []string{"A", "C"},
			wantTC:   "tc-1",
			wantResp: `["A","C"]`,
			wantOK:   true,
		},
		{
			name:     "foreign custom_id → ok=false (component we didn't render)",
			customID: "not-our-prefix",
			wantOK:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tcID, resp, ok := decodeInteractionResponse(tc.customID, tc.values)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if tcID != tc.wantTC {
				t.Errorf("toolCallID: got %q, want %q", tcID, tc.wantTC)
			}
			if resp != tc.wantResp {
				t.Errorf("response: got %q, want %q", resp, tc.wantResp)
			}
		})
	}
}

// fakeSquadronAPI exercises the resolve flow in-process; records
// each call so tests can inspect what was sent upstream.
type fakeSquadronAPI struct {
	mu            sync.Mutex
	resolveCalls  []resolveCall
	listCalls     atomic.Int32
	resolveResult gatewaysdk.ResolveResult
	resolveErr    error
	listResult    []gatewaysdk.HumanInputRecord
	listTotal     int
	listErr       error
}

type resolveCall struct {
	ToolCallID, Response, Responder string
}

func (f *fakeSquadronAPI) ListHumanInputs(_ context.Context, _ gatewaysdk.HumanInputFilter) ([]gatewaysdk.HumanInputRecord, int, error) {
	f.listCalls.Add(1)
	return f.listResult, f.listTotal, f.listErr
}

func (f *fakeSquadronAPI) ResolveHumanInput(_ context.Context, toolCallID, response, responder string) (gatewaysdk.ResolveResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolveCalls = append(f.resolveCalls, resolveCall{toolCallID, response, responder})
	return f.resolveResult, f.resolveErr
}

func (f *fakeSquadronAPI) snapshotResolveCalls() []resolveCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]resolveCall(nil), f.resolveCalls...)
}

func TestFakeAPIWiring(t *testing.T) {
	api := &fakeSquadronAPI{
		resolveResult: gatewaysdk.ResolveResult{
			Record: gatewaysdk.HumanInputRecord{ToolCallID: "tc-1", Response: "yes"},
		},
	}
	g := newDiscordGateway()
	g.api = api

	res, err := g.api.ResolveHumanInput(context.Background(), "tc-1", "yes", "discord:alice")
	if err != nil {
		t.Fatal(err)
	}
	if res.Record.Response != "yes" {
		t.Errorf("response mismatch: got %q", res.Record.Response)
	}
	if calls := api.snapshotResolveCalls(); len(calls) != 1 || calls[0].Responder != "discord:alice" {
		t.Errorf("expected 1 resolve call with responder=discord:alice, got %+v", calls)
	}
}

func TestFakeAPIErrorPath(t *testing.T) {
	api := &fakeSquadronAPI{resolveErr: errors.New("rpc closed")}
	g := newDiscordGateway()
	g.api = api

	if _, err := g.api.ResolveHumanInput(context.Background(), "tc", "x", "u"); err == nil {
		t.Error("expected error from fake API")
	}
}
