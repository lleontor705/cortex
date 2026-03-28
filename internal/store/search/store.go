// Package search implements the SQLite search store for Cortex.
//
// It provides FTS5-based full-text search with BM25 ranking, topic key
// direct lookup, RRF fusion for hybrid search, and snippet extraction.
// The store implements the domain.SearchRepository interface.
package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// Store implements the SQLite search store.
// It provides FTS5-based full-text search with advanced features.
type Store struct {
	db *sql.DB
}

// NewStore creates a new search store with the given database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Search performs a full-text search with the given query and options.
// It supports:
//   - FTS5 keyword search with BM25 ranking
//   - Topic key direct lookup (queries containing '/')
//   - RRF fusion for combining topic key and keyword results
//   - Snippet extraction using FTS5 snippet() function
//   - Column weighting (content 2x title)
//
// Returns search results ordered by relevance (rank).
func (s *Store) Search(ctx context.Context, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
	// Validate query
	if strings.TrimSpace(query) == "" {
		return []*domain.SearchResult{}, nil
	}

	// Apply default limit
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	// Check if this is a topic key lookup (contains '/')
	if strings.Contains(query, "/") {
		return s.searchWithTopicKey(ctx, query, opts, limit)
	}

	// Standard FTS5 keyword search
	return s.searchKeywords(ctx, query, opts, limit)
}

// searchWithTopicKey handles searches that include topic keys.
// It uses RRF fusion to combine topic key direct lookup with keyword search.
func (s *Store) searchWithTopicKey(ctx context.Context, query string, opts domain.SearchOptions, limit int) ([]*domain.SearchResult, error) {
	// Get direct topic key matches
	topicResults, err := s.lookupByTopicKey(ctx, query, opts, limit)
	if err != nil {
		return nil, fmt.Errorf("search store: topic key lookup: %w", err)
	}

	// Get FTS5 keyword matches
	keywordResults, err := s.searchKeywords(ctx, query, opts, limit)
	if err != nil {
		return nil, fmt.Errorf("search store: keyword search: %w", err)
	}

	// Combine results using RRF fusion
	return s.combineWithRRF(topicResults, keywordResults, limit), nil
}

// lookupByTopicKey performs a direct lookup by topic key.
// This returns observations where the topic_key matches the query exactly.
func (s *Store) lookupByTopicKey(ctx context.Context, query string, opts domain.SearchOptions, limit int) ([]*domain.SearchResult, error) {
	baseQuery := `
		SELECT 
			o.id, o.title, o.content, o.type, o.project, o.scope, o.session_id,
			o.topic_key, o.confidence, o.source, o.tags, o.created_at, o.updated_at
		FROM observations o
		WHERE o.topic_key = ? AND o.deleted_at IS NULL
	`
	args := []any{query}

	// Apply filters
	if opts.Type != "" {
		baseQuery += " AND o.type = ?"
		args = append(args, opts.Type)
	}
	if opts.Project != "" {
		baseQuery += " AND o.project = ?"
		args = append(args, opts.Project)
	}
	if opts.Scope != "" {
		baseQuery += " AND o.scope = ?"
		args = append(args, normalizeScope(opts.Scope))
	}

	baseQuery += " ORDER BY o.updated_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("topic key lookup: %w", err)
	}
	defer rows.Close()

	var results []*domain.SearchResult
	for rows.Next() {
		result, err := s.scanSearchResult(rows, -1000) // High priority for exact topic match
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("topic key lookup: iterate rows: %w", err)
	}

	return results, nil
}

