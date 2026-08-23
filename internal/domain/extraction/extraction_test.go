package extraction

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// ssrfTestProvider is an HTTPS provider on loopback approved through the
// sanctioned explicit local-only development exception (AllowLoopback). It
// counts hits and allows per-test handler overrides.
type ssrfTestProvider struct {
	server  *httptest.Server
	policy  OutboundPolicy
	hits    atomic.Int64
	mu      sync.Mutex
	handler http.HandlerFunc
}

func newSSRFTestProvider(t *testing.T, response string) *ssrfTestProvider {
	t.Helper()
	tp := &ssrfTestProvider{}
	tp.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tp.hits.Add(1)
		tp.mu.Lock()
		handler := tp.handler
		tp.mu.Unlock()
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	tp.server.StartTLS()
	t.Cleanup(tp.server.Close)

	u, err := url.Parse(tp.server.URL)
	if err != nil {
		t.Fatalf("parse provider url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse provider port: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(tp.server.Certificate())
	tp.policy = OutboundPolicy{
		AllowedHosts:  []string{u.Hostname()},
		AllowedPorts:  []int{443, port},
		AllowLoopback: true,
		MaxConcurrent: 2,
		MaxRedirects:  3,
		TLSConfig:     &tls.Config{RootCAs: pool},
	}
	return tp
}

func (tp *ssrfTestProvider) url() string { return tp.server.URL }

func (tp *ssrfTestProvider) hitCount() int64 { return tp.hits.Load() }

func (tp *ssrfTestProvider) setHandler(handler http.HandlerFunc) {
	tp.mu.Lock()
	tp.handler = handler
	tp.mu.Unlock()
}

func (tp *ssrfTestProvider) service(t *testing.T, cfg Config) *Service {
	t.Helper()
	if cfg.BaseURL == "" {
		cfg.BaseURL = tp.url()
	}
	return NewServiceWithPolicy(cfg, tp.policy)
}

const extractLLMResponse = `{"choices":[{"message":{"content":"{\"observations\":[{\"title\":\"llm decision\",\"content\":\"from approved provider\",\"type\":\"decision\",\"project\":\"cortex\",\"scope\":\"project\",\"confidence\":0.9,\"tags\":[]}],\"edges\":[],\"summary\":\"llm summary marker\"}"}}]}`

// TestSSRFApprovedProviderSucceeds pins the SEC-02 happy path: an explicitly
// approved HTTPS provider with an explicitly approved local-only development
// exception still produces LLM extraction and synthesis results.
func TestSSRFApprovedProviderSucceeds(t *testing.T) {
	tp := newSSRFTestProvider(t, extractLLMResponse)
	svc := tp.service(t, Config{
		Provider: ProviderGeneric,
		APIKey:   "sk-admin-approved",
		Model:    "test-model",
		Timeout:  10 * time.Second,
	})

	res, err := svc.Extract(context.Background(), ExtractionRequest{Text: "We decided to use an approved provider.", Project: "cortex"})
	if err != nil {
		t.Fatalf("approved provider extraction failed: %v", err)
	}
	if res.SourceMethod != "llm" {
		t.Fatalf("source_method = %q, want llm", res.SourceMethod)
	}
	if tp.hitCount() == 0 {
		t.Fatal("approved provider was never contacted")
	}

	syn, err := svc.Synthesize(context.Background(), SynthesisRequest{
		Project: "cortex",
		Observations: []*domain.Observation{
			{ID: 1, Title: "d", Content: "c", Type: "decision", Project: "cortex"},
		},
	})
	if err != nil {
		t.Fatalf("approved provider synthesis failed: %v", err)
	}
	if syn.Summary == "" {
		t.Fatal("synthesis summary missing")
	}
}

// TestSSRFServiceIgnoresRequestLevelLLMConfig pins SEC-02 defense-in-depth:
// even when a request carries llm_config credentials, the service uses only
// its server-side configuration; the request-controlled destination is never
// contacted.
func TestSSRFServiceIgnoresRequestLevelLLMConfig(t *testing.T) {
	var attackHits int64
	attack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&attackHits, 1)
		_, _ = w.Write([]byte(llmCanaryResponse))
	}))
	defer attack.Close()

	tp := newSSRFTestProvider(t, extractLLMResponse)
	svc := tp.service(t, Config{
		Provider: ProviderGeneric,
		APIKey:   "sk-admin-approved",
		Model:    "test-model",
		Timeout:  10 * time.Second,
	})

	res, err := svc.Extract(context.Background(), ExtractionRequest{
		Text:      "We decided to ignore request-level credentials.",
		Project:   "cortex",
		LLMConfig: &Config{Provider: ProviderGeneric, BaseURL: attack.URL, APIKey: "sk-request-canary", Model: "attacker"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SourceMethod != "llm" {
		t.Fatalf("source_method = %q, want llm from admin provider", res.SourceMethod)
	}
	if attackHits != 0 {
		t.Fatalf("request-controlled destination was contacted %d times", attackHits)
	}
}

// TestSSRFUnapprovedHostPortAndSchemeCorpus pins the allowlist layer: with an
// explicit policy approving api.example.test:443 only, every other host,
// port, scheme, or userinfo variant is rejected at the URL layer.
func TestSSRFUnapprovedHostPortAndSchemeCorpus(t *testing.T) {
	policy := OutboundPolicy{AllowedHosts: []string{"api.example.test"}, AllowedPorts: []int{443}}
	corpus := []string{
		"https://evil.example.test/v1",
		"https://api.example.test:8443/v1",
		"http://api.example.test/v1",
		"https://user@api.example.test/v1",
		"https://user:pass@api.example.test/v1",
		// Allowlist-prefix bypass: a longer name ending in the approved
		// host must not match.
		"https://api.example.test.attacker.test/v1",
	}
	for i, baseURL := range corpus {
		svc := NewServiceWithPolicy(Config{Provider: ProviderGeneric, BaseURL: baseURL, APIKey: "sk-admin", Timeout: 2 * time.Second}, policy)
		_, err := svc.Extract(context.Background(), ExtractionRequest{Text: "corpus", Project: "cortex"})
		if !errors.Is(err, ErrUnsafeDestination) {
			t.Errorf("corpus %d: err = %v, want ErrUnsafeDestination", i, err)
		}
	}
}

// TestSSRFRedirectRevalidation pins the redirect layer: an approved provider
// may still only redirect within policy (same origin), and every redirect
// target that changes host, port, scheme, or adds userinfo is rejected before
// the follow-up request — and before credentials are forwarded.
func TestSSRFRedirectRevalidation(t *testing.T) {
	tp := newSSRFTestProvider(t, extractLLMResponse)
	providerHost := ""
	if u, err := url.Parse(tp.url()); err != nil {
		t.Fatalf("parse provider url: %v", err)
	} else {
		providerHost = u.Hostname()
	}

	targets := []string{
		"https://evil.example.test/v1/chat/completions",
		"https://" + providerHost + ":8443/v1/chat/completions",
		"http://" + providerHost + "/v1/chat/completions",
		"https://user@" + providerHost + "/v1/chat/completions",
		"https://169.254.169.254/v1/chat/completions",
		"https://[fc00::9]/v1/chat/completions",
	}
	for i, target := range targets {
		tp.setHandler(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusFound)
		})
		hitsBefore := tp.hitCount()
		svc := tp.service(t, Config{Provider: ProviderGeneric, APIKey: "sk-admin", Model: "m", Timeout: 10 * time.Second})
		_, err := svc.Extract(context.Background(), ExtractionRequest{Text: "redirect corpus", Project: "cortex"})
		if !errors.Is(err, ErrUnsafeDestination) {
			t.Errorf("redirect target %d: err = %v, want ErrUnsafeDestination", i, err)
		}
		if got := tp.hitCount(); got != hitsBefore+1 {
			t.Errorf("redirect target %d: provider saw %d requests, want exactly the initial hop", i, got-hitsBefore)
		}
	}

	// Safe redirect: same-origin path redirect succeeds within hop limits.
	tp.setHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions/final" {
			http.Redirect(w, r, "/v1/chat/completions/final", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(extractLLMResponse))
	})
	hitsBefore := tp.hitCount()
	svc := tp.service(t, Config{Provider: ProviderGeneric, APIKey: "sk-admin", Model: "m", Timeout: 10 * time.Second})
	res, err := svc.Extract(context.Background(), ExtractionRequest{Text: "safe redirect", Project: "cortex"})
	if err != nil {
		t.Fatalf("same-origin redirect failed: %v", err)
	}
	if res.SourceMethod != "llm" {
		t.Fatalf("source_method = %q, want llm", res.SourceMethod)
	}
	if tp.hitCount() < hitsBefore+2 {
		t.Fatalf("expected at least the redirect hop and the final request, got %d", tp.hitCount()-hitsBefore)
	}
}

