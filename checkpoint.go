package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Checkpoint = the latest event timestamp the gateway has processed,
// persisted so a restart can replay only what it missed. Lost or
// corrupt files fall back to zero (replay everything in squadron's
// recent window).

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
	data, err := json.Marshal(checkpoint{Latest: t})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(g.checkpointPath), 0755); err != nil {
		return
	}
	_ = os.WriteFile(g.checkpointPath, data, 0644)
}
