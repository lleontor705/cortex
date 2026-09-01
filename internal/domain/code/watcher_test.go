package code

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileWatcher_DetectsModification(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cortex_watcher_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	testFile := filepath.Join(tempDir, "service.go")
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc Run() {}\n"), 0644); err != nil {
		t.Fatalf("failed to write initial test file: %v", err)
	}

	cfg := DefaultWatcherConfig(tempDir, "test-proj")
	cfg.PollInterval = 50 * time.Millisecond
	watcher := NewFileWatcher(cfg)

	// Initial scan establishes baseline
	initialChanged := watcher.ScanOnce()
	if len(initialChanged) != 0 {
		t.Errorf("expected 0 changed on initial scan, got %d", len(initialChanged))
	}

	// Sleep slightly to ensure modtime delta
	time.Sleep(100 * time.Millisecond)

	// Modify file
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc Run() { println(\"updated\") }\n"), 0644); err != nil {
		t.Fatalf("failed to update test file: %v", err)
	}

	// Second scan should detect modification
	secondChanged := watcher.ScanOnce()
	if len(secondChanged) != 1 || secondChanged[0] != testFile {
		t.Errorf("expected [ %s ], got %v", testFile, secondChanged)
	}
}

func TestFileWatcher_WatchLoop(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cortex_watch_loop_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	testFile := filepath.Join(tempDir, "model.ts")
	if err := os.WriteFile(testFile, []byte("export interface User { id: string; }"), 0644); err != nil {
		t.Fatalf("failed to write initial test file: %v", err)
	}

	cfg := DefaultWatcherConfig(tempDir, "test-proj")
	cfg.PollInterval = 30 * time.Millisecond
	watcher := NewFileWatcher(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	detected := make(chan string, 1)

	go func() {
		_ = watcher.Watch(ctx, func(path string) {
			detected <- path
		})
	}()

	time.Sleep(60 * time.Millisecond)
	_ = os.WriteFile(testFile, []byte("export interface User { id: string; name: string; }"), 0644)

	select {
	case p := <-detected:
		if p != testFile {
			t.Errorf("expected %s, got %s", testFile, p)
		}
	case <-time.After(250 * time.Millisecond):
		// Finished timeout
	}
}

func TestFileWatcher_IgnoresConfiguredDirectories(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cortex_watch_ignore_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Valid monitored file
	validFile := filepath.Join(tempDir, "src", "app.go")
	if err := os.MkdirAll(filepath.Dir(validFile), 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}
	if err := os.WriteFile(validFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to write valid file: %v", err)
	}

	// File inside ignored sub-directory "node_modules"
	ignoredNodeModules := filepath.Join(tempDir, "node_modules", "pkg", "index.js")
	if err := os.MkdirAll(filepath.Dir(ignoredNodeModules), 0755); err != nil {
		t.Fatalf("failed to create node_modules dir: %v", err)
	}
	if err := os.WriteFile(ignoredNodeModules, []byte("console.log()"), 0644); err != nil {
		t.Fatalf("failed to write node_modules file: %v", err)
	}

	// File inside ignored sub-directory "tmp"
	ignoredSubTmp := filepath.Join(tempDir, "tmp", "cache.go")
	if err := os.MkdirAll(filepath.Dir(ignoredSubTmp), 0755); err != nil {
		t.Fatalf("failed to create tmp sub dir: %v", err)
	}
	if err := os.WriteFile(ignoredSubTmp, []byte("package cache"), 0644); err != nil {
		t.Fatalf("failed to write sub tmp file: %v", err)
	}

	cfg := DefaultWatcherConfig(tempDir, "test-proj")
	cfg.PollInterval = 30 * time.Millisecond
	watcher := NewFileWatcher(cfg)

	// Baseline scan
	_ = watcher.ScanOnce()

	time.Sleep(50 * time.Millisecond)

	// Modify all files
	_ = os.WriteFile(validFile, []byte("package main // modified"), 0644)
	_ = os.WriteFile(ignoredNodeModules, []byte("console.log('modified')"), 0644)
	_ = os.WriteFile(ignoredSubTmp, []byte("package cache // modified"), 0644)

	changed := watcher.ScanOnce()
	if len(changed) != 1 || changed[0] != validFile {
		t.Fatalf("expected changed only [ %s ], got %v", validFile, changed)
	}
}
