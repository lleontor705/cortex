package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/embedding"
	"github.com/lleontor705/cortex/v2/internal/ratelimit"
)

// Presets accepted by search.rerank_provider. The set is shared by config
// validation and both factories so a value that loads can always construct.
const (
	rerankProviderNone             = "none"
	rerankProviderLateInteraction  = "late-interaction"
	rerankProviderOpenAICompatible = "openai-compatible"
)

const (
	// Provider budgets: nan.builders qwen3-Reranker-8B advertises RPM 60 and
	// batch 32 (REQ-RNK-001); both are enforced client-side, not assumed.
	rerankMaxBatchDocs   = 32
	rerankRequestsPerMin = 60

	rerankMaxRetries429  = 3
	rerankMaxRedirects   = 3
	rerankMaxResponseLen = 4 << 20
)

// Reranker orders one fused candidate list for a query before the final
// limit is applied (REQ-RNK-001). Implementations preserve the input set
// exactly — the same *domain.SearchResult pointers with the same
// multiplicity — and order by descending score with ties broken by descending
// observation ID (the RRF convention in fuseInputs). A non-nil error means
// the returned slice must be discarded wholesale: partially reranked output
// is never valid.
type Reranker interface {
	Rerank(query string, docs []*domain.SearchResult) ([]*domain.SearchResult, error)
}

// RerankConfig is the resolved rerank preset handed to the factories. It has
// no API-key field by design: the key resolves only from CORTEX_RERANK_API_KEY
// at construction (REQ-CFG-001), so it can never be serialized into a config
// file or passed in from an untrusted source.
type RerankConfig struct {
	Provider string
	Model    string
	BaseURL  string
	// Budget is the shared provider rate budget drawn by every subsystem
	// dialing the same provider base URL (rerank, embedding, agent chat).
	// When nil the reranker builds its own private budget at the provider's
	// advertised rpm, which is the historical per-subsystem behavior.
	Budget *ratelimit.Limiter
}

// DefaultLateInteractionReranker is the zero-network local default. It
// delegates to ReRankWithLateInteraction so the revived 60/40 rank/MaxSim
// blend keeps exactly one source of ordering semantics.
type DefaultLateInteractionReranker struct{}

// Rerank reorders docs with the late-interaction blend and never fails.
func (DefaultLateInteractionReranker) Rerank(query string, docs []*domain.SearchResult) ([]*domain.SearchResult, error) {
	return ReRankWithLateInteraction(query, docs), nil
}

// NewReranker builds the local-mode reranker. The disabled default returns
// (nil, nil) so the local composition constructs zero machinery, and the
// remote preset is refused fail-closed: only NewSecureReranker may ever
// build the HTTP reranker.
func NewReranker(cfg RerankConfig) (Reranker, error) {
	switch normalizeRerankProvider(cfg.Provider) {
	case rerankProviderNone:
		return nil, nil
	case rerankProviderLateInteraction:
		return DefaultLateInteractionReranker{}, nil
	case rerankProviderOpenAICompatible:
		return nil, errors.New("rerank: openai-compatible is server-mode only; construct via NewSecureReranker")
	default:
		return nil, fmt.Errorf("rerank: unsupported provider %q", cfg.Provider)
	}
}

// NewSecureReranker is the sole construction path for the HTTP reranker: the
// base URL must be approved on the shared embedding OutboundPolicy, and the
// API key resolves only from CORTEX_RERANK_API_KEY (missing => construction
// error). Local composition never reaches this dial.
func NewSecureReranker(cfg RerankConfig, policy embedding.OutboundPolicy) (Reranker, error) {
	switch normalizeRerankProvider(cfg.Provider) {
	case rerankProviderNone:
		return nil, nil
	case rerankProviderLateInteraction:
		return DefaultLateInteractionReranker{}, nil
	case rerankProviderOpenAICompatible:
	default:
		return nil, fmt.Errorf("rerank: unsupported provider %q", cfg.Provider)
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, errors.New("rerank: openai-compatible requires an explicit base URL")
	}
	if err := approveRerankURL(policy, base); err != nil {
		return nil, err
	}
	key := config.ResolveRerankAPIKey()
	if key == "" {
		return nil, errors.New("rerank: CORTEX_RERANK_API_KEY is required for openai-compatible")
	}
	if policy.Timeout <= 0 {
		policy.Timeout = 30 * time.Second
	}
	client := &http.Client{
		Timeout: policy.Timeout,
		Transport: &http.Transport{
			DialContext:         rerankDialContext(policy),
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     90 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > rerankMaxRedirects {
				return errors.New("rerank: redirect limit exceeded")
			}
			return approveRerankURL(policy, req.URL.String())
		},
	}
	budget := cfg.Budget
	if budget == nil {
		budget = ratelimit.New(rerankRequestsPerMin)
	}
	return &httpReranker{
		model:   strings.TrimSpace(cfg.Model),
		baseURL: base,
		apiKey:  key,
		client:  client,
		budget:  budget,
		sleep:   time.Sleep,
	}, nil
}

