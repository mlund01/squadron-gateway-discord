package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Checkpoint persistence for the catch-up cursor.
//
// The gateway keeps a single timestamp on disk: the latest
// requested_at OR resolved_at it has processed. On restart, catchUp
// asks squadron for everything since this timestamp so transient
// disconnects don't drop events. Lost / corrupt files fall back to
// zero (replay everything available), which is safe because squadron
// returns a finite recent window.

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
