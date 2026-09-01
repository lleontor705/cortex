package server

import (
	"bufio"
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
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	"github.com/lleontor705/cortex/v2/internal/domain/extraction"
	"github.com/lleontor705/cortex/v2/internal/embedding"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
	"github.com/lleontor705/cortex/v2/internal/transportpolicy"
)

var sourceHandleRegex = regexp.MustCompile(`src_[a-f0-9]+_\d+`)

type llmClaim struct {
	Text            string   `json:"text"`
	CitationHandles []string `json:"citation_handles"`
	Sources         []string `json:"sources"`
	Citations       []string `json:"citations"`
}

func (c llmClaim) toDomain() agentdomain.CompletionClaim {
	handles := c.CitationHandles
	if len(handles) == 0 {
		handles = c.Sources
	}
	if len(handles) == 0 {
		handles = c.Citations
	}
	if len(handles) == 0 {
		matches := sourceHandleRegex.FindAllString(c.Text, -1)
		if len(matches) > 0 {
			handles = matches
		}
	}
	return agentdomain.CompletionClaim{
		Text:            c.Text,
		CitationHandles: handles,
	}
}

type agentProject struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type serverAgentRequest struct {
	tenantID, workspaceID string
	project               agentProject
	question              string
	history               []agentdomain.Message
}

type serverAgentService struct{ core *agentdomain.Service }

var sharedAgentRetrievalCache = retrieval.NewScopedCache[agentdomain.RetrievalResult](512, 5*time.Minute)

func newServerAgentService(ops agentRetrievalOperations, vectors domain.VectorIndex, embeddings embedding.Service, completion agentdomain.CompletionProvider) *serverAgentService {
	retriever := scopedAgentRetriever{
		ops:        ops,
		vectors:    vectors,
		embeddings: embeddings,
		summaries:  sharedAgentSummaryCache,
		cache:      sharedAgentRetrievalCache,
	}
	return &serverAgentService{core: agentdomain.NewScopedService(retriever, completion)}
}

func (s *serverAgentService) Answer(ctx context.Context, req serverAgentRequest) (agentdomain.Answer, error) {
	domainReq, ctx, err := s.domainRequest(ctx, req)
	if err != nil {
		return agentdomain.Answer{}, err
	}
	return s.core.Answer(ctx, domainReq)
}

func (s *serverAgentService) Stream(ctx context.Context, req serverAgentRequest, callbacks agentdomain.StreamCallbacks) (agentdomain.Answer, error) {
	domainReq, ctx, err := s.domainRequest(ctx, req)
	if err != nil {
		return agentdomain.Answer{}, err
	}
	return s.core.Stream(ctx, domainReq, callbacks)
}

func (s *serverAgentService) domainRequest(ctx context.Context, req serverAgentRequest) (agentdomain.Request, context.Context, error) {
	if s == nil || s.core == nil || strings.TrimSpace(req.project.Label) == "" {
		return agentdomain.Request{}, ctx, &agentdomain.Error{Code: agentdomain.ErrorInvalidRequest}
	}
	projectID, err := uuid.Parse(strings.TrimSpace(req.project.ID))
	if err != nil || projectID == uuid.Nil {
		return agentdomain.Request{}, ctx, &agentdomain.Error{Code: agentdomain.ErrorInvalidRequest, Err: errors.New("project id is invalid")}
	}
	ctx = context.WithValue(ctx, agentProjectIDKey{}, projectID.String())
	return agentdomain.Request{
		Scope:    agentdomain.Scope{TenantID: req.tenantID, WorkspaceID: req.workspaceID, Project: strings.TrimSpace(req.project.Label)},
		Question: req.question, History: req.history,
	}, ctx, nil
}

type agentProjectIDKey struct{}

type agentRetrievalOperations interface {
	SearchAgentObservations(context.Context, string, string, string, domain.SearchOptions) ([]*domain.SearchResult, error)
	GetAgentObservationByID(context.Context, string, string, int64) (*domain.Observation, error)
	ListCodeSymbols(context.Context, code.SymbolFilter) ([]code.Symbol, error)
	GetCodeGraph(context.Context, string) (*code.CodeGraph, error)
}

type agentMemoryRetriever struct {
	ops        agentRetrievalOperations
	vectors    domain.VectorIndex
	embeddings embedding.Service
}