// TestSSRFRedirectHopLimitCapsRedirectChains pins the hop cap: a redirect
// loop is stopped after MaxRedirects hops with a policy rejection.
func TestSSRFRedirectHopLimitCapsRedirectChains(t *testing.T) {
	tp := newSSRFTestProvider(t, extractLLMResponse)
	tp.policy.MaxRedirects = 2
	tp.setHandler(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	svc := tp.service(t, Config{Provider: ProviderGeneric, APIKey: "sk-admin", Model: "m", Timeout: 10 * time.Second})

	_, err := svc.Extract(context.Background(), ExtractionRequest{Text: "hop limit", Project: "cortex"})
	if !errors.Is(err, ErrUnsafeDestination) {
		t.Fatalf("err = %v, want ErrUnsafeDestination", err)
	}
	if got := tp.hitCount(); got != 2 {
		t.Fatalf("provider saw %d requests, want exactly 2 (hop cap)", got)
	}
}

// TestSSRFDNSRebindDialValidation pins the dial layer: the policy resolves
// the host itself, filters every resolved address, and dials only an approved
// address — an approved name that rebinds to a denied address never opens a
// connection, and mixed resolutions dial only the approved IP.
func TestSSRFDNSRebindDialValidation(t *testing.T) {
	policy := OutboundPolicy{AllowedHosts: []string{"rebind.example.test"}, AllowedPorts: []int{443}}
	base := Config{Provider: ProviderGeneric, BaseURL: "https://rebind.example.test/v1", APIKey: "sk-admin", Timeout: 5 * time.Second}

	t.Run("rebind to metadata denied", func(t *testing.T) {
		var dials int64
		svc := NewServiceWithPolicy(base, policy)
		svc.policy.resolver = func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("169.254.169.254"), net.ParseIP("127.0.0.1")}, nil
		}
		svc.policy.dial = func(context.Context, string, string) (net.Conn, error) {
			atomic.AddInt64(&dials, 1)
			return nil, errors.New("no dial expected")
		}
		if _, err := svc.Extract(context.Background(), ExtractionRequest{Text: "rebind", Project: "cortex"}); !errors.Is(err, ErrUnsafeDestination) {
			t.Fatalf("err = %v, want ErrUnsafeDestination (an approved name that rebinds to denied addresses never dials)", err)
		}
		if dials != 0 {
			t.Fatalf("denied address was dialed %d times", dials)
		}
	})

	t.Run("all resolutions denied", func(t *testing.T) {
		svc := NewServiceWithPolicy(base, policy)
		svc.policy.resolver = func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("10.0.0.9"), net.ParseIP("127.0.0.1"), net.ParseIP("fd12::1")}, nil
		}
		if _, err := svc.Extract(context.Background(), ExtractionRequest{Text: "rebind", Project: "cortex"}); !errors.Is(err, ErrUnsafeDestination) {
			t.Fatalf("err = %v, want ErrUnsafeDestination", err)
		}
	})

	t.Run("mixed resolution dials only approved address", func(t *testing.T) {
		var dialed []string
		var mu sync.Mutex
		svc := NewServiceWithPolicy(base, policy)
		svc.policy.resolver = func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("10.0.0.9"), net.ParseIP("203.0.113.7")}, nil
		}
		svc.policy.dial = func(_ context.Context, _ string, address string) (net.Conn, error) {
			mu.Lock()
			dialed = append(dialed, address)
			mu.Unlock()
			return nil, errors.New("stop after dial decision")
		}
		_, err := svc.Extract(context.Background(), ExtractionRequest{Text: "rebind", Project: "cortex"})
		if errors.Is(err, ErrUnsafeDestination) {
			t.Fatal("approved address was not dialed")
		}
		mu.Lock()
		defer mu.Unlock()
		if len(dialed) != 1 || dialed[0] != "203.0.113.7:443" {
			t.Fatalf("dialed %v, want exactly [203.0.113.7:443]", dialed)
		}
	})
}

