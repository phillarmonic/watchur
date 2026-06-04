package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestEventOccurredAfterStartIgnoresStaleWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stale.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now()
	staleTime := startedAt.Add(-time.Second)
	if err := os.Chtimes(path, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	if eventOccurredAfterStart(fsnotify.Event{Name: path, Op: fsnotify.Write}, startedAt) {
		t.Fatalf("expected stale write event to be ignored")
	}
}

func TestEventOccurredAfterStartAcceptsFreshWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.txt")
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now().Add(-time.Second)
	freshTime := time.Now()
	if err := os.Chtimes(path, freshTime, freshTime); err != nil {
		t.Fatal(err)
	}

	if !eventOccurredAfterStart(fsnotify.Event{Name: path, Op: fsnotify.Write}, startedAt) {
		t.Fatalf("expected fresh write event to be accepted")
	}
}

func TestEventOccurredAfterStartAlwaysAcceptsCreate(t *testing.T) {
	startedAt := time.Now()
	if !eventOccurredAfterStart(fsnotify.Event{Name: "new.txt", Op: fsnotify.Create}, startedAt) {
		t.Fatalf("expected create event to be accepted")
	}
}