func (r agentMemoryRetriever) Retrieve(ctx context.Context, scope agentdomain.Scope, query string, limit int) ([]agentdomain.Evidence, error) {
	if r.ops == nil {
		return nil, errors.New("agent memory retrieval unavailable")
	}
	projectID, ok := ctx.Value(agentProjectIDKey{}).(string)
	if !ok || projectID == "" {
		return nil, errors.New("agent memory scope unavailable")
	}
	opts := domain.SearchOptions{Query: query, Project: scope.Project, Limit: limit}
	results, err := r.ops.SearchAgentObservations(ctx, projectID, scope.Project, query, opts)
	if err != nil {
		return nil, err
	}
	if r.embeddings != nil && domain.IsVectorIndexHealthy(ctx, r.vectors) {
		vector, embedErr := r.embeddings.Embed(ctx, query)
		if embedErr == nil && len(vector) > 0 {
			found, searchErr := retrieval.SearchVectors(ctx, r.vectors, domain.VectorQuery{
				Vector: vector, Limit: limit, Threshold: .3,
				Filters: map[string]any{"tenant_id": scope.TenantID, "workspace_id": scope.WorkspaceID, "project_id": projectID},
			}, agentObservationLookup{ops: r.ops, projectID: projectID, projectLabel: scope.Project})
			if searchErr == nil && len(found) > 0 {
				results = retrieval.FuseResults(results, found, limit)
			}
		}
	}
	evidence := make([]agentdomain.Evidence, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		score := result.Rank
		if score <= 0 {
			score = .5
		}
		evidence = append(evidence, agentdomain.Evidence{Title: result.Title, Content: result.Content, Score: score})
	}
	return evidence, nil
}

type agentObservationLookup struct {
	ops                     agentRetrievalOperations
	projectID, projectLabel string
}

func (o agentObservationLookup) GetByID(ctx context.Context, id int64) (*domain.Observation, error) {
	return o.ops.GetAgentObservationByID(ctx, o.projectID, o.projectLabel, id)
}

type agentCodeRetriever struct{ ops agentRetrievalOperations }

func (r agentCodeRetriever) Retrieve(ctx context.Context, scope agentdomain.Scope, query string, limit int) ([]agentdomain.Evidence, error) {
	projectID, ok := ctx.Value(agentProjectIDKey{}).(string)
	if !ok || projectID == "" || r.ops == nil {
		return nil, errors.New("agent code scope unavailable")
	}
	symbols, err := r.ops.ListCodeSymbols(ctx, code.SymbolFilter{Project: projectID, Query: query, Limit: limit})
	if err != nil {
		return nil, err
	}
	for _, symbol := range symbols {
		if symbol.Project != projectID {
			return nil, errors.New("agent code project identity mismatch")
		}
	}
	relations := map[string][]string{}
	if graph, graphErr := r.ops.GetCodeGraph(ctx, projectID); graphErr == nil && graph != nil {
		if graph.Project != "" && graph.Project != projectID {
			return nil, errors.New("agent code project identity mismatch")
		}
		for _, relation := range graph.Relations {
			relations[relation.SourceID] = append(relations[relation.SourceID], relation.Relation+" -> "+relation.TargetID)
			relations[relation.TargetID] = append(relations[relation.TargetID], relation.SourceID+" -> "+relation.Relation)
		}
	}
	evidence := make([]agentdomain.Evidence, 0, len(symbols))
	for _, symbol := range symbols {
		content := strings.TrimSpace(strings.Join([]string{symbol.Kind + " " + symbol.Name, symbol.Signature, symbol.DocSummary, strings.Join(relations[symbol.ID], "; ")}, "\n"))
		evidence = append(evidence, agentdomain.Evidence{Title: symbol.Name, Path: symbol.FilePath, LineStart: symbol.LineNumber, LineEnd: symbol.EndLine, Content: content, Score: .75})
	}
	return evidence, nil
}

type configuredChatProvider struct {
	cfg      config.ServerLLMConfig
	baseURL  string
	client   *http.Client
	sem      chan struct{}
	maxBody  int64
	maxError int64
}

