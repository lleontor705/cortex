// Package extraction provides automated extraction and synthesis of observations,
// entities, and knowledge-graph relationships from raw text, code notes, and session logs.
package extraction

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/transportpolicy"
)

// LLMProvider represents the supported LLM provider types.
type LLMProvider string

const (
	ProviderOpenAI    LLMProvider = "openai"
	ProviderAnthropic LLMProvider = "anthropic"
	ProviderOllama    LLMProvider = "ollama"
	ProviderGeneric   LLMProvider = "generic"
)

// Config holds LLM client parameters for server-side extraction.
type Config struct {
	Provider    LLMProvider   `json:"provider"`
	BaseURL     string        `json:"base_url"`
	APIKey      string        `json:"api_key"`
	Model       string        `json:"model"`
	Temperature float64       `json:"temperature"`
	Timeout     time.Duration `json:"timeout"`
}

// Stable sentinel outbound errors (SEC-02). Messages are static: they never
// embed URLs, hostnames, addresses, credentials, or upstream bodies.
var (
	// ErrUnsafeDestination marks a destination rejected by the outbound
	// policy at the URL, DNS-resolution/dial, or redirect layer. It aborts
	// extraction instead of falling back to heuristics.
	ErrUnsafeDestination = errors.New("extraction: outbound destination rejected by policy")
	// ErrResponseTooLarge marks a provider response that exceeded the
	// configured size bound before decoding. It also aborts.
	ErrResponseTooLarge = errors.New("extraction: provider response exceeded the size limit")
	// ErrProviderUnavailable marks transport-level provider failures.
	ErrProviderUnavailable = errors.New("extraction: provider request failed")
	// ErrProviderRejected marks a non-2xx provider response.
	ErrProviderRejected = errors.New("extraction: provider returned an error status")
	// ErrInvalidProviderResponse marks an unparseable provider payload.
	ErrInvalidProviderResponse = errors.New("extraction: provider response could not be parsed")
)

// Outbound bounds and defaults for the outbound policy.
const (
	defaultMaxRedirects               = 3
	defaultMaxResponseBodyBytes int64 = 4 << 20 // 4 MiB
	defaultMaxErrorBodyBytes    int64 = 4 << 10 // 4 KiB
	defaultMaxConcurrent              = 4
)

// OutboundPolicy is the reusable destination policy (SEC-02) injected into
// the extraction Service. It is enforced at three layers:
//
//  1. URL layer: ValidateURL runs before any request is built or a credential
//     is attached. It requires HTTPS (plain HTTP only under an explicit
//     local-only development switch), rejects userinfo, requires an approved
//     port, requires the host to be admin-approved, and denies IP literals in
//     loopback/private/link-local/metadata/multicast/unspecified/broadcast/
//     shared (CGNAT) ranges for both IPv4 and IPv6 (including IPv4-mapped
//     spellings).
//  2. Dial layer: a custom DialContext resolves the host, filters every
//     resolved address through the same address-class rules, and dials only
//     an approved resolved address directly — a DNS rebinding between
//     validation and dial cannot reach a denied address.
//  3. Redirect layer: CheckRedirect revalidates every redirect target URL
//     through ValidateURL and caps the hop count.
//
// All fields are admin/server configuration. Request data can never widen
// the policy.
type OutboundPolicy struct {
	// AllowedHosts is the admin-approved destination allowlist (exact,
	// case-insensitive hostnames without port; IP literals additionally pass
	// the address-class rules). Empty means no outbound destination is
	// approved.
	AllowedHosts []string
	// AllowedPorts lists approved TCP ports. Default: 443 only.
	AllowedPorts []int
	// AllowLoopback is an explicit local-only development switch that
	// permits loopback addresses for approved hosts (still HTTPS-only).
	AllowLoopback bool
	// AllowInsecureLoopbackHTTP is an explicit local-only development switch
	// that permits plain HTTP to strict loopback hosts (see
	// transportpolicy.IsStrictLoopbackHost).
	AllowInsecureLoopbackHTTP bool
	// MaxRedirects caps the redirect chain length. Default: 3.
	MaxRedirects int
	// MaxResponseBodyBytes bounds the provider success body. Default: 4 MiB.
	MaxResponseBodyBytes int64
	// MaxErrorBodyBytes bounds how much of an error body is drained. Default: 4 KiB.
	MaxErrorBodyBytes int64
	// MaxConcurrent bounds concurrent outbound provider requests. Default: 4.
	MaxConcurrent int
	// TLSConfig is an optional admin TLS configuration (e.g. private CA
	// roots). It never relaxes destination validation.
	TLSConfig *tls.Config

	// hosts/ports are normalized views built by normalize.
	hosts map[string]struct{}
	ports map[int]struct{}
	// resolver/dial are injection points for tests; production uses the
	// default resolver and dialer.
	resolver func(ctx context.Context, host string) ([]net.IP, error)
	dial     func(ctx context.Context, network, address string) (net.Conn, error)
}