// searchKeywords performs FTS5 keyword search with BM25 ranking.
// It uses column weighting where content has 2x weight compared to title.
func (s *Store) searchKeywords(ctx context.Context, query string, opts domain.SearchOptions, limit int) ([]*domain.SearchResult, error) {
	// Sanitize query for FTS5
	ftsQuery := sanitizeFTS(query)

	// Build the search query with BM25 ranking and column weighting
	// BM25 weights: content=2.0 (higher priority), title=1.0, others=0.5
	// The bm25() function with weights: (table, col1_weight, col2_weight, ...)
	// Column order in FTS5: title, content, tool_name, type, project, scope, topic_key
	baseQuery := `
		SELECT 
			o.id, o.title, o.content, o.type, o.project, o.scope, o.session_id,
			o.topic_key, o.confidence, o.source, o.tags, o.created_at, o.updated_at,
			bm25(observations_fts, 1.0, 2.0, 0.5, 0.5, 0.5, 0.5, 0.5) as rank
		FROM observations_fts fts
		JOIN observations o ON o.id = fts.rowid
		WHERE observations_fts MATCH ? AND o.deleted_at IS NULL
	`
	args := []any{ftsQuery}

	// Apply filters
	if opts.Type != "" {
		baseQuery += " AND o.type = ?"
		args = append(args, opts.Type)
	}
	if opts.Project != "" {
		baseQuery += " AND o.project = ?"
		args = append(args, opts.Project)
	}
	if opts.Scope != "" {
		baseQuery += " AND o.scope = ?"
		args = append(args, normalizeScope(opts.Scope))
	}

	baseQuery += " ORDER BY rank LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	defer rows.Close()

	var results []*domain.SearchResult
	for rows.Next() {
		var rank float64
		result, err := s.scanSearchResultWithRank(rows, &rank)
		if err != nil {
			return nil, err
		}
		result.Rank = rank
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("keyword search: iterate rows: %w", err)
	}

	return results, nil
}

// GetSnippet extracts a snippet from the content that matches the query.
// It uses FTS5's snippet() function to highlight matching terms.
func (s *Store) GetSnippet(ctx context.Context, query string, content string, maxLength int) (string, error) {
	if maxLength <= 0 {
		maxLength = 200
	}

	// Sanitize query for FTS5
	ftsQuery := sanitizeFTS(query)

	// Use FTS5 snippet function to extract relevant text
	// snippet(table, column, start_marker, end_marker, ellipsis, max_tokens)
	snippetQuery := `
		SELECT snippet(observations_fts, 1, '...', '...', '...', ?)
		FROM observations_fts
		WHERE observations_fts MATCH ?
		LIMIT 1
	`

	var snippet string
	maxTokens := maxLength / 10 // Approximate token count
	err := s.db.QueryRowContext(ctx, snippetQuery, maxTokens, ftsQuery).Scan(&snippet)
	if err != nil {
		// Fallback to simple truncation if FTS5 snippet fails
		if len(content) > maxLength {
			return content[:maxLength] + "...", nil
		}
		return content, nil
	}

	return snippet, nil
}

// combineWithRRF combines two result sets using Reciprocal Rank Fusion.
// RRF formula: score = sum(1 / (k + rank)) for each result list
// where k is a constant (default 60).
func (s *Store) combineWithRRF(topicResults, keywordResults []*domain.SearchResult, limit int) []*domain.SearchResult {
	const k = 60.0 // RRF constant

	// Track scores and observations by ID
	scores := make(map[int64]float64)
	observations := make(map[int64]*domain.SearchResult)

	// Score topic key results (higher priority due to exact match)
	for rank, result := range topicResults {
		id := result.ID
		scores[id] += 1.0 / (k + float64(rank+1))
		observations[id] = result
	}

	// Score keyword results
	for rank, result := range keywordResults {
		id := result.ID
		scores[id] += 1.0 / (k + float64(rank+1))
		if _, exists := observations[id]; !exists {
			observations[id] = result
		}
	}

	// Build combined results sorted by RRF score
	combined := make([]*domain.SearchResult, 0, len(observations))
	for id, result := range observations {
		result.Rank = scores[id]
		combined = append(combined, result)
	}

	// Sort by RRF score (descending)
	sortByRRFScore(combined)

	// Limit results
	if len(combined) > limit {
		combined = combined[:limit]
	}

	return combined
}

// sortByRRFScore sorts search results by RRF score in descending order.
func sortByRRFScore(results []*domain.SearchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Rank > results[j].Rank
	})
}