func newConfiguredChatProvider(cfg config.ServerLLMConfig) (agentdomain.CompletionProvider, error) {
	if !cfg.Configured() {
		return nil, nil
	}
	if err := config.ValidateServerLLM(&cfg); err != nil {
		return nil, err
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = extraction.ProviderDefaultBaseURL(extraction.LLMProvider(cfg.Provider))
	}
	endpoint, err := url.Parse(strings.TrimRight(baseURL, "/") + "/chat/completions")
	if err != nil || validateAgentProviderURL(cfg, baseURL, endpoint) != nil {
		return nil, errors.New("server: agent provider destination rejected")
	}
	transport := &http.Transport{DialContext: agentDialContext(cfg, baseURL)}
	if cfg.CACertPool != nil {
		transport.TLSClientConfig = &tls.Config{RootCAs: cfg.CACertPool}
	}
	client := &http.Client{Transport: transport, Timeout: cfg.Timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= cfg.MaxRedirects {
			return errors.New("server: agent provider redirect rejected")
		}
		return validateAgentProviderURL(cfg, baseURL, req.URL)
	}}
	return &configuredChatProvider{cfg: cfg, baseURL: strings.TrimRight(baseURL, "/"), client: client, sem: make(chan struct{}, cfg.MaxConcurrent), maxBody: cfg.MaxResponseBodyBytes, maxError: cfg.MaxErrorBodyBytes}, nil
}

func (p *configuredChatProvider) Complete(ctx context.Context, req agentdomain.CompletionRequest) (agentdomain.CompletionResult, error) {
	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	payload := struct {
		Model       string        `json:"model"`
		Messages    []chatMessage `json:"messages"`
		Temperature float64       `json:"temperature"`
		MaxTokens   int           `json:"max_tokens"`
	}{Model: p.cfg.Model, Temperature: .2, MaxTokens: 1200}
	if payload.Model == "" {
		payload.Model = "gpt-4o-mini"
	}
	providerPolicy := req.SystemPrompt + `
Return ONLY a JSON object with this schema: {"claims":[{"text":"one factual claim","citation_handles":["issued_handle"]}]}.
Split distinct factual statements into distinct claims. Omit any claim that cannot cite an issued handle.`
	payload.Messages = []chatMessage{{Role: "system", Content: providerPolicy}, {Role: "user", Content: req.UserPrompt}}
	body, err := json.Marshal(payload)
	if err != nil {
		return agentdomain.CompletionResult{}, errors.New("server: agent provider request encoding failed")
	}
	endpoint := p.baseURL + "/chat/completions"
	u, _ := url.Parse(endpoint)
	if err := validateAgentProviderURL(p.cfg, p.baseURL, u); err != nil {
		return agentdomain.CompletionResult{}, errors.New("server: agent provider destination rejected")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return agentdomain.CompletionResult{}, errors.New("server: agent provider request construction failed")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-ctx.Done():
		return agentdomain.CompletionResult{}, ctx.Err()
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return agentdomain.CompletionResult{}, errors.New("server: agent provider request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, p.maxError+1))
		return agentdomain.CompletionResult{}, fmt.Errorf("server: agent provider rejected request: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, p.maxBody+1))
	if err != nil || int64(len(data)) > p.maxBody {
		return agentdomain.CompletionResult{}, errors.New("server: agent provider response invalid")
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &envelope) != nil || len(envelope.Choices) == 0 {
		return agentdomain.CompletionResult{}, errors.New("server: agent provider response invalid")
	}
	raw := strings.TrimSpace(envelope.Choices[0].Message.Content)
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(raw, "```json"), "```"), "```"))
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			raw = raw[start : end+1]
		}
	}
	var answer struct {
		Claims []llmClaim `json:"claims"`
	}
	if json.Unmarshal([]byte(raw), &answer) == nil && len(answer.Claims) > 0 {
		domainClaims := make([]agentdomain.CompletionClaim, len(answer.Claims))
		for i, c := range answer.Claims {
			domainClaims[i] = c.toDomain()
		}
		return agentdomain.CompletionResult{Claims: domainClaims, InputTokens: envelope.Usage.Prompt, OutputTokens: envelope.Usage.Completion}, nil
	}
	var singleClaim llmClaim
	if json.Unmarshal([]byte(raw), &singleClaim) == nil && strings.TrimSpace(singleClaim.Text) != "" {
		return agentdomain.CompletionResult{Claims: []agentdomain.CompletionClaim{singleClaim.toDomain()}, InputTokens: envelope.Usage.Prompt, OutputTokens: envelope.Usage.Completion}, nil
	}
	if raw != "" {
		c := llmClaim{Text: raw}
		return agentdomain.CompletionResult{Claims: []agentdomain.CompletionClaim{c.toDomain()}, InputTokens: envelope.Usage.Prompt, OutputTokens: envelope.Usage.Completion}, nil
	}
	return agentdomain.CompletionResult{}, errors.New("server: agent provider response invalid")
}