var (
	_ Reranker = DefaultLateInteractionReranker{}
	_ Reranker = (*httpReranker)(nil)
)

// rerankScored pairs an input pointer with the provider relevance score for
// the final deterministic ordering pass.
type rerankScored struct {
	doc   *domain.SearchResult
	score float64
}

type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

type rerankResponse struct {
	Results []rerankResult `json:"results"`
}

type rerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// httpReranker speaks the OpenAI-compatible /v1/rerank contract. It is only
// reachable through NewSecureReranker, which pins the approved transport.
type httpReranker struct {
	model   string
	baseURL string
	apiKey  string
	client  *http.Client
	budget  *ratelimit.Limiter
	sleep   func(time.Duration)
}

// Rerank reranks docs in batches of at most 32, spacing requests to the RPM
// budget and backing off on 429. Any batch failure discards the whole run.
func (h *httpReranker) Rerank(query string, docs []*domain.SearchResult) ([]*domain.SearchResult, error) {
	if len(docs) == 0 {
		return docs, nil
	}
	scored := make([]rerankScored, 0, len(docs))
	for start := 0; start < len(docs); start += rerankMaxBatchDocs {
		end := min(start+rerankMaxBatchDocs, len(docs))
		batch, err := h.rerankBatch(query, docs[start:end])
		if err != nil {
			return nil, err
		}
		scored = append(scored, batch...)
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].doc.ID > scored[j].doc.ID
	})
	out := make([]*domain.SearchResult, len(scored))
	for i, s := range scored {
		s.doc.Rank = s.score
		out[i] = s.doc
	}
	return out, nil
}

func (h *httpReranker) rerankBatch(query string, batch []*domain.SearchResult) ([]rerankScored, error) {
	documents := make([]string, len(batch))
	for i, d := range batch {
		documents[i] = strings.TrimSpace(d.Title + " " + d.TopicKey + " " + d.Content)
	}
	body, err := json.Marshal(rerankRequest{Model: h.model, Query: query, Documents: documents, TopN: len(batch)})
	if err != nil {
		return nil, fmt.Errorf("rerank: encode request: %w", err)
	}
	resp, err := h.post(body)
	if err != nil {
		return nil, err
	}
	// Validate the entire result set before any caller-visible mutation so a
	// malformed batch can never leave the input half-applied (atomic reject).
	scores := make([]float64, len(batch))
	seen := make(map[int]struct{}, len(resp.Results))
	for _, r := range resp.Results {
		if r.Index < 0 || r.Index >= len(batch) {
			return nil, fmt.Errorf("rerank: result index %d out of range [0,%d)", r.Index, len(batch))
		}
		if _, dup := seen[r.Index]; dup {
			return nil, fmt.Errorf("rerank: duplicate result index %d", r.Index)
		}
		seen[r.Index] = struct{}{}
		scores[r.Index] = r.RelevanceScore
	}
	if len(seen) != len(batch) {
		return nil, fmt.Errorf("rerank: results cover %d of %d documents", len(seen), len(batch))
	}
	out := make([]rerankScored, 0, len(batch))
	for i, d := range batch {
		out = append(out, rerankScored{doc: d, score: scores[i]})
	}
	return out, nil
}