// DefaultOutboundPolicy returns the strict server policy: HTTPS on port 443
// only, no approved hosts (outbound LLM disabled until an administrator
// approves a destination), loopback denied.
func DefaultOutboundPolicy() OutboundPolicy {
	return OutboundPolicy{AllowedPorts: []int{443}}
}

func (p *OutboundPolicy) normalize() {
	if len(p.AllowedPorts) == 0 {
		p.AllowedPorts = []int{443}
	}
	p.ports = make(map[int]struct{}, len(p.AllowedPorts))
	for _, port := range p.AllowedPorts {
		if port > 0 && port <= 65535 {
			p.ports[port] = struct{}{}
		}
	}
	if p.MaxRedirects <= 0 {
		p.MaxRedirects = defaultMaxRedirects
	}
	if p.MaxResponseBodyBytes <= 0 {
		p.MaxResponseBodyBytes = defaultMaxResponseBodyBytes
	}
	if p.MaxErrorBodyBytes <= 0 {
		p.MaxErrorBodyBytes = defaultMaxErrorBodyBytes
	}
	if p.MaxConcurrent <= 0 {
		p.MaxConcurrent = defaultMaxConcurrent
	}
	p.hosts = make(map[string]struct{}, len(p.AllowedHosts))
	for _, host := range p.AllowedHosts {
		host = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(host, ".")))
		if host != "" {
			p.hosts[host] = struct{}{}
		}
	}
}