func (p *configuredChatProvider) Stream(ctx context.Context, req agentdomain.CompletionRequest, emit func(agentdomain.CompletionClaim) error) (agentdomain.CompletionUsage, error) {
	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	payload := struct {
		Model       string        `json:"model"`
		Messages    []chatMessage `json:"messages"`
		Temperature float64       `json:"temperature"`
		MaxTokens   int           `json:"max_tokens"`
		Stream      bool          `json:"stream"`
	}{Model: p.cfg.Model, Temperature: .2, MaxTokens: 1200, Stream: true}
	if payload.Model == "" {
		payload.Model = "gpt-4o-mini"
	}
	providerPolicy := req.SystemPrompt + `
Return ONLY newline-delimited JSON (NDJSON), one complete claim per line, with this schema:
{"text":"one factual claim","citation_handles":["issued_handle"]}
Split distinct factual statements into distinct lines. Omit any claim that cannot cite an issued handle. Do not use markdown fences.`
	payload.Messages = []chatMessage{{Role: "system", Content: providerPolicy}, {Role: "user", Content: req.UserPrompt}}
	body, err := json.Marshal(payload)
	if err != nil {
		return agentdomain.CompletionUsage{}, errors.New("server: agent provider request encoding failed")
	}
	endpoint := p.baseURL + "/chat/completions"
	u, _ := url.Parse(endpoint)
	if err := validateAgentProviderURL(p.cfg, p.baseURL, u); err != nil {
		return agentdomain.CompletionUsage{}, errors.New("server: agent provider destination rejected")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return agentdomain.CompletionUsage{}, errors.New("server: agent provider request construction failed")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if p.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-ctx.Done():
		return agentdomain.CompletionUsage{}, ctx.Err()
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return agentdomain.CompletionUsage{}, errors.New("server: agent provider request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, p.maxError+1))
		return agentdomain.CompletionUsage{}, fmt.Errorf("server: agent provider rejected request: status %d", resp.StatusCode)
	}

	limited := &io.LimitedReader{R: resp.Body, N: p.maxBody + 1}
	scanner := bufio.NewScanner(limited)
	maxToken := int(p.maxBody)
	if int64(maxToken) != p.maxBody || maxToken < 1 {
		maxToken = int(^uint(0) >> 1)
	}
	scanner.Buffer(make([]byte, 4096), maxToken)
	var eventData []string
	var content strings.Builder
	usage := agentdomain.CompletionUsage{}
	claims := 0
	consumeClaims := func(final bool) error {
		for {
			value := content.String()
			at := strings.IndexByte(value, '\n')
			if at < 0 && !final {
				return nil
			}
			var line string
			if at < 0 {
				line = value
				content.Reset()
			} else {
				line = value[:at]
				rest := value[at+1:]
				content.Reset()
				content.WriteString(rest)
			}
			line = strings.TrimSpace(line)
			if line != "" {
				if strings.HasPrefix(line, "```") {
					if at < 0 {
						break
					}
					continue
				}
				var claim llmClaim
				if err := json.Unmarshal([]byte(line), &claim); err == nil && strings.TrimSpace(claim.Text) != "" {
					if err := emit(claim.toDomain()); err != nil {
						return err
					}
					claims++
				} else if start := strings.Index(line, "{"); start >= 0 {
					if end := strings.LastIndex(line, "}"); end > start {
						if err := json.Unmarshal([]byte(line[start:end+1]), &claim); err == nil && strings.TrimSpace(claim.Text) != "" {
							if err := emit(claim.toDomain()); err != nil {
								return err
							}
							claims++
						}
					}
				}
			}
			if at < 0 {
				break
			}
		}
		if final && claims == 0 {
			fullText := strings.TrimSpace(content.String())
			if start := strings.Index(fullText, "{"); start >= 0 {
				if end := strings.LastIndex(fullText, "}"); end > start {
					var answer struct {
						Claims []llmClaim `json:"claims"`
					}
					if err := json.Unmarshal([]byte(fullText[start:end+1]), &answer); err == nil && len(answer.Claims) > 0 {
						for _, cl := range answer.Claims {
							if strings.TrimSpace(cl.Text) != "" {
								if err := emit(cl.toDomain()); err != nil {
									return err
								}
								claims++
							}
						}
					}
				}
			}
			if claims == 0 && fullText != "" {
				raw := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(fullText, "```json"), "```"), "```"))
				if raw != "" {
					cl := llmClaim{Text: raw}.toDomain()
					if err := emit(cl); err == nil {
						claims++
					}
				}
			}
		}
		return nil
	}
	consumeEvent := func() (bool, error) {
		if len(eventData) == 0 {
			return false, nil
		}
		raw := strings.Join(eventData, "\n")
		eventData = eventData[:0]
		if strings.TrimSpace(raw) == "[DONE]" {
			return true, consumeClaims(true)
		}
		var event struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage struct {
				Prompt     int `json:"prompt_tokens"`
				Completion int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(raw), &event) != nil {
			return false, errors.New("server: agent provider stream invalid")
		}
		usage.InputTokens = max(usage.InputTokens, event.Usage.Prompt)
		usage.OutputTokens = max(usage.OutputTokens, event.Usage.Completion)
		for _, choice := range event.Choices {
			content.WriteString(choice.Delta.Content)
			if int64(content.Len()) > p.maxBody {
				return false, errors.New("server: agent provider stream invalid")
			}
			if err := consumeClaims(false); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	done := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			var consumeErr error
			done, consumeErr = consumeEvent()
			if consumeErr != nil {
				return agentdomain.CompletionUsage{}, consumeErr
			}
			if done {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			eventData = append(eventData, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil || limited.N <= 0 {
		return agentdomain.CompletionUsage{}, errors.New("server: agent provider stream invalid")
	}
	if !done {
		var consumeErr error
		done, consumeErr = consumeEvent()
		if consumeErr != nil {
			return agentdomain.CompletionUsage{}, consumeErr
		}
		if !done {
			if err := consumeClaims(true); err != nil {
				return agentdomain.CompletionUsage{}, err
			}
		}
	}
	if claims == 0 {
		if err := consumeClaims(true); err != nil {
			return agentdomain.CompletionUsage{}, err
		}
	}
	if claims == 0 && content.Len() > 0 {
		raw := strings.TrimSpace(content.String())
		raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(raw, "```json"), "```"), "```"))
		if raw != "" {
			if err := emit(agentdomain.CompletionClaim{Text: raw}); err == nil {
				claims++
			}
		}
	}
	if claims == 0 {
		return agentdomain.CompletionUsage{}, errors.New("server: agent provider stream invalid")
	}
	return usage, nil
}

