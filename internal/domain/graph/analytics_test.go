package graph

import (
	"testing"
)

func TestGraphAnalytics(t *testing.T) {
	nodes := []GraphAnalyticsNode{
		{ID: "func:auth.Login", Label: "Login", Kind: "func", SourceFile: "auth/login.go"},
		{ID: "func:auth.ValidateToken", Label: "ValidateToken", Kind: "func", SourceFile: "auth/jwt.go"},
		{ID: "type:domain.User", Label: "User", Kind: "struct", SourceFile: "domain/user.go"},
		{ID: "func:handler.HandleLogin", Label: "HandleLogin", Kind: "func", SourceFile: "handler/login.go"},
		{ID: "func:handler.HandleProfile", Label: "HandleProfile", Kind: "func", SourceFile: "handler/profile.go"},
	}

	edges := []GraphAnalyticsEdge{
		{ID: "e1", Source: "func:handler.HandleLogin", Target: "func:auth.Login", Type: "calls"},
		{ID: "e2", Source: "func:handler.HandleProfile", Target: "func:auth.ValidateToken", Type: "calls"},
		{ID: "e3", Source: "func:auth.Login", Target: "type:domain.User", Type: "uses"},
		{ID: "e4", Source: "func:auth.ValidateToken", Target: "type:domain.User", Type: "uses"},
	}

	report := AnalyzeGraph(nodes, edges)

	if report.TotalNodes != 5 {
		t.Errorf("expected 5 nodes, got %d", report.TotalNodes)
	}
	if report.TotalEdges != 4 {
		t.Errorf("expected 4 edges, got %d", report.TotalEdges)
	}
	if len(report.Communities) == 0 {
		t.Errorf("expected at least 1 community")
	}
	if len(report.GodNodes) == 0 {
		t.Errorf("expected at least 1 god node")
	}

	// Test Blast Radius
	blast := CalculateBlastRadius("type:domain.User", nodes, edges, 3)
	if len(blast.TotalImpacted) == 0 {
		t.Errorf("expected User struct change to impact dependent functions")
	}
	if len(blast.ImpactedFiles) == 0 {
		t.Errorf("expected impacted files list to not be empty")
	}
}

func TestFindCycles(t *testing.T) {
	nodes := []GraphAnalyticsNode{
		{ID: "mod:A", Label: "A"},
		{ID: "mod:B", Label: "B"},
		{ID: "mod:C", Label: "C"},
	}
	edges := []GraphAnalyticsEdge{
		{ID: "e1", Source: "mod:A", Target: "mod:B", Type: "imports"},
		{ID: "e2", Source: "mod:B", Target: "mod:C", Type: "imports"},
		{ID: "e3", Source: "mod:C", Target: "mod:A", Type: "imports"},
	}

	cycles := FindCycles(nodes, edges)
	if len(cycles) == 0 {
		t.Errorf("expected circular dependency A->B->C->A to be detected")
	}
}
