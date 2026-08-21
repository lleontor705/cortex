package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/domain/extraction"
)

// newSSRFServerHandler mirrors the verified production wiring with an
// explicitly injected server-composed extraction service (SEC-02).
func newSSRFServerHandler(t *testing.T, extractor *extraction.Service) http.Handler {
	t.Helper()
	cfg := config.Config{HTTP: config.HTTPConfig{Token: "test-token"}}
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, _ string) (domain.Principal, error) {
			if secret != cfg.HTTP.Token {
				return domain.Principal{}, errors.New("unknown credential")
			}
			return domain.Principal{Subject: "00000000-0000-0000-0000-0000000000f1", OrgID: "00000000-0000-0000-0000-000000000001"}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) {
			return newFakeOperations(), nil
		}),
	}
	h, _ := newHTTPHandlerWithAuth(cfg, requestOperations{}, func(context.Context) error { return nil }, auth.middleware, extractor)
	return h
}

// TestServerSSRFRejectsRequestControlledLLMConfig pins SEC-02
// (HTTP-SSRF-REDACTION): the server extract/synthesize endpoints must never
// let a request body select the outbound destination or credential. A
// request-supplied llm_config with base_url or api_key is rejected with a
// stable invalid_configuration error before any outbound attempt, and the
// response must not echo the destination URL or credential canaries.
func TestServerSSRFRejectsRequestControlledLLMConfig(t *testing.T) {
	var hits int64
	attack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"observations\":[{\"title\":\"pwned\",\"content\":\"attacker controlled destination\",\"type\":\"manual\",\"project\":\"p\",\"scope\":\"project\",\"confidence\":1,\"tags\":[]}],\"edges\":[],\"summary\":\"canary\"}"}}]}`))
	}))
	defer attack.Close()

	h, _ := newVerifiedHTTPHandler(
		config.Config{HTTP: config.HTTPConfig{Token: "test-token"}},
		newFakeOperations(),
		func(context.Context) error { return nil },
	)

	extractBody := fmt.Sprintf(
		`{"text":"We decided to prove request-controlled destinations are rejected.","project":"cortex","llm_config":{"provider":"generic","base_url":%q,"api_key":"sk-request-canary","model":"test"}}`,
		attack.URL,
	)
	synthesizeBody := fmt.Sprintf(
		`{"project":"cortex","observations":[{"id":1,"title":"decision","content":"contain ssrf","type":"decision","project":"cortex"}],"llm_config":{"provider":"generic","base_url":%q,"api_key":"sk-request-canary","model":"test"}}`,
		attack.URL,
	)

	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{name: "extract", path: "/api/extract", body: extractBody},
		{name: "synthesize", path: "/api/synthesize", body: synthesizeBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hitsBefore := hits
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer test-token")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			body := rec.Body.String()
			if !strings.Contains(body, `"code":"invalid_configuration"`) {
				t.Fatalf("response missing stable invalid_configuration code: %s", body)
			}
			for _, canary := range []string{attack.URL, "sk-request-canary", "Bearer"} {
				if strings.Contains(body, canary) {
					t.Fatalf("response leaks canary %q: %s", canary, body)
				}
			}
			if hits != hitsBefore {
				t.Fatalf("attacker destination was contacted by a request-controlled llm_config")
			}
		})
	}
}

const serverExtractLLMResponse = `{"choices":[{"message":{"content":"{\"observations\":[{\"title\":\"server llm decision\",\"content\":\"from injected approved provider\",\"type\":\"decision\",\"project\":\"cortex\",\"scope\":\"project\",\"confidence\":0.9,\"tags\":[]}],\"edges\":[],\"summary\":\"server llm summary marker\"}"}}]}`

// TestServerSSRFApprovedProviderSucceeds pins the SEC-02 happy path at the
// HTTP boundary: with a server-composed (administrator-configured) extraction
// service whose policy approves the provider destination, plain extract and
// synthesize requests succeed through the LLM path.
func TestServerSSRFApprovedProviderSucceeds(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(serverExtractLLMResponse))
	}))
	defer provider.Close()

	u, err := url.Parse(provider.URL)
	if err != nil {
		t.Fatalf("parse provider url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse provider port: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(provider.Certificate())
	policy := extraction.OutboundPolicy{
		AllowedHosts:  []string{u.Hostname()},
		AllowedPorts:  []int{443, port},
		AllowLoopback: true, // explicit local-only development exception for the test provider
		TLSConfig:     &tls.Config{RootCAs: pool},
	}
	extractor := extraction.NewServiceWithPolicy(extraction.Config{
		Provider: extraction.ProviderGeneric,
		BaseURL:  provider.URL,
		APIKey:   "sk-admin-approved",
		Model:    "test-model",
		Timeout:  10 * time.Second,
	}, policy)
	h := newSSRFServerHandler(t, extractor)

	req := httptest.NewRequest(http.MethodPost, "/api/extract", strings.NewReader(`{"text":"We decided to verify the approved provider path.","project":"cortex"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("extract status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"source_method":"llm"`) {
		t.Fatalf("extract did not use the llm path: %s", rec.Body.String())
	}

	synReq := httptest.NewRequest(http.MethodPost, "/api/synthesize", strings.NewReader(`{"project":"cortex","observations":[{"id":1,"title":"d","content":"c","type":"decision","project":"cortex"}]}`))
	synReq.Header.Set("Authorization", "Bearer test-token")
	synReq.Header.Set("Content-Type", "application/json")
	synRec := httptest.NewRecorder()
	h.ServeHTTP(synRec, synReq)
	if synRec.Code != http.StatusOK {
		t.Fatalf("synthesize status = %d: %s", synRec.Code, synRec.Body.String())
	}
}

// TestServerSSRFDefaultWiringRemainsHeuristic pins production compatibility:
// without an injected provider, extract/synthesize keep working through the
// deterministic heuristic path (no outbound destination is approved).
func TestServerSSRFDefaultWiringRemainsHeuristic(t *testing.T) {
	h, _ := newVerifiedHTTPHandler(
		config.Config{HTTP: config.HTTPConfig{Token: "test-token"}},
		newFakeOperations(),
		func(context.Context) error { return nil },
	)
	req := httptest.NewRequest(http.MethodPost, "/api/extract", strings.NewReader(`{"text":"We decided to keep heuristic extraction working.","project":"cortex"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"source_method":"heuristic"`) {
		t.Fatalf("default wiring is not heuristic: %s", rec.Body.String())
	}
}

// TestServerSSRFUnsafeAdminDestinationRedacted pins the error contract: when
// the administrator-configured destination itself violates policy, the public
// error is a stable provider_unavailable classification with no destination
// detail.
func TestServerSSRFUnsafeAdminDestinationRedacted(t *testing.T) {
	extractor := extraction.NewServiceWithPolicy(extraction.Config{
		Provider: extraction.ProviderGeneric,
		BaseURL:  "https://10.99.0.5/v1",
		APIKey:   "sk-admin",
		Timeout:  2 * time.Second,
	}, extraction.DefaultOutboundPolicy())
	h := newSSRFServerHandler(t, extractor)

	req := httptest.NewRequest(http.MethodPost, "/api/extract", strings.NewReader(`{"text":"We decided to verify redacted admin misconfiguration.","project":"cortex"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"provider_unavailable"`) {
		t.Fatalf("missing stable provider_unavailable code: %s", body)
	}
	for _, canary := range []string{"10.99.0.5", "sk-admin", "https://"} {
		if strings.Contains(body, canary) {
			t.Fatalf("public error leaks destination canary %q: %s", canary, body)
		}
	}
}
