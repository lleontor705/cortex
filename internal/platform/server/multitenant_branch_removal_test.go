package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

// TestRequestPlaneUsesConstantSingleTenantScope pins REQ-SH-002's single-tenant
// behavior now that the legacy MultiTenant flag is gone from configuration: a
// request without a workspace context must fall back to the configured default
// workspace instead of being rejected. Re-introducing any per-request
// multi-tenant gate in the request plane turns this test red.
func TestRequestPlaneUsesConstantSingleTenantScope(t *testing.T) {
	ops := newFakeOperations()
	cfg := config.Config{
		HTTP:   config.HTTPConfig{Token: "constant-bearer"},
		Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20},
	}
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, _, _ string) (domain.Principal, error) {
			return domain.Principal{Subject: "actor", OrgID: "tenant"}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) { return ops, nil }),
	}
	h, _ := newHTTPHandlerWithHybridSearch(cfg, requestOperations{}, func(context.Context) error { return nil }, auth.middleware, hybridSearchDependencies{})

	w, r := authed(http.MethodGet, "/api/search/hybrid?q=test", "")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("hybrid search status=%d body=%s; the request plane must not gate on removed multi-tenancy", w.Code, w.Body.String())
	}
}

// TestServerConfigHasNoMultiTenantField keeps config.ServerConfig
// unconditionally single-tenant: re-adding the removed field turns this red.
func TestServerConfigHasNoMultiTenantField(t *testing.T) {
	if _, ok := reflect.TypeOf(config.ServerConfig{}).FieldByName("MultiTenant"); ok {
		t.Fatal("config.ServerConfig.MultiTenant was re-introduced; the server composition must stay single-tenant")
	}
}

// TestStaticBindProvenanceMatchesMigrationStaticContract is the cross-layer
// mutation guard for the synthetic principal's binder proof. It reproduces the
// vector pinned by internal/store/postgres/store_bind_test.go, proving the
// composition derives exactly the `static:<hex>` argument migration 112
// recomputes from the actor's persisted grant digest; perturbing either the
// key or any coordinate of the message turns it red.
func TestStaticBindProvenanceMatchesMigrationStaticContract(t *testing.T) {
	const (
		tenant = "11111111-1111-1111-1111-111111111111"
		actor  = "22222222-2222-2222-2222-222222222222"
		digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		want   = "static:c01aab2f8dbf28526ae38f180710205c82f46c07db9ef11912c1a914943b5d03"
	)
	got, err := staticBindProvenance(tenant, actor, digest, 1)
	if err != nil {
		t.Fatalf("staticBindProvenance() = %v", err)
	}
	if got != want {
		t.Fatalf("static provenance = %q, want %q", got, want)
	}

	variants := map[string]func() (string, error){
		"tenant": func() (string, error) {
			return staticBindProvenance("33333333-3333-3333-3333-333333333333", actor, digest, 1)
		},
		"actor": func() (string, error) {
			return staticBindProvenance(tenant, "44444444-4444-4444-4444-444444444444", digest, 1)
		},
		"version": func() (string, error) { return staticBindProvenance(tenant, actor, digest, 2) },
		"digest":  func() (string, error) { return staticBindProvenance(tenant, actor, strings.Repeat("b", 64), 1) },
	}
	for name, variant := range variants {
		value, err := variant()
		if err != nil {
			t.Fatalf("%s variant = %v", name, err)
		}
		if value == got {
			t.Errorf("%s variant reused the baseline proof; the derivation must bind every coordinate", name)
		}
	}
}

func TestStaticBindProvenanceFailsClosedWithoutActorCoordinates(t *testing.T) {
	const (
		tenant = "11111111-1111-1111-1111-111111111111"
		actor  = "22222222-2222-2222-2222-222222222222"
		digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	for name, call := range map[string]func() (string, error){
		"empty digest":    func() (string, error) { return staticBindProvenance(tenant, actor, "", 1) },
		"missing version": func() (string, error) { return staticBindProvenance(tenant, actor, digest, 0) },
		"invalid tenant":  func() (string, error) { return staticBindProvenance("not-a-uuid", actor, digest, 1) },
		"invalid actor":   func() (string, error) { return staticBindProvenance(tenant, "not-a-uuid", digest, 1) },
	} {
		if _, err := call(); err == nil {
			t.Errorf("%s was accepted; the proof must fail closed", name)
		}
	}
}

// TestWorkspaceSelectorPinnedToDefaultRejectsForeignHeader documents the
// workspaceSelector contract the composition pins to the constant false: a
// request header can never redirect scope, even when the principal holds the
// foreign workspace grant.
func TestWorkspaceSelectorPinnedToDefaultRejectsForeignHeader(t *testing.T) {
	const (
		defaultWorkspace = "10000000-a000-0000-0000-000000000001"
		foreignWorkspace = "10000000-a000-0000-0000-000000000002"
	)
	selector := workspaceSelector{defaultWorkspace: defaultWorkspace}
	principal := domain.Principal{Subject: "actor", OrgID: "tenant", WorkspaceIDs: []string{defaultWorkspace, foreignWorkspace}}

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	if got, err := selector.selectWorkspace(req, principal); err != nil || got != defaultWorkspace {
		t.Fatalf("default selection = %q/%v, want %q", got, err, defaultWorkspace)
	}
	req.Header.Set(workspaceRequestHeader, foreignWorkspace)
	if _, err := selector.selectWorkspace(req, principal); !errors.Is(err, errWorkspaceNotGranted) {
		t.Fatalf("foreign header error = %v, want errWorkspaceNotGranted", err)
	}
}