// TestSSRFErrorBodyBoundedAndSanitized pins the error-body bound: a non-2xx
// response body is drained only up to the configured bound and never appears
// in any error message.
func TestSSRFErrorBodyBoundedAndSanitized(t *testing.T) {
	const canaryMarker = "INTERNAL-ERROR-BODY-CANARY-"
	tp := newSSRFTestProvider(t, extractLLMResponse)
	tp.policy.MaxErrorBodyBytes = 1024
	tp.policy.MaxResponseBodyBytes = 4096
	tp.setHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(bytes.Repeat([]byte(canaryMarker), 4096)) // ~100 KiB error body
	})
	svc := tp.service(t, Config{Provider: ProviderGeneric, APIKey: "sk-admin", Model: "m", Timeout: 10 * time.Second})

	_, err := svc.extractWithLLM(context.Background(), ExtractionRequest{Text: "error body", Project: "cortex"}, svc.defaultCfg)
	if !errors.Is(err, ErrProviderRejected) {
		t.Fatalf("err = %v, want ErrProviderRejected", err)
	}
	if strings.Contains(err.Error(), canaryMarker) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error message leaks upstream detail: %s", err.Error())
	}
	if tp.hitCount() == 0 {
		t.Fatal("provider never saw the request")
	}
}

// TestSSRFOutboundConcurrencyBounded pins the concurrency bound: with
// MaxConcurrent=1 a second outbound request cannot reach the provider while
// the first is in flight.
func TestSSRFOutboundConcurrencyBounded(t *testing.T) {
	tp := newSSRFTestProvider(t, extractLLMResponse)
	tp.policy.MaxConcurrent = 1

	started := make(chan struct{})
	release := make(chan struct{})
	var inFlight int64
	tp.setHandler(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt64(&inFlight, 1)
		if n == 1 {
			close(started)
			<-release
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(extractLLMResponse))
	})
	svc := tp.service(t, Config{Provider: ProviderGeneric, APIKey: "sk-admin", Model: "m", Timeout: 10 * time.Second})

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := svc.Extract(context.Background(), ExtractionRequest{Text: "first", Project: "cortex"})
		results[0] = err
	}()
	<-started
	go func() {
		defer wg.Done()
		_, err := svc.Extract(context.Background(), ExtractionRequest{Text: "second", Project: "cortex"})
		results[1] = err
	}()

	// While the first request holds the only outbound slot, the second must
	// not reach the provider.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&inFlight) > 1 {
			t.Fatal("second outbound request reached the provider despite MaxConcurrent=1")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	wg.Wait()
	for i, err := range results {
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}
	if atomic.LoadInt64(&inFlight) != 2 {
		t.Fatalf("expected both requests to complete sequentially, inFlight=%d", inFlight)
	}
}

