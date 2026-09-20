package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/transportpolicy"
)

// RemoteSearchClient performs hybrid search against a remote Cortex server API.
type RemoteSearchClient struct {
	baseURL string
	token   string
	client  *http.Client
}

// Compile-time interface assertion.
var _ domain.RemoteSearcher = (*RemoteSearchClient)(nil)

// NewRemoteSearchClient constructs a validated RemoteSearchClient.
// It verifies that baseURL complies with transportpolicy.ValidateBearerDestination
// and configures an http.Client with transportpolicy.CheckBearerRedirect.
func NewRemoteSearchClient(baseURL, token string, timeout time.Duration) (*RemoteSearchClient, error) {
	trimmed := strings.TrimRight(baseURL, "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return nil, errors.New("remote search: invalid server URL")
	}

	// Enforce the Bearer transport security policy (REM-TRANSPORT-001) before
	// any request is issued: HTTPS required for non-loopback, plain HTTP
	// permitted only on strict loopback.
	if err := transportpolicy.ValidateBearerDestination(u.String()); err != nil {
		return nil, fmt.Errorf("remote search: %w", err)
	}

	if strings.TrimSpace(token) == "" {
		return nil, errors.New("remote search: token is required")
	}

	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	return &RemoteSearchClient{
		baseURL: u.String(),
		token:   token,
		client: &http.Client{
			Timeout:       timeout,
			CheckRedirect: transportpolicy.CheckBearerRedirect,
		},
	}, nil
}

type rawRemoteSearchResult struct {
	ID             any                         `json:"id"`
	Title          string                      `json:"title"`
	Content        string                      `json:"content"`
	Type           string                      `json:"type"`
	Project        string                      `json:"project"`
	Scope          string                      `json:"scope"`
	OwnerSubject   string                      `json:"owner_subject,omitempty"`
	SessionID      string                      `json:"session_id,omitempty"`
	TopicKey       string                      `json:"topic_key,omitempty"`
	Confidence     float64                     `json:"confidence"`
	Source         string                      `json:"source,omitempty"`
	Tags           []string                    `json:"tags,omitempty"`
	CreatedAt      any                         `json:"created_at,omitempty"`
	UpdatedAt      any                         `json:"updated_at,omitempty"`
	Rank           float64                     `json:"rank"`
	ScoreBreakdown domain.SearchScoreBreakdown `json:"score_breakdown,omitempty"`
	SearchID       domain.SearchID             `json:"search_id,omitempty"`
	NextCursor     string                      `json:"next_cursor,omitempty"`
}

func (r rawRemoteSearchResult) toDomain() *domain.SearchResult {
	sr := &domain.SearchResult{
		Observation: domain.Observation{
			Title:        r.Title,
			Content:      r.Content,
			Type:         r.Type,
			Project:      r.Project,
			Scope:        r.Scope,
			OwnerSubject: r.OwnerSubject,
			SessionID:    r.SessionID,
			TopicKey:     r.TopicKey,
			Confidence:   r.Confidence,
			Source:       r.Source,
			Tags:         r.Tags,
			CreatedAt:    parseTimeVal(r.CreatedAt),
			UpdatedAt:    parseTimeVal(r.UpdatedAt),
		},
		Rank:           r.Rank,
		ScoreBreakdown: r.ScoreBreakdown,
		SearchID:       r.SearchID,
		NextCursor:     r.NextCursor,
	}

	if sr.Confidence <= 0 {
		sr.Confidence = 1.0
	}

	switch v := r.ID.(type) {
	case float64:
		sr.ID = int64(v)
	case int64:
		sr.ID = v
	case int:
		sr.ID = int64(v)
	case string:
		sr.PublicID = v
		if parsedInt, err := strconv.ParseInt(v, 10, 64); err == nil {
			sr.ID = parsedInt
		}
	case json.Number:
		if i, err := v.Int64(); err == nil {
			sr.ID = i
		}
	}

	return sr
}

func parseTimeVal(v any) time.Time {
	if v == nil {
		return time.Time{}
	}
	if s, ok := v.(string); ok {
		return parseSyncTime(s)
	}
	return time.Time{}
}

// SearchHybrid executes a remote hybrid search query against /api/search/hybrid.
func (c *RemoteSearchClient) SearchHybrid(ctx context.Context, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("remote search: client is not initialized")
	}

	q := query
	if q == "" {
		q = opts.Query
	}

	params := url.Values{}
	params.Set("q", q)
	if opts.Project != "" {
		params.Set("project", opts.Project)
	}
	if opts.Type != "" {
		params.Set("type", opts.Type)
	}
	if opts.Scope != "" {
		params.Set("scope", opts.Scope)
	}
	if opts.Limit > 0 {
		params.Set("limit", strconv.Itoa(opts.Limit))
	}

	reqURL := fmt.Sprintf("%s/api/search/hybrid?%s", c.baseURL, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("remote search: create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote search: request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("remote search: server returned %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
	}

	var rawList []rawRemoteSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&rawList); err != nil {
		return nil, fmt.Errorf("remote search: decode response: %w", err)
	}

	results := make([]*domain.SearchResult, 0, len(rawList))
	for _, raw := range rawList {
		results = append(results, raw.toDomain())
	}

	return results, nil
}