// scanSearchResult scans a row into a SearchResult with a fixed rank.
func (s *Store) scanSearchResult(rows *sql.Rows, rank float64) (*domain.SearchResult, error) {
	var result domain.SearchResult
	var createdAtStr, updatedAtStr string
	var topicKey, source, tagsJSON sql.NullString

	err := rows.Scan(
		&result.ID, &result.Title, &result.Content, &result.Type,
		&result.Project, &result.Scope, &result.SessionID, &topicKey,
		&result.Confidence, &source, &tagsJSON,
		&createdAtStr, &updatedAtStr,
	)
	if err != nil {
		return nil, fmt.Errorf("search store: scan result: %w", err)
	}

	if topicKey.Valid {
		result.TopicKey = topicKey.String
	}
	result.Source = source.String
	if tagsJSON.Valid {
		json.Unmarshal([]byte(tagsJSON.String), &result.Tags)
	}
	result.CreatedAt = parseSearchTime(createdAtStr)
	result.UpdatedAt = parseSearchTime(updatedAtStr)
	result.Rank = rank

	return &result, nil
}

// scanSearchResultWithRank scans a row into a SearchResult including the rank.
func (s *Store) scanSearchResultWithRank(rows *sql.Rows, rank *float64) (*domain.SearchResult, error) {
	var result domain.SearchResult
	var createdAtStr, updatedAtStr string
	var topicKey, source, tagsJSON sql.NullString

	err := rows.Scan(
		&result.ID, &result.Title, &result.Content, &result.Type,
		&result.Project, &result.Scope, &result.SessionID, &topicKey,
		&result.Confidence, &source, &tagsJSON,
		&createdAtStr, &updatedAtStr, rank,
	)
	if err != nil {
		return nil, fmt.Errorf("search store: scan result with rank: %w", err)
	}

	if topicKey.Valid {
		result.TopicKey = topicKey.String
	}
	result.Source = source.String
	if tagsJSON.Valid {
		json.Unmarshal([]byte(tagsJSON.String), &result.Tags)
	}
	result.CreatedAt = parseSearchTime(createdAtStr)
	result.UpdatedAt = parseSearchTime(updatedAtStr)

	return &result, nil
}

// sanitizeFTS sanitizes a query string for FTS5 full-text search.
// It wraps each term in double quotes to prevent FTS5 syntax errors
// from special characters, and adds prefix matching for the last term.
func sanitizeFTS(query string) string {
	// Trim whitespace
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}

	// Remove FTS5 special operators that could cause syntax errors
	// These characters have special meaning in FTS5: * ^ ~ + - ( )
	replacer := strings.NewReplacer(
		"*", "",
		"^", "",
		"~", "",
		"+", "",
		"-", " ",
		"(", "",
		")", "",
	)
	query = replacer.Replace(query)

	// Escape double quotes by replacing them with single quotes
	query = strings.ReplaceAll(query, `"`, `'`)

	// Split into terms
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return ""
	}

	// Wrap each term in double quotes
	for i, term := range terms {
		// Add prefix matching (*) to the last term
		// This allows partial matches like "auth" matching "authentication"
		if i == len(terms)-1 {
			terms[i] = fmt.Sprintf(`"%s*"`, term)
		} else {
			terms[i] = fmt.Sprintf(`"%s"`, term)
		}
	}

	// Join with AND operator
	return strings.Join(terms, " AND ")
}

// normalizeScope normalizes scope values to standard format.
func normalizeScope(scope string) string {
	switch strings.ToLower(scope) {
	case "personal":
		return domain.ScopePersonal
	case "project":
		return domain.ScopeProject
	default:
		return scope
	}
}

// parseSearchTime parses a time string, logging a warning if it fails.
func parseSearchTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	if s != "" {
		log.Printf("search: failed to parse time %q", s)
	}
	return time.Time{}
}

// Ensure Store implements domain.SearchRepository
var _ domain.SearchRepository = (*Store)(nil)
