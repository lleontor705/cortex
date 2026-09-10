package code

import (
	"testing"
)

func TestSearchSymbols_Ranking(t *testing.T) {
	graph := &CodeGraph{
		Project: "test-proj",
		Symbols: []Symbol{
			{ID: "func:1", Name: "backup", Kind: KindFunc, FilePath: "a.go"},
			{ID: "func:2", Name: "backupDatabase", Kind: KindFunc, FilePath: "b.go"},
			{ID: "func:3", Name: "createBackupHelper", Kind: KindFunc, FilePath: "c.go"},
			{ID: "func:4", Name: "runCommand", Signature: "func runCommand(backup bool)", Kind: KindFunc, FilePath: "d.go"},
			{ID: "func:5", Name: "doWork", DocSummary: "Takes a backup snapshot", Kind: KindFunc, FilePath: "e.go"},
			{ID: "func:6", Name: "unrelated", Kind: KindFunc, FilePath: "f.go"},
		},
	}

	results := SearchSymbols(graph, SymbolSearchQuery{
		Query: "backup",
		Limit: 10,
	})

	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}

	// 1. Exact match
	if results[0].Symbol.Name != "backup" || results[0].MatchField != "name_exact" {
		t.Errorf("expected exact match first, got %+v", results[0])
	}
	// 2. Prefix match
	if results[1].Symbol.Name != "backupDatabase" || results[1].MatchField != "name_prefix" {
		t.Errorf("expected prefix match second, got %+v", results[1])
	}
	// 3. Contains match
	if results[2].Symbol.Name != "createBackupHelper" || results[2].MatchField != "name_contains" {
		t.Errorf("expected contains match third, got %+v", results[2])
	}
	// 4. Signature match
	if results[3].Symbol.Name != "runCommand" || results[3].MatchField != "signature" {
		t.Errorf("expected signature match fourth, got %+v", results[3])
	}
	// 5. Doc match
	if results[4].Symbol.Name != "doWork" || results[4].MatchField != "doc" {
		t.Errorf("expected doc match fifth, got %+v", results[4])
	}
}

func TestSearchSymbols_RegexAndKindFilter(t *testing.T) {
	graph := &CodeGraph{
		Project: "test-proj",
		Symbols: []Symbol{
			{ID: "struct:1", Name: "UserSession", Kind: KindStruct, FilePath: "session.go"},
			{ID: "func:1", Name: "CreateUserSession", Kind: KindFunc, FilePath: "session.go"},
			{ID: "func:2", Name: "DeleteUserSession", Kind: KindFunc, FilePath: "session.go"},
			{ID: "func:3", Name: "CreateApp", Kind: KindFunc, FilePath: "app.go"},
		},
	}

	// Regex search
	regexRes := SearchSymbols(graph, SymbolSearchQuery{
		Query:   "^Create.*",
		IsRegex: true,
	})
	if len(regexRes) != 2 {
		t.Fatalf("expected 2 regex results, got %d", len(regexRes))
	}

	// Filter by Kind
	kindRes := SearchSymbols(graph, SymbolSearchQuery{
		Query: "UserSession",
		Kind:  KindStruct,
	})
	if len(kindRes) != 1 || kindRes[0].Symbol.Kind != KindStruct {
		t.Fatalf("expected 1 struct result, got %+v", kindRes)
	}
}
