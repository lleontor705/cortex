package graph

import (
	"strings"
	"testing"
)

func TestGenerateCommunitySummaries(t *testing.T) {
	nodes := []GraphAnalyticsNode{
		{ID: "auth_service", Label: "AuthService"},
		{ID: "token_mgr", Label: "TokenManager"},
		{ID: "db_store", Label: "DBStore"},
	}

	edges := []GraphAnalyticsEdge{
		{Source: "auth_service", Target: "token_mgr", Weight: 1.0},
		{Source: "auth_service", Target: "db_store", Weight: 1.0},
	}

	communities := []Community{
		{
			ID:            1,
			Label:         "Auth & Security",
			HubNodeID:     "auth_service",
			Members:       []string{"auth_service", "token_mgr"},
			Size:          2,
			CohesionScore: 1.0,
		},
	}

	summaries := GenerateCommunitySummaries(communities, nodes, edges)
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}

	s := summaries[0]
	if s.CommunityID != 1 || s.HubNodeLabel != "AuthService" {
		t.Errorf("unexpected summary fields: %+v", s)
	}

	if !strings.Contains(s.SummaryMarkdown, "Auth & Security") {
		t.Errorf("expected title in markdown summary, got: %s", s.SummaryMarkdown)
	}

	if !strings.Contains(s.SummaryMarkdown, "DBStore") {
		t.Errorf("expected external dependency DBStore in summary, got: %s", s.SummaryMarkdown)
	}
}
