package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckpointRoundTrip(t *testing.T) {
	dir := t.TempDir()
	g := newDiscordGateway()
	g.checkpointPath = filepath.Join(dir, "cp.json")

	// Initially: no file → readCheckpoint returns zero time.
	if got := g.readCheckpoint(); !got.IsZero() {
		t.Errorf("readCheckpoint with no file should return zero time, got %v", got)
	}

	now := time.Now().UTC().Truncate(time.Second)
	g.advanceCheckpoint(now)

	if got := g.readCheckpoint(); !got.Equal(now) {
		t.Errorf("readCheckpoint after advance: got %v, want %v", got, now)
	}
}

func TestCheckpointAdvanceIgnoresZeroTime(t *testing.T) {
	dir := t.TempDir()
	g := newDiscordGateway()
	g.checkpointPath = filepath.Join(dir, "cp.json")

	now := time.Now().UTC().Truncate(time.Second)
	g.advanceCheckpoint(now)
	g.advanceCheckpoint(time.Time{}) // zero — should NOT clobber

	if got := g.readCheckpoint(); !got.Equal(now) {
		t.Errorf("zero advanceCheckpoint should not clobber a previous valid value; got %v want %v", got, now)
	}
}

func TestCheckpointIsNoOpWhenPathEmpty(t *testing.T) {
	g := newDiscordGateway()
	g.checkpointPath = ""

	g.advanceCheckpoint(time.Now())
	if got := g.readCheckpoint(); !got.IsZero() {
		t.Errorf("read with empty path should return zero, got %v", got)
	}
}

func TestCheckpointReadIgnoresMalformedFile(t *testing.T) {
	dir := t.TempDir()
	g := newDiscordGateway()
	g.checkpointPath = filepath.Join(dir, "cp.json")
	if err := os.WriteFile(g.checkpointPath, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := g.readCheckpoint(); !got.IsZero() {
		t.Errorf("malformed checkpoint should fall back to zero time, got %v", got)
	}
}
