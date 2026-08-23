package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	postgresstore "github.com/lleontor705/cortex/v2/internal/store/postgres"
)

func TestRuntimeDoesNotExposePostgresRepositories(t *testing.T) {
	typ := reflect.TypeOf(Runtime{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := field.Type.String()
		if strings.Contains(name, "postgres") || strings.Contains(name, "Repository") || field.Name == "Repositories" || field.Name == "Storage" {
			t.Fatalf("Runtime exposes storage implementation %s %s", field.Name, name)
		}
	}
}

func TestAuthorizedStoreHasNoRawRepositoryAccessors(t *testing.T) {
	typ := reflect.TypeOf((*postgresstore.AuthorizedStore)(nil))
	for _, name := range []string{
		"Observations", "Sessions", "Prompts", "Graph", "Entities", "Search", "Outbox", "Tokens",
		"GetObservation", "GetScore", "UpdateScore", "SetScore", "GetTop", "GetTopByScore",
		"GetAllScores", "RecordAccess", "GetIncomingEdgeCount", "BeginTx", "WithinTx", "Backend", "Health",
	} {
		if _, ok := typ.MethodByName(name); ok {
			t.Fatalf("AuthorizedStore exposes raw accessor %s", name)
		}
	}
}

// TestArchitectureMutationPins exercises the same AST rule against synthetic
// mutations. This prevents the architecture gate from becoming a passive
// assertion that only describes the current tree.
func TestArchitectureMutationPins(t *testing.T) {
	for name, src := range map[string]string{
		"raw accessor":             "package p; type AuthorizedStore struct{}; func (*AuthorizedStore) Observations() any { return nil }",
		"runtime repository field": "package p; type Repositories struct{}; type Runtime struct{ Repositories Repositories }",
	} {
		f, err := parser.ParseFile(token.NewFileSet(), name+".go", src, 0)
		if err != nil {
			t.Fatalf("parse mutation %s: %v", name, err)
		}
		found := false
		ast.Inspect(f, func(n ast.Node) bool {
			if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name == "Observations" {
				found = true
			}
			if ts, ok := n.(*ast.TypeSpec); ok && ts.Name.Name == "Runtime" {
				if st, ok := ts.Type.(*ast.StructType); ok {
					for _, field := range st.Fields.List {
						for _, id := range field.Names {
							if id.Name == "Repositories" {
								found = true
							}
						}
					}
				}
			}
			return true
		})
		if !found {
			t.Errorf("mutation detector missed %s", name)
		}
	}
	// Keep the real files present so a broken checkout cannot silently skip the gate.
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	for _, path := range []string{"internal/store/postgres/store.go", "internal/platform/server/server.go"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("architecture source missing: %s: %v", path, err)
		}
	}
}