// ValidateURL enforces the URL layer of the outbound policy. Rejection
// errors wrap ErrUnsafeDestination with static, redacted messages.
func (p *OutboundPolicy) ValidateURL(u *url.URL) error {
	if u == nil || u.Host == "" {
		return fmt.Errorf("%w: destination is not an absolute HTTP(S) URL", ErrUnsafeDestination)
	}
	if u.User != nil {
		return fmt.Errorf("%w: destination must not embed userinfo", ErrUnsafeDestination)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "https":
	case "http":
		if !p.AllowInsecureLoopbackHTTP || !transportpolicy.IsStrictLoopbackHost(u.Hostname()) {
			return fmt.Errorf("%w: plain HTTP destinations are not permitted", ErrUnsafeDestination)
		}
	default:
		return fmt.Errorf("%w: destination scheme is not an approved HTTPS transport", ErrUnsafeDestination)
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	portNo, err := strconv.Atoi(port)
	if err != nil || portNo <= 0 || portNo > 65535 {
		return fmt.Errorf("%w: destination port is not approved", ErrUnsafeDestination)
	}
	if _, ok := p.ports[portNo]; !ok {
		return fmt.Errorf("%w: destination port is not approved", ErrUnsafeDestination)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if ip := net.ParseIP(host); ip != nil {
		if !p.ipApproved(ip) {
			return fmt.Errorf("%w: destination address class is denied", ErrUnsafeDestination)
		}
		return nil
	}
	if _, ok := p.hosts[host]; !ok {
		return fmt.Errorf("%w: destination host is not approved", ErrUnsafeDestination)
	}
	return nil
}

// ipApproved reports whether an address passes the policy's address-class
// rules. Loopback is only allowed under the explicit AllowLoopback
// development switch; private, link-local (metadata), multicast, unspecified,
// broadcast, shared/CGNAT, and IPv4-mapped spellings of denied classes are
// always rejected.
func (p *OutboundPolicy) ipApproved(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.Equal(net.IPv4bcast) {
		return false
	}
	if ip.IsLoopback() {
		return p.AllowLoopback
	}
	if ip.IsPrivate() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false // 100.64.0.0/10 shared/CGNAT space
	}
	return ip.IsGlobalUnicast()
}

// checkRedirect implements http.Client.CheckRedirect: every redirect target
// is revalidated through ValidateURL and the chain length is capped.
func (p *OutboundPolicy) checkRedirect(req *http.Request, via []*http.Request) error {
	if p.MaxRedirects > 0 && len(via) >= p.MaxRedirects {
		return fmt.Errorf("%w: redirect chain exceeds the configured hop limit", ErrUnsafeDestination)
	}
	return p.ValidateURL(req.URL)
}

// dialContext implements the dial layer: resolve, filter every address
// through ipApproved, and dial only an approved resolved address directly.
// The connection is made to the validated IP itself, so a DNS rebinding
// between validation and dial cannot redirect traffic to a denied address.
func (p *OutboundPolicy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed dial address", ErrUnsafeDestination)
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		ips, err = p.lookup(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("%w: provider host could not be resolved", ErrProviderUnavailable)
		}
	}
	for _, ip := range ips {
		if !p.ipApproved(ip) {
			continue
		}
		dialAddr := net.JoinHostPort(ip.String(), port)
		if p.dial != nil {
			return p.dial(ctx, network, dialAddr)
		}
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, dialAddr)
	}
	return nil, fmt.Errorf("%w: destination resolved only to addresses denied by policy", ErrUnsafeDestination)
}

func (p *OutboundPolicy) lookup(ctx context.Context, host string) ([]net.IP, error) {
	if p.resolver != nil {
		return p.resolver(ctx, host)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		ips = append(ips, addr.IP)
	}
	return ips, nil
}

// isPolicyAbort reports whether an LLM error must abort the operation rather
// than fall back to the heuristic extractor.
func isPolicyAbort(err error) bool {
	return errors.Is(err, ErrUnsafeDestination) || errors.Is(err, ErrResponseTooLarge)
}

// ObservationDraft represents an observation extracted from raw input.
type ObservationDraft struct {
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Type       string   `json:"type"` // decision, bugfix, pattern, architecture, discovery, manual
	Project    string   `json:"project"`
	Scope      string   `json:"scope"`
	Confidence float64  `json:"confidence"`
	Tags       []string `json:"tags"`
	Entities   []string `json:"entities,omitempty"`
}

// EdgeDraft represents a candidate knowledge graph relationship.
type EdgeDraft struct {
	FromTitle    string  `json:"from_title"`
	ToTitle      string  `json:"to_title"`
	RelationType string  `json:"relation_type"` // references, relates_to, follows, supersedes, contradicts
	Reasoning    string  `json:"reasoning"`
	Confidence   float64 `json:"confidence"`
}

// ExtractionRequest represents a payload submitted for knowledge extraction.
type ExtractionRequest struct {
	Text      string  `json:"text"`
	Project   string  `json:"project"`
	SessionID string  `json:"session_id,omitempty"`
	Source    string  `json:"source,omitempty"`
	LLMConfig *Config `json:"llm_config,omitempty"`
}

// ExtractionResult is the output of an extraction operation.
type ExtractionResult struct {
	Observations []*ObservationDraft `json:"observations"`
	Edges        []*EdgeDraft        `json:"edges"`
	Summary      string              `json:"summary"`
	SourceMethod string              `json:"source_method"` // "llm" or "heuristic"
	ExtractedAt  time.Time           `json:"extracted_at"`
}