// post performs one batch request with 429 backoff (1s<<attempt, at most 3
// retries) and paces every attempt against the provider rate budget (the
// shared bucket when one is composed, the private 60-rpm default otherwise).
// rerankEndpoint builds the provider URL (issue #123): operators configure
// the base URL in the same version-inclusive preset shape as the embedding
// provider (e.g. "https://host/v1", which the embedding client turns into
// /v1/embeddings). A base whose path already ends in a version segment gets
// only "/rerank" appended — appending "/v1/rerank" produced /v1/v1/rerank
// and a deterministic 404 that made the gate fail open on every search. A
// bare host keeps the documented "/v1/rerank" default.
func rerankEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if u, err := url.Parse(base); err == nil && rerankVersionedPath(u.Path) {
		return base + "/rerank"
	}
	return base + "/v1/rerank"
}

func rerankVersionedPath(path string) bool {
	trimmed := strings.TrimSuffix(path, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		return false
	}
	segment := strings.ToLower(trimmed[idx+1:])
	if len(segment) < 2 || segment[0] != 'v' {
		return false
	}
	for _, r := range segment[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (h *httpReranker) post(body []byte) (*rerankResponse, error) {
	endpoint := rerankEndpoint(h.baseURL)
	for attempt := 0; ; attempt++ {
		h.budget.Wait()
		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("rerank: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
		resp, err := h.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("rerank: request failed: %w", err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if attempt >= rerankMaxRetries429 {
				return nil, fmt.Errorf("rerank: rate limited after %d retries", rerankMaxRetries429)
			}
			h.sleep(time.Second << attempt)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			apiErr := embedding.NewAPIError("rerank", resp.StatusCode, resp.Body)
			_ = resp.Body.Close()
			return nil, apiErr
		}
		var parsed rerankResponse
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, rerankMaxResponseLen)).Decode(&parsed)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("rerank: decode response: %w", decodeErr)
		}
		return &parsed, nil
	}
}

// approveRerankURL re-validates a rerank destination against the approved
// outbound policy. embedding keeps its twin validator unexported, so the
// reranker mirrors the same allowlist rules over the policy's exported
// fields: approved host/port only, plain HTTP only on opted-in loopback,
// and literal IPs must pass the IP-class gate.
func approveRerankURL(policy embedding.OutboundPolicy, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return errors.New("rerank: outbound destination rejected")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if !rerankContainsHost(policy.AllowedHosts, host) {
		return errors.New("rerank: outbound destination rejected")
	}
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || !rerankContainsPort(policy.AllowedPorts, n) {
		return errors.New("rerank: outbound destination rejected")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !policy.AllowInsecureLoopbackHTTP || !rerankIsLoopbackHost(host) {
			return errors.New("rerank: outbound destination rejected")
		}
	default:
		return errors.New("rerank: outbound destination rejected")
	}
	if ip := net.ParseIP(host); ip != nil && !rerankIPAllowed(policy, ip) {
		return errors.New("rerank: outbound destination rejected")
	}
	return nil
}

// rerankDialContext enforces the IP-class filter at dial time so an approved
// hostname that resolves to a private or link-local address still cannot be
// reached with the rerank API key.
func rerankDialContext(policy embedding.OutboundPolicy) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("rerank: outbound dial rejected")
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, errors.New("rerank: outbound resolution failed")
		}
		for _, ip := range ips {
			if rerankIPAllowed(policy, ip) {
				return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			}
		}
		return nil, errors.New("rerank: outbound dial rejected")
	}
}

func rerankIPAllowed(policy embedding.OutboundPolicy, ip net.IP) bool {
	if ip.IsLoopback() {
		return policy.AllowLoopback
	}
	return !ip.IsPrivate() && !ip.IsUnspecified() && !ip.IsMulticast() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}

func rerankIsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func rerankContainsHost(hosts []string, want string) bool {
	for _, h := range hosts {
		if strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(h), "."), want) {
			return true
		}
	}
	return false
}

func rerankContainsPort(ports []int, want int) bool {
	for _, p := range ports {
		if p == want {
			return true
		}
	}
	return false
}

func normalizeRerankProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "" {
		return rerankProviderNone
	}
	return p
}
