package server

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestRequestOperationsForwardsAdminAuthorizationFromContext(t *testing.T) {
	want := errors.New("deny")
	inner := newFakeOperations()
	inner.authorizeAdminErr = want
	err := (requestOperations{}).AuthorizeAdminManage(withOperations(context.Background(), inner))
	if !errors.Is(err, want) || inner.authorizeAdminCalls != 1 {
		t.Fatalf("AuthorizeAdminManage() error=%v calls=%d", err, inner.authorizeAdminCalls)
	}
}

func TestRequestOperationsAdminAuthorizationFailsWithoutContext(t *testing.T) {
	if err := (requestOperations{}).AuthorizeAdminManage(context.Background()); err == nil {
		t.Fatal("AuthorizeAdminManage() succeeded without authenticated operations")
	}
}

func TestRequestOperationsForwardsAgentProjectDiscovery(t *testing.T) {
	inner := newFakeOperations()
	inner.agentProjects = map[string]string{agentProjectID: "cortex"}
	projects, err := (requestOperations{}).ListAgentProjects(withOperations(context.Background(), inner))
	if err != nil || projects[agentProjectID] != "cortex" {
		t.Fatalf("ListAgentProjects() = %#v, %v", projects, err)
	}
	if _, err := (requestOperations{}).ListAgentProjects(context.Background()); err == nil {
		t.Fatal("ListAgentProjects() succeeded without authenticated operations")
	}
}

func TestRequestOperationsForwardsAgentSearchAuthorityAndLabel(t *testing.T) {
	inner := newFakeOperations()
	inner.agentSearchResults = []*domain.SearchResult{{Observation: domain.Observation{Title: "authorized"}}}
	results, err := (requestOperations{}).SearchAgentObservations(withOperations(context.Background(), inner), "project-id", "project-label", "query", domain.SearchOptions{Project: "project-label"})
	if err != nil || len(results) != 1 {
		t.Fatalf("SearchAgentObservations() = %#v, %v", results, err)
	}
	if inner.agentSearchProjectID != "project-id" || inner.agentSearchProjectLabel != "project-label" {
		t.Fatalf("forwarded authority=(%q,%q)", inner.agentSearchProjectID, inner.agentSearchProjectLabel)
	}
	if _, err := (requestOperations{}).SearchAgentObservations(context.Background(), "id", "label", "q", domain.SearchOptions{}); err == nil {
		t.Fatal("SearchAgentObservations() succeeded without authenticated operations")
	}
}