// SynthesisRequest represents a request to consolidate multiple observations.
type SynthesisRequest struct {
	Project      string                `json:"project"`
	Observations []*domain.Observation `json:"observations"`
	LLMConfig    *Config               `json:"llm_config,omitempty"`
}

// SynthesisResult represents the consolidated knowledge summary.
type SynthesisResult struct {
	Project       string    `json:"project"`
	Summary       string    `json:"summary"`
	KeyDecisions  []string  `json:"key_decisions"`
	Patterns      []string  `json:"patterns"`
	OpenIssues    []string  `json:"open_issues"`
	SynthesizedAt time.Time `json:"synthesized_at"`
}

// Service provides extraction and synthesis capabilities.
type Service struct {
	httpClient *http.Client
	defaultCfg Config
	policy     *OutboundPolicy
	sem        chan struct{}
}

// NewService creates a new extraction service. The outbound policy is derived
// from the (trusted, admin-provided) configuration: its destination host (and
// explicit port) is approved into the default policy. The URL-layer policy
// still validates scheme, port, and address class at request time, so plain
// HTTP or loopback/private destinations remain rejected unless the policy
// carries explicit development switches — use NewServiceWithPolicy for those.
func NewService(cfg Config) *Service {
	policy := DefaultOutboundPolicy()
	if base := providerBaseURL(cfg); base != "" {
		// Approval only widens the host/port allowlist; the request-time
		// URL/dial/redirect layers still enforce scheme and address class.
		_ = policy.ApproveDestination(base)
	}
	return NewServiceWithPolicy(cfg, policy)
}

// NewServiceWithPolicy creates a new extraction service with an explicit
// outbound destination policy. This is the server-mode constructor: the
// policy is composed by the server, never from request data. It performs NO
// implicit approval — the composition must approve the trusted configured
// destination itself via ApproveDestination so an explicit policy approves
// exactly what its author intended.
func NewServiceWithPolicy(cfg Config, policy OutboundPolicy) *Service {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	policy.normalize()
	transport := &http.Transport{DialContext: policy.dialContext}
	if policy.TLSConfig != nil {
		transport.TLSClientConfig = policy.TLSConfig.Clone()
	}
	return &Service{
		httpClient: &http.Client{
			Transport:     transport,
			CheckRedirect: policy.checkRedirect,
			Timeout:       cfg.Timeout,
		},
		defaultCfg: cfg,
		policy:     &policy,
		sem:        make(chan struct{}, policy.MaxConcurrent),
	}
}

// ApproveDestination adds the host and explicit port of a trusted
// (administrator-configured) HTTP(S) destination URL to the allowlists. It
// rejects userinfo and non-HTTP(S) schemes, and must be called before
// NewServiceWithPolicy normalizes the policy. Approval only widens the
// host/port allowlist: every request is still validated against the full
// policy (scheme, port, address class, redirects, dial targets).
func (p *OutboundPolicy) ApproveDestination(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return fmt.Errorf("%w: trusted destination is not an absolute HTTP(S) URL", ErrUnsafeDestination)
	}
	if u.User != nil {
		return fmt.Errorf("%w: trusted destination must not embed userinfo", ErrUnsafeDestination)
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http":
	default:
		return fmt.Errorf("%w: trusted destination scheme is not an HTTP(S) transport", ErrUnsafeDestination)
	}
	if host := strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")); host != "" {
		p.AllowedHosts = append(p.AllowedHosts, host)
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err == nil && n > 0 && n <= 65535 {
			p.AllowedPorts = append(p.AllowedPorts, n)
		}
	}
	return nil
}

// ProviderDefaultBaseURL returns the canonical HTTPS endpoint for a provider
// preset (empty string for empty or unknown providers).
func ProviderDefaultBaseURL(provider LLMProvider) string {
	switch provider {
	case ProviderAnthropic:
		return "https://api.anthropic.com/v1"
	case ProviderOpenAI, ProviderGeneric:
		return "https://api.openai.com/v1"
	}
	return ""
}

