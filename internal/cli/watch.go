package cli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

func runWatch(args []string, stdout, stderr io.Writer) int {
	dir := "."
	project := "default"

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--project" && i+1 < len(args) {
			project = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--project=") {
			project = strings.TrimPrefix(arg, "--project=")
		} else if !strings.HasPrefix(arg, "-") {
			dir = arg
		}
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		writef(stderr, "Error resolving directory: %v\n", err)
		return 1
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg := code.DefaultWatcherConfig(absDir, project)
	watcher := code.NewFileWatcher(cfg)

	writef(stdout, "👀 Cortex File Watcher activo en %s (proyecto: %s)\n", absDir, project)
	writef(stdout, "Presiona Ctrl+C para detener.\n\n")

	err = watcher.Watch(ctx, func(path string) {
		rel, _ := filepath.Rel(absDir, path)
		if rel == "" {
			rel = path
		}
		writef(stdout, "⚡ [Detectado cambio] %s -> actualizando AST incremental...\n", rel)
	})

	if err != nil && err != context.Canceled {
		writef(stderr, "Watcher finalizado con error: %v\n", err)
		return 1
	}

	writef(stdout, "\n🛑 File Watcher detenido.\n")
	return 0
}
