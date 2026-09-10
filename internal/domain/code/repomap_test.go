package code

import (
	"strings"
	"testing"
)

func TestGenerateRepoMap(t *testing.T) {
	graph := &CodeGraph{
		Project: "cortex-test",
		Symbols: []Symbol{
			{ID: "sym:auth.AuthService", Name: "AuthService", Kind: KindStruct, FilePath: "internal/auth/service.go"},
			{ID: "sym:auth.NewService", Name: "NewService", Kind: KindFunc, Signature: "() *AuthService", FilePath: "internal/auth/service.go"},
			{ID: "sym:auth.TokenValidator", Name: "TokenValidator", Kind: KindInterface, FilePath: "internal/auth/validator.go"},
			{ID: "sym:db.Manager", Name: "Manager", Kind: KindStruct, FilePath: "internal/db/manager.go"},
		},
		Relations: []Relation{
			{SourceID: "sym:auth.NewService", TargetID: "sym:auth.AuthService", Relation: RelationCalls},
		},
	}

	repoMap := GenerateRepoMap(graph, 1000)

	if !strings.Contains(repoMap, "internal/auth/service.go:") {
		t.Errorf("expected service.go in repo map, got:\n%s", repoMap)
	}
	if !strings.Contains(repoMap, "type AuthService struct") {
		t.Errorf("expected type AuthService struct in repo map, got:\n%s", repoMap)
	}
	if !strings.Contains(repoMap, "type TokenValidator interface") {
		t.Errorf("expected type TokenValidator interface in repo map, got:\n%s", repoMap)
	}

	// Test budget truncation
	shortMap := GenerateRepoMap(graph, 15) // Extremely low budget (~60 chars)
	if !strings.Contains(shortMap, "+") && len(shortMap) > 150 {
		t.Errorf("expected budget truncation message in short map, got:\n%s", shortMap)
	}
}