// Extract processes raw text and extracts structured observations and graph edges.
func (s *Service) Extract(ctx context.Context, req ExtractionRequest) (*ExtractionResult, error) {
	if strings.TrimSpace(req.Text) == "" {
		return nil, errors.New("extraction: empty text payload")
	}

	// SEC-02: the effective LLM configuration is exclusively the trusted
	// configuration the Service was constructed with. The request-level
	// LLMConfig field exists for JSON decoding compatibility only and is
	// never used to select a destination or credential.
	cfg := s.defaultCfg
	if cfg.APIKey != "" || cfg.BaseURL != "" {
		res, err := s.extractWithLLM(ctx, req, cfg)
		if err == nil && len(res.Observations) > 0 {
			res.SourceMethod = "llm"
			res.ExtractedAt = time.Now().UTC()
			return res, nil
		}
		if isPolicyAbort(err) {
			return nil, err
		}
	}

	// Fallback to deterministic heuristic extractor
	res := s.extractHeuristically(req)
	res.SourceMethod = "heuristic"
	res.ExtractedAt = time.Now().UTC()
	return res, nil
}

// Synthesize consolidates observations into an overarching architectural summary.
func (s *Service) Synthesize(ctx context.Context, req SynthesisRequest) (*SynthesisResult, error) {
	if len(req.Observations) == 0 {
		return nil, errors.New("synthesis: no observations provided")
	}

	// SEC-02: same server-managed configuration rule as Extract.
	cfg := s.defaultCfg
	if cfg.APIKey != "" || cfg.BaseURL != "" {
		res, err := s.synthesizeWithLLM(ctx, req, cfg)
		if err == nil {
			res.SynthesizedAt = time.Now().UTC()
			return res, nil
		}
		if isPolicyAbort(err) {
			return nil, err
		}
	}

	// Heuristic summary
	var decisions, patterns, issues []string
	for _, obs := range req.Observations {
		switch obs.Type {
		case "decision":
			decisions = append(decisions, fmt.Sprintf("%s: %s", obs.Title, obs.Content))
		case "pattern", "architecture":
			patterns = append(patterns, fmt.Sprintf("%s: %s", obs.Title, obs.Content))
		case "bugfix":
			issues = append(issues, fmt.Sprintf("%s: %s", obs.Title, obs.Content))
		default:
			decisions = append(decisions, obs.Title)
		}
	}

	return &SynthesisResult{
		Project:       req.Project,
		Summary:       fmt.Sprintf("Consolidated %d knowledge observations for project %s.", len(req.Observations), req.Project),
		KeyDecisions:  decisions,
		Patterns:      patterns,
		OpenIssues:    issues,
		SynthesizedAt: time.Now().UTC(),
	}, nil
}

