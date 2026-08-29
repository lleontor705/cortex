package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewSecureRejectsUnapprovedPrivateDestination(t *testing.T) {
	policy := OutboundPolicy{}
	if err := policy.ApproveDestination("https://169.254.169.254"); err != nil {
		t.Fatal(err)
	}
	_, err := NewSecure(Config{Provider: "ollama", BaseURL: "https://169.254.169.254"}, policy)
	if err == nil {
		t.Fatal("accepted metadata endpoint")
	}
}

func TestSecureEmbeddingRejectsRedirectAndOversizedBody(t *testing.T) {
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer redirectTarget.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer redirector.Close()
	policy := OutboundPolicy{AllowLoopback: true, AllowInsecureLoopbackHTTP: true, MaxResponseBodyBytes: 64}
	if err := policy.ApproveDestination(redirector.URL); err != nil {
		t.Fatal(err)
	}
	svc, err := NewSecure(Config{Provider: "ollama", BaseURL: redirector.URL}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Embed(context.Background(), "x"); err == nil {
		t.Fatal("cross-origin redirect succeeded")
	}

	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[` + strings.Repeat("1,", 100) + `1]]}`))
	}))
	defer large.Close()
	policy = OutboundPolicy{AllowLoopback: true, AllowInsecureLoopbackHTTP: true, MaxResponseBodyBytes: 64}
	if err := policy.ApproveDestination(large.URL); err != nil {
		t.Fatal(err)
	}
	svc, err = NewSecure(Config{Provider: "ollama", BaseURL: large.URL}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Embed(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "response exceeded") {
		t.Fatalf("oversized response error=%v", err)
	}
}
