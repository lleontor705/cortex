package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/identity"
)

func TestAPITokenLifecycle(t *testing.T) {
	s := identity.NewMemoryTokenStore([]byte("test-key-material-that-is-long-enough"))
	issued, err := s.Issue(context.Background(), identity.TokenIssue{
		Subject: "svc-1", PrincipalType: "service_account", OrgID: "org-1",
		Workspaces: []string{"ws-1"}, Scopes: []string{"memory:read"},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Secret == "" || issued.Secret == issued.Record.Digest {
		t.Fatal("secret must be returned once and never stored plaintext")
	}
	got, err := s.Verify(context.Background(), issued.Secret, "memory:read")
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "svc-1" || got.OrgID != "org-1" {
		t.Fatalf("unexpected principal: %+v", got)
	}
	if _, err = s.Verify(context.Background(), issued.Secret, "memory:write"); err == nil {
		t.Fatal("out-of-scope token accepted")
	}
	if err = s.Revoke(context.Background(), issued.Record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(context.Background(), issued.Secret, "memory:read"); err == nil {
		t.Fatal("revoked token accepted")
	}
}

func TestIssuedTokenRecordCannotMutateStoreGrants(t *testing.T) {
	s := identity.NewMemoryTokenStore([]byte("test-key"))
	issued, err := s.Issue(context.Background(), identity.TokenIssue{Subject: "u", Workspaces: []string{"w"}, Scopes: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	issued.Record.Scopes[0] = "admin"
	issued.Record.Workspaces[0] = "other"
	p, err := s.Verify(context.Background(), issued.Secret, "read")
	if err != nil || p.Scopes[0] != "read" || p.WorkspaceIDs[0] != "w" {
		t.Fatalf("grant mutation escalated token: %+v, %v", p, err)
	}
}

func TestAPITokenRotationKeepsPreviousUntilExpiry(t *testing.T) {
	s := identity.NewMemoryTokenStore([]byte("test-key-material-that-is-long-enough"))
	old, err := s.Issue(context.Background(), identity.TokenIssue{Subject: "svc", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	newToken, err := s.Rotate(context.Background(), old.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(context.Background(), old.Secret, ""); err != nil {
		t.Fatalf("old token should remain valid: %v", err)
	}
	if _, err = s.Verify(context.Background(), newToken.Secret, ""); err != nil {
		t.Fatalf("new token invalid: %v", err)
	}
}

func TestAPITokenExpiryAndMalformed(t *testing.T) {
	s := identity.NewMemoryTokenStore([]byte("test-key-material-that-is-long-enough"))
	issued, err := s.Issue(context.Background(), identity.TokenIssue{Subject: "svc", ExpiresAt: time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(context.Background(), issued.Secret, ""); err != identity.ErrTokenExpired {
		t.Fatalf("err=%v, want expiry", err)
	}
	if _, err = s.Verify(context.Background(), "ctx_unknown", ""); err != identity.ErrInvalidToken {
		t.Fatalf("err=%v, want invalid", err)
	}
}