func (s *Service) extractWithLLM(ctx context.Context, req ExtractionRequest, cfg Config) (*ExtractionResult, error) {
	model := cfg.Model
	if model == "" {
		model = "gpt-4o-mini"
	}

	prompt := fmt.Sprintf(`You are a knowledge extraction engine for software development agents.
Extract knowledge observations and relationship edges from the text below for project %q.

Output ONLY valid JSON matching this schema:
{
  "observations": [
    {
      "title": "Concise summary title",
      "content": "Detailed factual explanation",
      "type": "decision|bugfix|pattern|architecture|discovery|manual",
      "project": %q,
      "scope": "project",
      "confidence": 0.95,
      "tags": ["tag1", "tag2"],
      "entities": ["entity1", "entity2"]
    }
  ],
  "edges": [
    {
      "from_title": "Source Observation Title",
      "to_title": "Target Observation Title",
      "relation_type": "references|relates_to|follows|supersedes|contradicts",
      "reasoning": "Why these observations relate",
      "confidence": 0.9
    }
  ],
  "summary": "High-level summary of findings"
}

Text to analyze:
%s`, req.Project, req.Project, req.Text)

	type openAIMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type openAIReq struct {
		Model       string          `json:"model"`
		Messages    []openAIMessage `json:"messages"`
		Temperature float64         `json:"temperature"`
	}

	data, err := s.callProvider(ctx, cfg, openAIReq{
		Model: model,
		Messages: []openAIMessage{
			{Role: "system", Content: "You extract structured knowledge items for software agents. Respond strictly in JSON format."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.2,
	})
	if err != nil {
		return nil, err
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &chatResp); err != nil {
		return nil, ErrInvalidProviderResponse
	}

	if len(chatResp.Choices) == 0 {
		return nil, ErrInvalidProviderResponse
	}

	rawContent := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	rawContent = strings.TrimPrefix(rawContent, "```json")
	rawContent = strings.TrimPrefix(rawContent, "```")
	rawContent = strings.TrimSuffix(rawContent, "```")
	rawContent = strings.TrimSpace(rawContent)

	var result ExtractionResult
	if err := json.Unmarshal([]byte(rawContent), &result); err != nil {
		return nil, ErrInvalidProviderResponse
	}

	return &result, nil
}

// callProvider performs the single outbound provider round-trip under the
// outbound policy: the endpoint URL is validated before any credential is
// attached, concurrency is bounded by the policy semaphore, redirects are
// revalidated by the client's CheckRedirect hook, every dial target is
// filtered by the policy dialer, and response/error bodies are bounded before
// decoding. All returned errors are the stable sentinel errors and never
// carry URLs, addresses, credentials, or upstream body content.
func (s *Service) callProvider(ctx context.Context, cfg Config, payload any) ([]byte, error) {
	bodyJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: provider request could not be encoded", ErrProviderUnavailable)
	}

	endpoint := fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(providerBaseURL(cfg), "/"))
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: provider endpoint is not a valid URL", ErrUnsafeDestination)
	}
	if err := s.policy.ValidateURL(endpointURL); err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("%w: provider request could not be built", ErrProviderUnavailable)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		// The credential is attached only after the destination passed
		// validation (SEC-02: no secret transmission to denied targets).
		httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: cancelled while waiting for an outbound slot", ErrProviderUnavailable)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, ErrUnsafeDestination) {
			// Re-wrap to strip the url.Error, which embeds the full URL.
			return nil, fmt.Errorf("%w: outbound destination rejected by policy", ErrUnsafeDestination)
		}
		return nil, fmt.Errorf("%w: provider request failed", ErrProviderUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Bounded drain (never surfaced) so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, s.policy.MaxErrorBodyBytes+1))
		return nil, fmt.Errorf("%w: status %d", ErrProviderRejected, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, s.policy.MaxResponseBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: provider response stream failed", ErrProviderUnavailable)
	}
	if int64(len(data)) > s.policy.MaxResponseBodyBytes {
		return nil, ErrResponseTooLarge
	}
	return data, nil
}

func providerBaseURL(cfg Config) string {
	if cfg.BaseURL != "" {
		return cfg.BaseURL
	}
	return ProviderDefaultBaseURL(cfg.Provider)
}

