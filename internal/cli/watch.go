package cli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lleontor705/cortex/v2/internal/domain/ast"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	projectpkg "github.com/lleontor705/cortex/v2/internal/project"
)

func runWatch(args []string, stdout, stderr io.Writer) int {
	dir := "."
	project := ""

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

	if project == "" {
		project = projectpkg.DetectProject(absDir)
		if project == "" || project == "unknown" {
			project = "default"
		}
	}

	a, err := openApp()
	if err != nil {
		writef(stderr, "Error opening cortex app: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg := code.DefaultWatcherConfig(absDir, project)
	watcher := code.NewFileWatcher(cfg)
	extractor := ast.NewExtractor(absDir)

	writef(stdout, "👀 Cortex File Watcher activo en %s (proyecto: %s)\n", absDir, project)
	writef(stdout, "Presiona Ctrl+C para detener.\n\n")

	err = watcher.WatchWithEvents(ctx, func(path string) {
		rel, _ := filepath.Rel(absDir, path)
		if rel == "" {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		writef(stdout, "⚡ [Detectado cambio] %s -> indexando AST...\n", rel)

		if a.Stores.Code != nil {
			codeGraph, err := extractor.ExtractCodeGraph(path, project, 1)
			if err != nil {
				writef(stderr, "   ⚠️ error extrayendo AST de %s: %v\n", rel, err)
				return
			}
			// Clean up previous symbols and relationships for this file to prevent orphans
			_ = a.Stores.Code.DeleteSymbolsByFile(ctx, project, rel)
			_ = a.Stores.Code.DeleteRelationsByFile(ctx, project, rel)

			if len(codeGraph.Symbols) > 0 {
				if err := a.Stores.Code.SaveSymbols(ctx, codeGraph.Symbols); err != nil {
					writef(stderr, "   ⚠️ error guardando símbolos: %v\n", err)
					return
				}
			}
			if len(codeGraph.Relations) > 0 {
				if err := a.Stores.Code.SaveRelations(ctx, codeGraph.Relations); err != nil {
					writef(stderr, "   ⚠️ error guardando relaciones: %v\n", err)
					return
				}
			}
			writef(stdout, "   ✅ %d símbolos, %d relaciones actualizadas.\n", len(codeGraph.Symbols), len(codeGraph.Relations))
		}
	}, func(path string) {
		rel, _ := filepath.Rel(absDir, path)
		if rel == "" {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		writef(stdout, "🗑️ [Detectada eliminación] %s -> eliminando símbolos del grafo...\n", rel)
		if a.Stores.Code != nil {
			_ = a.Stores.Code.DeleteSymbolsByFile(ctx, project, rel)
			_ = a.Stores.Code.DeleteRelationsByFile(ctx, project, rel)
			writef(stdout, "   ✅ Símbolos y relaciones de %s eliminados.\n", rel)
		}
	})

	if err != nil && err != context.Canceled {
		writef(stderr, "Watcher finalizado con error: %v\n", err)
		return 1
	}

	writef(stdout, "\n🛑 File Watcher detenido.\n")
	return 0
}
