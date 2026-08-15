// Package extraction provides automated extraction and synthesis of observations,
// entities, and knowledge-graph relationships from raw text, code notes, and session logs.
package extraction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
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
}

// NewService creates a new extraction service.
func NewService(cfg Config) *Service {
	if cfg.Timeout == 0 {
		cfg.Timeout = 45 * time.Second
	}
	return &Service{
		httpClient: &http.Client{Timeout: cfg.Timeout},
		defaultCfg: cfg,
	}
}

// Extract processes raw text and extracts structured observations and graph edges.
func (s *Service) Extract(ctx context.Context, req ExtractionRequest) (*ExtractionResult, error) {
	if strings.TrimSpace(req.Text) == "" {
		return nil, errors.New("extraction: empty text payload")
	}

	cfg := s.defaultCfg
	if req.LLMConfig != nil && req.LLMConfig.APIKey != "" {
		cfg = *req.LLMConfig
	}

	if cfg.APIKey != "" || cfg.BaseURL != "" {
		res, err := s.extractWithLLM(ctx, req, cfg)
		if err == nil && len(res.Observations) > 0 {
			res.SourceMethod = "llm"
			res.ExtractedAt = time.Now().UTC()
			return res, nil
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

	cfg := s.defaultCfg
	if req.LLMConfig != nil && req.LLMConfig.APIKey != "" {
		cfg = *req.LLMConfig
	}

	if cfg.APIKey != "" || cfg.BaseURL != "" {
		res, err := s.synthesizeWithLLM(ctx, req, cfg)
		if err == nil {
			res.SynthesizedAt = time.Now().UTC()
			return res, nil
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
	baseURL := cfg.BaseURL
	if baseURL == "" {
		if cfg.Provider == ProviderAnthropic {
			baseURL = "https://api.anthropic.com/v1"
		} else {
			baseURL = "https://api.openai.com/v1"
		}
	}

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

	bodyJSON, err := json.Marshal(openAIReq{
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(baseURL, "/")), bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.APIKey))
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	// The body is fully consumed below (ReadAll on error, Decode on success),
	// so a Close failure carries no additional signal for the caller.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llm request failed with status %d: %s", resp.StatusCode, string(respBytes))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}

	if len(chatResp.Choices) == 0 {
		return nil, errors.New("no choices returned by LLM")
	}

	rawContent := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	rawContent = strings.TrimPrefix(rawContent, "```json")
	rawContent = strings.TrimPrefix(rawContent, "```")
	rawContent = strings.TrimSuffix(rawContent, "```")
	rawContent = strings.TrimSpace(rawContent)

	var result ExtractionResult
	if err := json.Unmarshal([]byte(rawContent), &result); err != nil {
		return nil, fmt.Errorf("failed to parse LLM JSON: %w", err)
	}

	return &result, nil
}

func (s *Service) synthesizeWithLLM(ctx context.Context, req SynthesisRequest, cfg Config) (*SynthesisResult, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
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

	bodyJSON, _ := json.Marshal(openAIReq{
		Model: model,
		Messages: []openAIMessage{
			{Role: "system", Content: "You synthesize architecture notes. Return strictly JSON."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.2,
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(baseURL, "/")), bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.APIKey))
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	// The body is fully consumed below via Decode, so a Close failure carries
	// no additional signal for the caller.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("synthesis LLM request failed with status %d", resp.StatusCode)
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}
	if len(chatResp.Choices) == 0 {
		return nil, errors.New("empty LLM response")
	}

	rawContent := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	rawContent = strings.TrimPrefix(rawContent, "```json")
	rawContent = strings.TrimPrefix(rawContent, "```")
	rawContent = strings.TrimSuffix(rawContent, "```")
	rawContent = strings.TrimSpace(rawContent)

	var result SynthesisResult
	if err := json.Unmarshal([]byte(rawContent), &result); err != nil {
		return nil, err
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