func (s *Service) synthesizeWithLLM(ctx context.Context, req SynthesisRequest, cfg Config) (*SynthesisResult, error) {
	model := cfg.Model
	if model == "" {
		model = "gpt-4o-mini"
	}

	var sb strings.Builder
	for i, obs := range req.Observations {
		fmt.Fprintf(&sb, "%d. [%s] %s: %s\n", i+1, obs.Type, obs.Title, obs.Content)
	}

	prompt := fmt.Sprintf(`Synthesize the following observations for project %q into a cohesive knowledge map.
Output ONLY valid JSON matching:
{
  "project": %q,
  "summary": "Overarching summary",
  "key_decisions": ["Decision 1", "Decision 2"],
  "patterns": ["Pattern 1", "Pattern 2"],
  "open_issues": ["Issue 1"]
}

Observations:
%s`, req.Project, req.Project, sb.String())

	type openAIMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type openAIReq struct {
		Model       string          `json:"model"`
		Messages    []openAIMessage `json:"messages"`
		Temperature float64         `json:"temperature"`
	}

	data, err := s.callProvider(ctx, cfg, openAIReq{
		Model: model,
		Messages: []openAIMessage{
			{Role: "system", Content: "You synthesize architecture notes. Return strictly JSON."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.2,
	})
	if err != nil {
		return nil, err
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &chatResp); err != nil {
		return nil, ErrInvalidProviderResponse
	}
	if len(chatResp.Choices) == 0 {
		return nil, ErrInvalidProviderResponse
	}

	rawContent := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	rawContent = strings.TrimPrefix(rawContent, "```json")
	rawContent = strings.TrimPrefix(rawContent, "```")
	rawContent = strings.TrimSuffix(rawContent, "```")
	rawContent = strings.TrimSpace(rawContent)

	var result SynthesisResult
	if err := json.Unmarshal([]byte(rawContent), &result); err != nil {
		return nil, ErrInvalidProviderResponse
	}
	return &result, nil
}

var (
	decisionRegex = regexp.MustCompile(`(?i)(?:decid(?:ed|e|ing)|decision|chose|selected|migrated to)\s+(.+?)(?:\.|\n|$)`)
	bugfixRegex   = regexp.MustCompile(`(?i)(?:fixed|bug|error|resolved|patched|issue with)\s+(.+?)(?:\.|\n|$)`)
	patternRegex  = regexp.MustCompile(`(?i)(?:pattern|convention|architecture|standard|rule)\s*:\s*(.+?)(?:\.|\n|$)`)
)

func (s *Service) extractHeuristically(req ExtractionRequest) *ExtractionResult {
	lines := strings.Split(req.Text, "\n")
	var obs []*ObservationDraft
	project := req.Project
	if project == "" {
		project = "default"
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if match := decisionRegex.FindStringSubmatch(trimmed); len(match) > 1 {
			obs = append(obs, &ObservationDraft{
				Title:      truncateTitle(match[1]),
				Content:    trimmed,
				Type:       "decision",
				Project:    project,
				Scope:      "project",
				Confidence: 0.85,
				Tags:       []string{"decision", "architecture"},
			})
		} else if match := bugfixRegex.FindStringSubmatch(trimmed); len(match) > 1 {
			obs = append(obs, &ObservationDraft{
				Title:      truncateTitle(match[1]),
				Content:    trimmed,
				Type:       "bugfix",
				Project:    project,
				Scope:      "project",
				Confidence: 0.85,
				Tags:       []string{"bugfix", "fix"},
			})
		} else if match := patternRegex.FindStringSubmatch(trimmed); len(match) > 1 {
			obs = append(obs, &ObservationDraft{
				Title:      truncateTitle(match[1]),
				Content:    trimmed,
				Type:       "pattern",
				Project:    project,
				Scope:      "project",
				Confidence: 0.90,
				Tags:       []string{"pattern", "standard"},
			})
		}
	}

	if len(obs) == 0 && len(strings.TrimSpace(req.Text)) > 0 {
		obs = append(obs, &ObservationDraft{
			Title:      truncateTitle(req.Text),
			Content:    req.Text,
			Type:       "manual",
			Project:    project,
			Scope:      "project",
			Confidence: 0.70,
			Tags:       []string{"note"},
		})
	}

	var edges []*EdgeDraft
	for i := 0; i < len(obs)-1; i++ {
		edges = append(edges, &EdgeDraft{
			FromTitle:    obs[i].Title,
			ToTitle:      obs[i+1].Title,
			RelationType: "relates_to",
			Reasoning:    "Extracted together in the same session context",
			Confidence:   0.8,
		})
	}

	return &ExtractionResult{
		Observations: obs,
		Edges:        edges,
		Summary:      fmt.Sprintf("Heuristically extracted %d observations and %d relationships.", len(obs), len(edges)),
	}
}

func truncateTitle(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}