// llmCanaryResponse is a well-formed OpenAI-style chat completion that a
// hostile destination would return once it receives the server-side request.
const llmCanaryResponse = `{"choices":[{"message":{"content":"{\"observations\":[{\"title\":\"pwned\",\"content\":\"hostile destination reached\",\"type\":\"manual\",\"project\":\"p\",\"scope\":\"project\",\"confidence\":0.9,\"tags\":[\"canary\"]}],\"edges\":[],\"summary\":\"canary\"}"}}]}`

// TestSSRFDestinationCorpusRejectedBeforeRequest pins SEC-02 (UNIT-SSRF-CORPUS):
// a configured LLM destination that is plain HTTP, carries userinfo, uses a
// non-default/unexpected port, or points at loopback, private, link-local,
// metadata, multicast, unspecified, broadcast, CGNAT, or IPv4-mapped IPv6
// addresses MUST fail before any outbound request is issued — and therefore
// before any credential is transmitted. The attacker listener proves the
// vulnerable path would reach the destination; any corpus entry that leaks a
// request increments its hit counter.
func TestSSRFDestinationCorpusRejectedBeforeRequest(t *testing.T) {
	var hits int64
	attack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(llmCanaryResponse))
	}))
	defer attack.Close()

	hostPort := strings.TrimPrefix(attack.URL, "http://") // 127.0.0.1:<port>

	corpus := []string{
		// Reaches the attacker listener directly when unguarded.
		"http://" + hostPort + "/v1",
		"http://user:secret@" + hostPort + "/v1",
		// Userinfo and unexpected-port forms over TLS-shaped URLs.
		"https://user:secret@" + hostPort + "/v1",
		"https://" + hostPort + "/v1",
		// Metadata / link-local (IPv4 and IPv6 spellings).
		"https://169.254.169.254/v1",
		"https://[fe80::1]/v1",
		// Private ranges.
		"https://10.1.2.3/v1",
		"https://192.168.0.10/v1",
		"https://172.31.255.4/v1",
		"https://[fc00::12]/v1",
		// Loopback (native and IPv4-mapped) and multicast.
		"https://[::1]/v1",
		"https://[::ffff:127.0.0.1]/v1",
		"https://[ff02::1]/v1",
		// Unspecified, broadcast, and shared/CGNAT space.
		"https://0.0.0.0/v1",
		"https://255.255.255.255/v1",
		"https://100.64.0.7/v1",
	}

	for i, baseURL := range corpus {
		svc := NewService(Config{
			Provider: ProviderGeneric,
			BaseURL:  baseURL,
			APIKey:   "sk-outbound-canary",
			Model:    "test-model",
			Timeout:  1500 * time.Millisecond,
		})
		_, err := svc.Extract(context.Background(), ExtractionRequest{
			Text:    "We decided to validate SSRF containment before shipping.",
			Project: "cortex",
		})
		if err == nil {
			t.Errorf("corpus entry %d (%s): extraction succeeded; want policy rejection before any outbound request", i, corpusClass(baseURL))
		}
	}
	if hits != 0 {
		t.Fatalf("attacker destination received %d outbound requests; corpus must fail before transmission", hits)
	}
}