func validateAgentProviderURL(cfg config.ServerLLMConfig, baseURL string, target *url.URL) error {
	if target == nil || target.Host == "" || target.User != nil {
		return errors.New("server: agent provider destination rejected")
	}
	scheme := strings.ToLower(target.Scheme)
	if scheme != "https" && (scheme != "http" || !cfg.AllowLoopbackHTTP || !transportpolicy.IsStrictLoopbackHost(target.Hostname())) {
		return errors.New("server: agent provider destination rejected")
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return errors.New("server: agent provider destination rejected")
	}
	host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	hosts := map[string]bool{strings.ToLower(strings.TrimSuffix(base.Hostname(), ".")): true}
	for _, allowed := range cfg.AllowedHosts {
		hosts[strings.ToLower(strings.TrimSuffix(allowed, "."))] = true
	}
	if !hosts[host] {
		return errors.New("server: agent provider destination rejected")
	}
	port := target.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	ports := map[string]bool{"443": true}
	basePort := base.Port()
	if basePort == "" {
		if base.Scheme == "https" {
			basePort = "443"
		} else {
			basePort = "80"
		}
	}
	ports[basePort] = true
	for _, allowed := range cfg.AllowedPorts {
		ports[fmt.Sprint(allowed)] = true
	}
	if !ports[port] {
		return errors.New("server: agent provider destination rejected")
	}
	if ip := net.ParseIP(host); ip != nil && !agentIPApproved(ip, cfg.AllowLoopback) {
		return errors.New("server: agent provider destination rejected")
	}
	return nil
}

func agentDialContext(cfg config.ServerLLMConfig, baseURL string) func(context.Context, string, string) (net.Conn, error) {
	ports := map[string]bool{"443": true}
	for _, port := range cfg.AllowedPorts {
		ports[fmt.Sprint(port)] = true
	}
	if parsed, err := url.Parse(baseURL); err == nil {
		port := parsed.Port()
		if port == "" && parsed.Scheme == "http" {
			port = "80"
		}
		if port != "" {
			ports[port] = true
		}
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !ports[port] {
			return nil, errors.New("server: agent provider dial rejected")
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, errors.New("server: agent provider resolution failed")
		}
		for _, ip := range ips {
			if agentIPApproved(ip, cfg.AllowLoopback) {
				return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			}
		}
		return nil, errors.New("server: agent provider dial rejected")
	}
}

func agentIPApproved(ip net.IP, allowLoopback bool) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.Equal(net.IPv4bcast) {
		return false
	}
	if ip.IsLoopback() {
		return allowLoopback
	}
	if ip.IsPrivate() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false
	}
	return ip.IsGlobalUnicast()
}
