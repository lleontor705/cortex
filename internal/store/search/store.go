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
	"math"
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

	// Enhanced keyword search with recency, importance, and topic key expansion
	return s.searchEnhanced(ctx, query, opts, limit)
}

// searchWithTopicKey handles searches that include topic keys.
// It uses RRF fusion to combine topic key direct lookup with keyword search.
func (s *Store) searchWithTopicKey(ctx context.Context, query string, opts domain.SearchOptions, limit int) ([]*domain.SearchResult, error) {
	// Get direct topic key matches (highest priority)
	topicResults, err := s.lookupByTopicKey(ctx, query, opts, limit)
	if err != nil {
		return nil, fmt.Errorf("search store: topic key lookup: %w", err)
	}

	// Get FTS5 keyword matches with recency boost
	keywordResults, err := s.searchKeywords(ctx, query, opts, limit*3)
	if err != nil {
		return nil, fmt.Errorf("search store: keyword search: %w", err)
	}
	s.applyRecencyBoost(ctx, keywordResults)

	// Get importance ranking
	allIDs := collectIDs(topicResults, keywordResults)
	importanceResults := s.getImportanceRanking(ctx, allIDs)

	fusionK := opts.FusionK
	if fusionK <= 0 {
		fusionK = 60.0
	}
	return s.combineAllSignals(topicResults, keywordResults, nil, nil, importanceResults, fusionK, limit), nil
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
	defer func() { _ = rows.Close() }()

	var results []*domain.SearchResult
	for rows.Next() {
		result, err := s.scanSearchResult(rows, -1000) // High priority for exact topic match
		if err != nil {
			return nil, err
		}
		result.ScoreBreakdown = domain.SearchScoreBreakdown{
			Strategy:      "topic_key",
			TopicKeyExact: true,
		}
		result.Content = previewContent(query, result.Content, 300)
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
	defer func() { _ = rows.Close() }()

	var results []*domain.SearchResult
	for rows.Next() {
		var rank float64
		result, err := s.scanSearchResultWithRank(rows, &rank)
		if err != nil {
			return nil, err
		}
		result.Rank = rank
		result.ScoreBreakdown = domain.SearchScoreBreakdown{
			Strategy:    "keyword",
			KeywordBM25: rank,
		}
		result.Content = previewContent(query, result.Content, 300)
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
		_ = json.Unmarshal([]byte(tagsJSON.String), &result.Tags)
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
		_ = json.Unmarshal([]byte(tagsJSON.String), &result.Tags)
	}
	result.CreatedAt = parseSearchTime(createdAtStr)
	result.UpdatedAt = parseSearchTime(updatedAtStr)

	return &result, nil
}

var ftsSanitizeReplacer = strings.NewReplacer(
	"*", "",
	"^", "",
	"~", "",
	"+", "",
	"-", " ",
	"(", "",
	")", "",
)

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
	// Performance: Use package-level replacer to avoid allocation on every call
	query = ftsSanitizeReplacer.Replace(query)

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

func previewContent(query, content string, maxLength int) string {
	if maxLength <= 0 || len(content) <= maxLength {
		return content
	}

	terms := strings.Fields(strings.ToLower(query))
	lowerContent := strings.ToLower(content)
	matchIdx := -1
	for _, term := range terms {
		term = strings.Trim(term, `"'*/`)
		if term == "" {
			continue
		}
		if idx := strings.Index(lowerContent, term); idx >= 0 {
			matchIdx = idx
			break
		}
	}

	if matchIdx < 0 {
		return content[:maxLength] + "..."
	}

	start := matchIdx - (maxLength / 3)
	if start < 0 {
		start = 0
	}
	end := start + maxLength
	if end > len(content) {
		end = len(content)
		if end > maxLength {
			start = end - maxLength
		}
	}

	snippet := content[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(content) {
		snippet += "..."
	}
	return snippet
}

// --- Enhanced Search Pipeline ---

// searchEnhanced combines FTS5 keyword search with recency decay, importance
// ranking, topic key expansion, graph neighborhood expansion, and pseudo-relevance
// feedback using multi-signal RRF fusion.
func (s *Store) searchEnhanced(ctx context.Context, query string, opts domain.SearchOptions, limit int) ([]*domain.SearchResult, error) {
	// 1. Get FTS5 keyword results (fetch extra candidates for re-ranking)
	keywordResults, err := s.searchKeywords(ctx, query, opts, limit*3)
	if err != nil {
		return nil, err
	}

	// 2. Pseudo-Relevance Feedback: extract distinctive terms from top results
	//    and run a second FTS5 query to improve recall.
	prfResults := s.pseudoRelevanceFeedback(ctx, query, keywordResults, opts, limit)

	// 3. Get topic key expansion results (LIKE matching)
	topicExpResults, err := s.searchByTopicKeyExpansion(ctx, query, opts, limit)
	if err != nil {
		log.Printf("search: topic key expansion error (non-fatal): %v", err)
		topicExpResults = nil
	}

	// 4. Apply recency boost to keyword results
	s.applyRecencyBoost(ctx, keywordResults)

	// 5. Graph neighborhood expansion: boost observations connected to top results
	var graphResults []*domain.SearchResult
	if opts.GraphExpand && len(keywordResults) > 0 {
		graphResults = s.graphNeighborExpansion(ctx, keywordResults, opts, limit)
	}

	// 6. Get importance ranking for all candidate IDs
	allIDs := collectIDs(keywordResults, topicExpResults, prfResults, graphResults)
	importanceResults := s.getImportanceRanking(ctx, allIDs)

	// 7. Combine all signals via RRF
	fusionK := opts.FusionK
	if fusionK <= 0 {
		fusionK = 60.0
	}
	combined := s.combineAllSignals(keywordResults, topicExpResults, prfResults, graphResults, importanceResults, fusionK, limit)

	return combined, nil
}

// applyRecencyBoost adjusts BM25 rank by recency decay (Stanford Generative Agents).
// Formula: adjustedRank = bm25Rank * (1 + 0.995^hoursSinceAccess)
func (s *Store) applyRecencyBoost(ctx context.Context, results []*domain.SearchResult) {
	if len(results) == 0 {
		return
	}

	// Collect IDs for batch query
	placeholders := make([]string, len(results))
	args := make([]any, len(results))
	idIndex := make(map[int64]int)
	for i, r := range results {
		placeholders[i] = "?"
		args[i] = r.ID
		idIndex[r.ID] = i
	}

	query := fmt.Sprintf(`
		SELECT observation_id, last_accessed
		FROM importance_scores
		WHERE observation_id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return // non-fatal: skip recency boost
	}
	defer func() { _ = rows.Close() }()

	now := time.Now()
	for rows.Next() {
		var obsID int64
		var lastAccessed sql.NullString
		if err := rows.Scan(&obsID, &lastAccessed); err != nil {
			continue
		}
		idx, ok := idIndex[obsID]
		if !ok {
			continue
		}

		var hoursSince float64
		if lastAccessed.Valid {
			t := parseSearchTime(lastAccessed.String)
			if !t.IsZero() {
				hoursSince = now.Sub(t).Hours()
			}
		}
		if hoursSince < 0 {
			hoursSince = 0
		}

		// Decay: 0.995^hours -- recently accessed = ~1.0, 30 days ago = ~0.03
		recency := math.Pow(0.995, hoursSince)
		results[idx].Rank *= (1.0 + recency)
		results[idx].ScoreBreakdown.RecencyBoost = recency
	}

	// Re-sort by adjusted rank
	sort.Slice(results, func(i, j int) bool {
		return results[i].Rank < results[j].Rank // BM25 ranks are negative (lower = better)
	})
}

// searchByTopicKeyExpansion finds observations whose topic_key contains query terms.
func (s *Store) searchByTopicKeyExpansion(ctx context.Context, query string, opts domain.SearchOptions, limit int) ([]*domain.SearchResult, error) {
	terms := extractSearchTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}

	// Build LIKE conditions for each term
	conditions := make([]string, 0, len(terms))
	args := make([]any, 0)
	for _, term := range terms {
		if len(term) < 3 {
			continue // skip very short terms to avoid noise
		}
		conditions = append(conditions, "o.topic_key LIKE ?")
		args = append(args, "%"+term+"%")
	}
	if len(conditions) == 0 {
		return nil, nil
	}

	baseQuery := fmt.Sprintf(`
		SELECT o.id, o.title, o.content, o.type, o.project, o.scope, o.session_id,
		       o.topic_key, o.confidence, o.source, o.tags, o.created_at, o.updated_at
		FROM observations o
		WHERE (%s) AND o.topic_key IS NOT NULL AND o.topic_key != '' AND o.deleted_at IS NULL
	`, strings.Join(conditions, " OR "))

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
		return nil, fmt.Errorf("topic key expansion: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []*domain.SearchResult
	for rows.Next() {
		result, err := s.scanSearchResult(rows, -500) // Priority between topic exact (-1000) and keyword
		if err != nil {
			return nil, err
		}
		result.ScoreBreakdown = domain.SearchScoreBreakdown{
			Strategy:       "topic_key_expansion",
			TopicKeyExpand: true,
		}
		result.Content = previewContent(query, result.Content, 300)
		results = append(results, result)
	}

	return results, rows.Err()
}

// getImportanceRanking returns search results ordered by importance score.
func (s *Store) getImportanceRanking(ctx context.Context, ids []int64) []*domain.SearchResult {
	if len(ids) == 0 {
		return nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT s.observation_id, s.score
		FROM importance_scores s
		WHERE s.observation_id IN (%s)
		ORDER BY s.score DESC
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil // non-fatal
	}
	defer func() { _ = rows.Close() }()

	var results []*domain.SearchResult
	for rows.Next() {
		var obsID int64
		var score float64
		if err := rows.Scan(&obsID, &score); err != nil {
			continue
		}
		results = append(results, &domain.SearchResult{
			Observation: domain.Observation{ID: obsID},
			Rank:        score,
			ScoreBreakdown: domain.SearchScoreBreakdown{
				ImportanceRank: score,
			},
		})
	}
	return results
}

// --- Pseudo-Relevance Feedback (PRF) ---

// pseudoRelevanceFeedback extracts distinctive terms from top-3 results and
// runs a second FTS5 query to improve recall. This finds related observations
// that share vocabulary with relevant results but not the original query.
func (s *Store) pseudoRelevanceFeedback(ctx context.Context, originalQuery string, topResults []*domain.SearchResult, opts domain.SearchOptions, limit int) []*domain.SearchResult {
	if len(topResults) < 2 {
		return nil
	}

	// Extract terms from top 3 results (title + first 200 chars of content)
	topN := topResults
	if len(topN) > 3 {
		topN = topN[:3]
	}

	originalTerms := make(map[string]bool)
	for _, t := range extractSearchTerms(originalQuery) {
		originalTerms[t] = true
	}

	termFreq := make(map[string]int)
	for _, r := range topN {
		text := r.Title + " " + r.Content
		if len(text) > 300 {
			text = text[:300]
		}
		for _, t := range extractSearchTerms(text) {
			if !originalTerms[t] && len(t) >= 3 {
				termFreq[t]++
			}
		}
	}

	// Pick top 3 most frequent new terms
	type termCount struct {
		term  string
		count int
	}
	var ranked []termCount
	for t, c := range termFreq {
		if c >= 2 { // term must appear in at least 2 of top-3 results
			ranked = append(ranked, termCount{t, c})
		}
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].count > ranked[j].count })

	if len(ranked) == 0 {
		return nil
	}
	if len(ranked) > 3 {
		ranked = ranked[:3]
	}

	// Build expanded query: original terms + top PRF terms
	expandedTerms := append([]string{}, extractSearchTerms(originalQuery)...)
	for _, tc := range ranked {
		expandedTerms = append(expandedTerms, tc.term)
	}
	expandedQuery := strings.Join(expandedTerms, " ")

	// Run second FTS5 query with expanded terms
	prfResults, err := s.searchKeywords(ctx, expandedQuery, opts, limit)
	if err != nil {
		return nil
	}

	// Remove results already in top results to avoid duplicates
	existing := make(map[int64]bool)
	for _, r := range topResults {
		existing[r.ID] = true
	}
	var newResults []*domain.SearchResult
	for _, r := range prfResults {
		if !existing[r.ID] {
			newResults = append(newResults, r)
		}
	}
	return newResults
}

// --- Graph Neighborhood Expansion ---

// graphNeighborExpansion finds observations connected to top search results
// via knowledge graph edges (1-hop). Based on Microsoft GraphRAG concept.
func (s *Store) graphNeighborExpansion(ctx context.Context, topResults []*domain.SearchResult, opts domain.SearchOptions, limit int) []*domain.SearchResult {
	if len(topResults) == 0 {
		return nil
	}

	// Take top 5 results for graph expansion
	topN := topResults
	if len(topN) > 5 {
		topN = topN[:5]
	}

	// Collect neighbor IDs via 1-hop edges
	existing := make(map[int64]bool)
	for _, r := range topResults {
		existing[r.ID] = true
	}

	var neighborIDs []int64
	for _, r := range topN {
		// Query edges in both directions
		rows, err := s.db.QueryContext(ctx, `
			SELECT DISTINCT CASE
				WHEN from_obs_id = ? THEN to_obs_id
				ELSE from_obs_id
			END as neighbor_id
			FROM edges
			WHERE (from_obs_id = ? OR to_obs_id = ?)
			LIMIT 10
		`, r.ID, r.ID, r.ID)
		if err != nil {
			continue
		}
		for rows.Next() {
			var nID int64
			if err := rows.Scan(&nID); err == nil && !existing[nID] {
				neighborIDs = append(neighborIDs, nID)
				existing[nID] = true
			}
		}
		_ = rows.Close()
	}

	if len(neighborIDs) == 0 {
		return nil
	}

	// Fetch neighbor observations
	placeholders := make([]string, len(neighborIDs))
	args := make([]any, len(neighborIDs))
	for i, id := range neighborIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	// Apply project filter if set
	projectFilter := ""
	if opts.Project != "" {
		projectFilter = " AND o.project = ?"
		args = append(args, opts.Project)
	}

	query := fmt.Sprintf(`
		SELECT o.id, o.title, o.content, o.type, o.project, o.scope, o.session_id,
		       o.topic_key, o.confidence, o.source, o.tags, o.created_at, o.updated_at
		FROM observations o
		WHERE o.id IN (%s) AND o.deleted_at IS NULL%s
		ORDER BY o.updated_at DESC
		LIMIT ?
	`, strings.Join(placeholders, ","), projectFilter)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var results []*domain.SearchResult
	for rows.Next() {
		result, err := s.scanSearchResult(rows, -200) // Lower priority than direct matches
		if err != nil {
			continue
		}
		result.ScoreBreakdown = domain.SearchScoreBreakdown{
			Strategy: "graph_neighbor",
		}
		results = append(results, result)
	}
	return results
}

// --- Multi-Signal Fusion ---

// combineAllSignals combines all search signals using configurable RRF.
func (s *Store) combineAllSignals(keyword, topicExp, prf, graph, importance []*domain.SearchResult, fusionK float64, limit int) []*domain.SearchResult {
	scores := make(map[int64]float64)
	observations := make(map[int64]*domain.SearchResult)
	breakdowns := make(map[int64]domain.SearchScoreBreakdown)

	addSignal := func(results []*domain.SearchResult, signalName string) {
		for rank, r := range results {
			id := r.ID
			scores[id] += 1.0 / (fusionK + float64(rank+1))
			if _, exists := observations[id]; !exists {
				observations[id] = r
			}
			bd := breakdowns[id]
			switch signalName {
			case "keyword":
				if r.ScoreBreakdown.KeywordBM25 != 0 {
					bd.KeywordBM25 = r.ScoreBreakdown.KeywordBM25
				}
				if r.ScoreBreakdown.RecencyBoost != 0 {
					bd.RecencyBoost = r.ScoreBreakdown.RecencyBoost
				}
				if r.ScoreBreakdown.TopicKeyExact {
					bd.TopicKeyExact = true
				}
			case "topic_expansion":
				bd.TopicKeyExpand = true
			case "importance":
				bd.ImportanceRank = r.ScoreBreakdown.ImportanceRank
			}
			breakdowns[id] = bd
		}
	}

	addSignal(keyword, "keyword")
	addSignal(topicExp, "topic_expansion")
	addSignal(prf, "keyword") // PRF results share keyword characteristics
	addSignal(graph, "graph")
	addSignal(importance, "importance")

	// Build combined results
	combined := make([]*domain.SearchResult, 0, len(observations))
	for id, obs := range observations {
		bd := breakdowns[id]
		bd.Strategy = "enhanced"
		bd.FusionScore = scores[id]
		obs.Rank = scores[id]
		obs.ScoreBreakdown = bd
		combined = append(combined, obs)
	}

	sort.Slice(combined, func(i, j int) bool {
		return combined[i].Rank > combined[j].Rank
	})

	if len(combined) > limit {
		combined = combined[:limit]
	}
	return combined
}

// collectIDs gathers unique observation IDs from multiple result sets.
func collectIDs(sets ...[]*domain.SearchResult) []int64 {
	seen := make(map[int64]bool)
	var ids []int64
	for _, set := range sets {
		for _, r := range set {
			if !seen[r.ID] {
				seen[r.ID] = true
				ids = append(ids, r.ID)
			}
		}
	}
	return ids
}

var searchTermsReplacer = strings.NewReplacer("*", "", "^", "", "~", "", "+", "", "-", " ", "(", "", ")", "", `"`, "", "'", "")

// extractSearchTerms extracts clean search terms from a query string.
func extractSearchTerms(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	// Performance: Use package-level replacer to avoid allocation on every call
	query = searchTermsReplacer.Replace(query)
	terms := strings.Fields(query)
	// Filter out very common stop words
	var filtered []string
	stopWords := map[string]bool{"the": true, "a": true, "an": true, "is": true, "in": true, "on": true, "at": true, "to": true, "for": true, "of": true, "and": true, "or": true}
	for _, t := range terms {
		if !stopWords[t] && len(t) > 1 {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// Ensure Store implements domain.SearchRepository
var _ domain.SearchRepository = (*Store)(nil)
