// Package code provides AST extraction, code graph management, and file watching capabilities.
package code

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WatcherConfig configures the background file watcher.
type WatcherConfig struct {
	Directory        string
	Project          string
	PollInterval     time.Duration
	DebounceDuration time.Duration
	Extensions       []string
	IgnoreDirs       []string
}

// DefaultWatcherConfig returns standard file watcher settings for Cortex.
func DefaultWatcherConfig(dir, project string) WatcherConfig {
	return WatcherConfig{
		Directory:        dir,
		Project:          project,
		PollInterval:     800 * time.Millisecond,
		DebounceDuration: 500 * time.Millisecond,
		Extensions:       []string{".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs"},
		IgnoreDirs:       []string{".git", "node_modules", "vendor", "dist", "bin", ".next", "tmp"},
	}
}

// FileWatcher monitors code files and triggers incremental AST indexing upon changes.
type FileWatcher struct {
	cfg      WatcherConfig
	modTimes map[string]time.Time
	mu       sync.RWMutex
}

// NewFileWatcher creates a new Zero-CGO background file watcher.
func NewFileWatcher(cfg WatcherConfig) *FileWatcher {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 800 * time.Millisecond
	}
	if cfg.DebounceDuration <= 0 {
		cfg.DebounceDuration = 500 * time.Millisecond
	}
	if len(cfg.Extensions) == 0 {
		cfg.Extensions = []string{".go", ".ts", ".tsx", ".py", ".rs"}
	}
	return &FileWatcher{
		cfg:      cfg,
		modTimes: make(map[string]time.Time),
	}
}

// isIgnored checks if a directory path matches ignore patterns.
func (w *FileWatcher) isIgnored(path string) bool {
	rel, err := filepath.Rel(w.cfg.Directory, path)
	if err != nil || rel == "." || rel == "" {
		return false
	}
	relSlash := filepath.ToSlash(rel)
	parts := strings.Split(relSlash, "/")
	for _, part := range parts {
		for _, ign := range w.cfg.IgnoreDirs {
			if part == ign {
				return true
			}
		}
	}
	return false
}

// isSupported checks if a file extension is monitored.
func (w *FileWatcher) isSupported(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, s := range w.cfg.Extensions {
		if ext == s {
			return true
		}
	}
	return false
}

// ScanOnce performs a single scan of the directory tree, returning modified or added files.
func (w *FileWatcher) ScanOnce() []string {
	w.mu.Lock()
	defer w.mu.Unlock()

	var changed []string
	currentFiles := make(map[string]bool)

	_ = filepath.Walk(w.cfg.Directory, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			if w.isIgnored(path) {
				return filepath.SkipDir
			}
			return nil
		}

		if !w.isSupported(path) {
			return nil
		}

		currentFiles[path] = true
		mod := info.ModTime()

		lastMod, exists := w.modTimes[path]
		if !exists {
			w.modTimes[path] = mod
			// First scan registers initial state without triggering mass events
		} else if mod.After(lastMod) {
			w.modTimes[path] = mod
			changed = append(changed, path)
		}

		return nil
	})

	return changed
}

// Watch runs the polling loop until the context is canceled, calling onFileChanged on updates.
func (w *FileWatcher) Watch(ctx context.Context, onFileChanged func(path string)) error {
	// Initialize baseline snapshot
	_ = w.ScanOnce()

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			changed := w.ScanOnce()
			for _, file := range changed {
				if onFileChanged != nil {
					onFileChanged(file)
				}
			}
		}
	}
}
