package contextpack

import (
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

func TestBuildPack_And_Renderers(t *testing.T) {
	allObs := []*domain.Observation{
		{
			ID:       1,
			Title:    "Go 1.26 Pinned",
			Type:     "pattern",
			TopicKey: "rules/toolchain",
			Content:  "Always use Go 1.26 and Zero-CGO",
		},
		{
			ID:      2,
			Title:   "SQLite v2 Baseline",
			Type:    "decision",
			Content: "Use single file migrations/v2/001_init.sql",
		},
		{
			ID:      3,
			Title:   "Windows Path Traversal",
			Type:    "bugfix",
			Content: "Ensure filepath.ToSlash is used for keys",
		},
		{
			ID:      4,
			Title:   "Unrelated tool use",
			Type:    "tool_use",
			Content: "Ran bash command",
		},
	}

	codeGraph := &code.CodeGraph{
		Project: "test-cortex",
		Symbols: []code.Symbol{
			{ID: "sym:1", Name: "HubService", Kind: "struct", FilePath: "internal/hub.go"},
			{ID: "sym:2", Name: "Client", Kind: "struct", FilePath: "internal/client.go"},
		},
		Relations: []code.Relation{
			{SourceID: "sym:2", TargetID: "sym:1", Relation: code.RelationCalls},
		},
	}

	opts := Options{
		Project:        "test-cortex",
		IncludeRules:   true,
		MaxDecisions:   5,
		MaxBugfixes:    5,
		IncludeRepoMap: true,
		RepoMapBudget:  500,
	}

	pack := BuildPack("test-cortex", allObs, codeGraph, opts)

	if len(pack.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(pack.Rules))
	}
	if len(pack.Decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(pack.Decisions))
	}
	if len(pack.Bugfixes) != 1 {
		t.Fatalf("expected 1 bugfix, got %d", len(pack.Bugfixes))
	}
	if pack.RepoMap == "" {
		t.Fatalf("expected non-empty repo map")
	}

	// 1. Test Markdown
	md, err := Render(pack, "markdown")
	if err != nil {
		t.Fatalf("render markdown failed: %v", err)
	}
	if !strings.Contains(md, "# Cortex Context — test-cortex") ||
		!strings.Contains(md, "Active Rules & Directives") ||
		!strings.Contains(md, "Architectural Decisions") {
		t.Errorf("markdown missing expected sections: %s", md)
	}

	// 2. Test XML
	xml, err := Render(pack, "xml")
	if err != nil {
		t.Fatalf("render xml failed: %v", err)
	}
	if !strings.Contains(xml, "<cortex-context project=\"test-cortex\">") ||
		!strings.Contains(xml, "<rules>") ||
		!strings.Contains(xml, "<architectural-decisions>") ||
		!strings.Contains(xml, "<gotchas>") ||
		!strings.Contains(xml, "</cortex-context>") {
		t.Errorf("xml missing expected tags: %s", xml)
	}

	// 3. Test JSON
	jsonStr, err := Render(pack, "json")
	if err != nil {
		t.Fatalf("render json failed: %v", err)
	}
	if !strings.Contains(jsonStr, "\"project\": \"test-cortex\"") {
		t.Errorf("json missing project: %s", jsonStr)
	}
}
