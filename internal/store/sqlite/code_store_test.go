package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain/code"
	_ "modernc.org/sqlite"
)

func newTestCodeStore(t *testing.T) *CodeStore {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	store, err := NewCodeStore(db)
	if err != nil {
		t.Fatalf("failed to create code store: %v", err)
	}
	return store
}

func TestCodeStore_SaveAndList(t *testing.T) {
	store := newTestCodeStore(t)
	ctx := context.Background()

	symbols := []code.Symbol{
		{
			ID:          "sym:func:run",
			Project:     "test-project",
			FilePath:    "cmd/main.go",
			LineNumber:  10,
			Kind:        code.KindFunc,
			Name:        "Run",
			PackageName: "main",
			Signature:   "func Run() error",
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		},
		{
			ID:          "sym:struct:config",
			Project:     "test-project",
			FilePath:    "internal/config/config.go",
			LineNumber:  25,
			Kind:        code.KindStruct,
			Name:        "Config",
			PackageName: "config",
			Signature:   "type Config struct",
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		},
	}

	if err := store.SaveSymbols(ctx, symbols); err != nil {
		t.Fatalf("SaveSymbols failed: %v", err)
	}

	list, err := store.ListSymbols(ctx, code.SymbolFilter{Project: "test-project"})
	if err != nil {
		t.Fatalf("ListSymbols failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(list))
	}

	sym, err := store.GetSymbolByID(ctx, "sym:func:run")
	if err != nil || sym == nil {
		t.Fatalf("GetSymbolByID failed: %v", err)
	}
	if sym.Name != "Run" {
		t.Errorf("expected name Run, got %s", sym.Name)
	}

	relations := []code.Relation{
		{
			Project:    "test-project",
			SourceID:   "sym:func:run",
			TargetID:   "sym:struct:config",
			Relation:   code.RelationUses,
			Confidence: code.ConfidenceExtracted,
		},
	}
	if err := store.SaveRelations(ctx, relations); err != nil {
		t.Fatalf("SaveRelations failed: %v", err)
	}

	graph, err := store.GetGraph(ctx, "test-project")
	if err != nil {
		t.Fatalf("GetGraph failed: %v", err)
	}
	if len(graph.Symbols) != 2 || len(graph.Relations) != 1 {
		t.Errorf("unexpected graph size: %d symbols, %d relations", len(graph.Symbols), len(graph.Relations))
	}
}

func TestCodeStore_ListSymbolsQueryMatchesFilePath(t *testing.T) {
	store := newTestCodeStore(t)
	ctx := context.Background()

	err := store.SaveSymbols(ctx, []code.Symbol{{
		ID:         "module:SEE_ITC.DA/Conexion.DA/ApplicationDbContext.cs",
		Project:    "ITC.FacturadorWebPos",
		FilePath:   "SEE_ITC.DA/Conexion.DA/ApplicationDbContext.cs",
		LineNumber: 1,
		Kind:       code.KindModule,
		Name:       "ApplicationDbContext.cs",
	}})
	if err != nil {
		t.Fatalf("SaveSymbols failed: %v", err)
	}

	list, err := store.ListSymbols(ctx, code.SymbolFilter{
		Project: "ITC.FacturadorWebPos",
		Query:   "Conexion.DA/ApplicationDbContext.cs",
	})
	if err != nil {
		t.Fatalf("ListSymbols failed: %v", err)
	}
	if len(list) != 1 || list[0].FilePath != "SEE_ITC.DA/Conexion.DA/ApplicationDbContext.cs" {
		t.Fatalf("file-path query returned %#v", list)
	}
}