// TestSSRFRequestBodyBombBounded pins SEC-02: an oversized provider response
// (success or error body) must be detected and rejected at the configured
// bound instead of being fully buffered.
func TestSSRFRequestBodyBombBounded(t *testing.T) {
	bomb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"`))
		chunk := bytes.Repeat([]byte("A"), 64*1024)
		for i := 0; i < 160; i++ { // ~10 MiB total, well past any bound
			if _, err := w.Write(chunk); err != nil {
				return // client aborted the read at its bound
			}
		}
	}))
	defer bomb.Close()

	svc := NewService(Config{
		Provider: ProviderGeneric,
		BaseURL:  bomb.URL,
		APIKey:   "sk-outbound-canary",
		Model:    "test-model",
		Timeout:  10 * time.Second,
	})
	_, err := svc.Extract(context.Background(), ExtractionRequest{
		Text:    "We decided to validate bounded provider responses.",
		Project: "cortex",
	})
	if err == nil {
		t.Fatal("oversized provider response was buffered without a bounded rejection")
	}
}

// corpusClass classifies a corpus destination for failure messages without
// echoing the raw URL (which may embed userinfo or internal addresses).
func corpusClass(baseURL string) string {
	switch {
	case strings.Contains(baseURL, "@"):
		return "userinfo"
	case strings.HasPrefix(baseURL, "http://"):
		return "plain-http"
	case strings.Contains(baseURL, "169.254.") || strings.Contains(baseURL, "fe80"):
		return "link-local/metadata"
	case strings.HasPrefix(baseURL, "https://[ff"):
		return "multicast"
	case strings.Contains(baseURL, "127.0.0.1") || strings.Contains(baseURL, "[::1]") || strings.Contains(baseURL, "::ffff:"):
		return "loopback"
	case strings.Contains(baseURL, "0.0.0.0"):
		return "unspecified"
	case strings.Contains(baseURL, "255.255.255.255"):
		return "broadcast"
	case strings.Contains(baseURL, "100.64."):
		return "shared-cgnat"
	default:
		return "private"
	}
}

func TestExtractHeuristically_DecisionAndBugfix(t *testing.T) {
	svc := NewService(Config{Timeout: 5 * time.Second})

	input := `We decided to migrate the database from SQLite to PostgreSQL for high availability.
Fixed bug in the query planner where negative limits caused a panic.
Pattern: Always validate foreign keys before performing cascade deletion.`

	res, err := svc.Extract(context.Background(), ExtractionRequest{
		Text:    input,
		Project: "cortex-core",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.Observations) < 3 {
		t.Fatalf("expected at least 3 extracted observations, got %d", len(res.Observations))
	}

	if res.SourceMethod != "heuristic" {
		t.Errorf("expected source_method heuristic, got %s", res.SourceMethod)
	}

	if len(res.Edges) == 0 {
		t.Errorf("expected generated relation edges, got 0")
	}
}

func TestSynthesizeHeuristically(t *testing.T) {
	svc := NewService(Config{})

	obs := []*domain.Observation{
		{ID: 1, Title: "Chose PostgreSQL", Content: "Selected PostgreSQL for RLS support", Type: "decision", Project: "proj-1"},
		{ID: 2, Title: "Repository Pattern", Content: "Use repository interface boundaries", Type: "pattern", Project: "proj-1"},
		{ID: 3, Title: "Fix deadlock", Content: "Fixed transaction deadlock in batch inserts", Type: "bugfix", Project: "proj-1"},
	}

	res, err := svc.Synthesize(context.Background(), SynthesisRequest{
		Project:      "proj-1",
		Observations: obs,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.KeyDecisions) == 0 {
		t.Errorf("expected key decisions in synthesis result")
	}
	if len(res.Patterns) == 0 {
		t.Errorf("expected patterns in synthesis result")
	}
	if len(res.OpenIssues) == 0 {
		t.Errorf("expected open issues in synthesis result")
	}
}
