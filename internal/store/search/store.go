// Package search implements the SQLite search store for Cortex.
//
// It provides FTS5-based full-text search with BM25 ranking, topic key
// direct lookup, RRF fusion for hybrid search, and snippet extraction.
// The store implements the domain.SearchRepository interface.
package search

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
)

// Store implements the SQLite search store.
// It provides FTS5-based full-text search with advanced features.
// GraphEdgeProvider is an optional interface for temporal edge filtering.
// When set, graph neighbor expansion respects temporal validity.
type GraphEdgeProvider interface {
	GetEdgesValidAt(ctx context.Context, obsID int64, at time.Time) ([]*domain.Edge, error)
	GetEdgesForObservation(ctx context.Context, obsID int64) ([]*domain.Edge, error)
}

// Store implements FTS5-based search for Cortex.
type Store struct {
	db    *sql.DB
	Graph GraphEdgeProvider // Optional; enables temporal-aware graph expansion

	// --- Request-scoped feedback attribution (REQ-RET-001) ---
	//
	// feedbackMu protects feedbackSessions. Each Search call generates a unique
	// SearchID, stamps it on every result, and registers a session here. Feedback
	// (RecordFeedback) looks up the session by SearchID so attribution binds to
	// the originating search — never a shared global. This replaces the removed
	// shared mutable search-query field which raced under concurrent searches.
	feedbackMu       sync.RWMutex
	feedbackSessions map[domain.SearchID]*searchSession
	feedbackSink     FeedbackSink // optional; called by RecordFeedback for known IDs
	maxSessions      int          // bounded registry size (evicts oldest when exceeded)

	// --- LongMemEval pack (REQ-LME-001) ---
	//
	// factMu serializes lazy DDL + per-call reconcile of the fact-key
	// auxiliary tables; factDDLReady latches once the DDL has succeeded so a
	// failing schema (read-only DB, probe preflight) degrades to
	// session-text-only retrieval instead of erroring every search.
	factMu       sync.Mutex
	factDDLReady bool
	// TimeRangeInferencer is the pluggable LLM-backed time-range inference
	// seam for time-aware query expansion. Nil selects the offline
	// zero-CGO heuristic. It returns (nil, false) when the query carries no
	// temporal signal, which disables pruning entirely.
	TimeRangeInferencer TimeRangeInferencer

	// rerank resolves search.rerank_provider lazily once per Store so the
	// default disabled path constructs no rerank machinery (REQ-RNK-002).
	rerank rerankGate
}

// FeedbackSink persists feedback attributed to a KNOWN, valid SearchID. The
// store resolves the session by SearchID BEFORE invoking the sink, so the sink
// is only ever called for sessions that exist in the registry — never for
// unknown or expired IDs (those are safe no-ops). This is the request-scoped
// attribution anchor (REQ-RET-001).
//
// The production sink (wired by bundle.WireSearchFeedback) delegates to the
// observation store's RecordSearchFeedback using the session's query. Tests
// inject a recording sink to assert attribution correctness.
type FeedbackSink func(ctx context.Context, searchID domain.SearchID, query string, observationID int64, rankPosition int) error

// searchSession captures the context of one search request so feedback can be
// attributed back to it. It is bounded by the registry's maxSessions cap.
type searchSession struct {
	query     string         // the query that produced this search
	resultIDs map[int64]bool // observation IDs eligible for feedback
	createdAt time.Time      // used for oldest-first eviction
}

// defaultMaxFeedbackSessions bounds the in-memory feedback registry to prevent
// unbounded growth. Each entry is small (query + ID set); 1024 is generous for
// typical interleaved-search workloads while keeping memory predictable.
const defaultMaxFeedbackSessions = 1024

// NewStore creates a new search store with the given database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{
		db:               db,
		feedbackSessions: make(map[domain.SearchID]*searchSession),
		maxSessions:      defaultMaxFeedbackSessions,
	}
}

// SetFeedbackSink wires the optional feedback persistence sink. When nil (or
// not called), RecordFeedback validates the SearchID but performs no
// persistence — feedback is safely disabled, never falling back to a shared
// global (REQ-RET-001).
func (s *Store) SetFeedbackSink(sink FeedbackSink) {
	s.feedbackMu.Lock()
	s.feedbackSink = sink
	s.feedbackMu.Unlock()
}

// NewSearchID generates a fresh, unique, request-scoped SearchID using
// crypto/rand (stdlib, zero-CGO). Uniqueness does not rely on a shared mutable
// counter, so concurrent calls cannot race or collide.
func NewSearchID() domain.SearchID {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error on supported platforms
	return domain.SearchID("sch_" + hex.EncodeToString(b))
}

// Search performs a full-text search with the given query and options.
// It supports:
//   - FTS5 keyword search with BM25 ranking
//   - Topic key direct lookup (queries containing '/')
//   - RRF fusion for combining topic key and keyword results
//   - Snippet extraction using FTS5 snippet() function
//   - Column weighting (content 2x title)
//
// Returns search results ordered by relevance (rank). Every result carries a
// request-scoped SearchID (REQ-RET-001) so feedback can be attributed to THIS
// search rather than a shared global.
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

	// LongMemEval fact-key reconcile (REQ-LME-001): keep search_fact_keys in
	// sync with live observations before any ranked list is built. Failures
	// degrade to session-text-only retrieval (never fail the search).
	factReady := s.ensureFactKeys(ctx)

	// Dual-level routing (REQ-RET-002): a deterministic, observable decision that
	// is a PURE function of the query. A '/' routes to topic-exact + keyword
	// fusion (profileDualLevel); a plain query routes to keyword-only retrieval
	// (profileKeyword). The profile is exposed so callers/tests can assert which
	// strategy was selected without inspecting internals.
	var results []*domain.SearchResult
	var err error
	switch classifyQuery(query) {
	case profileDualLevel:
		results, err = s.searchDualLevel(ctx, query, opts, limit, factReady)
	default:
		results, err = s.searchEnhanced(ctx, query, opts, limit, factReady)
	}
	if err != nil {
		return nil, err
	}

	// Request-scoped feedback attribution (REQ-RET-001): stamp a stable SearchID
	// on every result and register the session so feedback binds to THIS search.
	// The SearchID is generated AFTER the search completes but is stable for all
	// results of this call. This must not race with concurrent searches — the
	// registry is mutex-protected and the SearchID is process-unique.
	searchID := NewSearchID()
	for _, r := range results {
		r.SearchID = searchID
	}
	s.registerSession(searchID, query, results)

	return results, nil
}

// registerSession records the search context for later feedback attribution. The
// registry is bounded: when it reaches maxSessions, the oldest session is
// evicted. Feedback for an evicted (unknown) SearchID is a safe no-op.
func (s *Store) registerSession(searchID domain.SearchID, query string, results []*domain.SearchResult) {
	s.feedbackMu.Lock()
	defer s.feedbackMu.Unlock()
	// Bounded eviction: drop the oldest session when at capacity.
	if len(s.feedbackSessions) >= s.maxSessions {
		s.evictOldestLocked()
	}
	ids := make(map[int64]bool, len(results))
	for _, r := range results {
		ids[r.ID] = true
	}
	s.feedbackSessions[searchID] = &searchSession{
		query:     query,
		resultIDs: ids,
		createdAt: time.Now(),
	}
}

// evictOldestLocked removes the single oldest session. Caller must hold
// feedbackMu in write mode.
func (s *Store) evictOldestLocked() {
	var oldestID domain.SearchID
	var oldestTime time.Time
	for id, session := range s.feedbackSessions {
		if oldestID == "" || session.createdAt.Before(oldestTime) {
			oldestID = id
			oldestTime = session.createdAt
		}
	}
	if oldestID != "" {
		delete(s.feedbackSessions, oldestID)
	}
}

// RecordFeedback attributes retrieval feedback to the search identified by
// searchID. The feedback is recorded against the session's query (the
// originating search), NOT a shared global.
//
// Behavior:
//   - Known SearchID: the feedback sink (if wired) is invoked with the session's
//     query. If no sink is wired, the SearchID is validated but no persistence
//     occurs (feedback safely disabled).
//   - Unknown/expired SearchID: safe no-op — returns nil, never panics, and
//     NEVER falls back to a shared global query (REQ-RET-001).
func (s *Store) RecordFeedback(ctx context.Context, searchID domain.SearchID, observationID int64, rankPosition int) error {
	s.feedbackMu.RLock()
	session, ok := s.feedbackSessions[searchID]
	sink := s.feedbackSink
	s.feedbackMu.RUnlock()

	if !ok {
		// Unknown or evicted SearchID: safe no-op. No global fallback, no panic.
		return nil
	}
	if sink == nil {
		// Sink not wired: SearchID validated, no persistence. Safe disable.
		return nil
	}
	return sink(ctx, searchID, session.query, observationID, rankPosition)
}

// searchDualLevel implements formal dual-level '/' retrieval (REQ-RET-002).
//
// The topic-exact retriever (lookupByTopicKey) and the keyword retriever
// (searchKeywords) each emit a TRUE ranked list. The two lists are fused by RRF
// (k=60) — a relevance score (BM25) is never treated as a rank input. After
// fusion, recency (0.995^hours) and importance are applied as a FINAL
// multiplicative re-rank over the fused candidate set; they are NOT injected as
// pseudo-ranked-lists into RRF.
func (s *Store) searchDualLevel(ctx context.Context, query string, opts domain.SearchOptions, limit int, factReady bool) ([]*domain.SearchResult, error) {
	// Retriever 1: topic-exact direct lookup (true ranked list).
	topicExact, err := s.lookupByTopicKey(ctx, query, opts, limit)
	if err != nil {
		return nil, fmt.Errorf("search store: topic key lookup: %w", err)
	}

	// Retriever 2: FTS5 keyword search with BM25 ranking (true ranked list). The
	// raw BM25 score is carried on the item but is IGNORED by RRF — only the
	// list position contributes (score-as-rank defect pin).
	keyword, err := s.searchKeywords(ctx, query, opts, limit*3)
	if err != nil {
		return nil, fmt.Errorf("search store: keyword search: %w", err)
	}

	// Retriever 3: fact-augmented keys (REQ-LME-001) — true ranked list.
	factKeys := s.factKeyRankedList(ctx, query, opts, limit, factReady)

	// Unified assembly path (REQ-RET-002 W5.3): fuse -> revalidate vs SQLite ->
	// multiplicative re-rank (stable-ID tie-break) -> deterministic pagination.
	return s.assembleResults(ctx, []rankedList{
		{name: "topic_exact", items: topicExact},
		{name: "keyword", items: keyword},
		{name: "fact_keys", items: factKeys},
	}, query, opts, limit), nil
}

// lookupByTopicKey performs a direct lookup by topic key.
// This returns observations where the topic_key matches the query exactly.
func (s *Store) lookupByTopicKey(ctx context.Context, query string, opts domain.SearchOptions, limit int) ([]*domain.SearchResult, error) {
	filterFrag, filterArgs := applyFilterClauses(opts)
	baseQuery := `
		SELECT 
			o.id, o.title, o.content, o.type, o.project, o.scope, o.session_id,
			o.topic_key, o.confidence, o.source, o.tags, o.created_at, o.updated_at,
			-1000.0 as rank
		FROM observations o
		WHERE o.topic_key = ? AND o.deleted_at IS NULL
	` + filterFrag + `
		ORDER BY o.updated_at DESC, o.id LIMIT ?`
	args := append([]any{query}, filterArgs...)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("topic key lookup: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []*domain.SearchResult
	for rows.Next() {
		result, err := s.scanSearchResult(rows) // High priority for exact topic match
		if err != nil {
			return nil, err
		}
		result.ScoreBreakdown = domain.SearchScoreBreakdown{
			Strategy:      "topic_key",
			TopicKeyExact: true,
		}
		result.Content = previewContent(query, result.Content, answerSpanPreviewChars)
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("topic key lookup: iterate rows: %w", err)
	}

	return results, nil
}

// searchKeywords performs ONE strict-AND FTS5 keyword search with BM25 ranking.
// It uses column weighting where content has 2x weight compared to title and is
// the recall contract: a keyword query resolves exactly as it always has.
func (s *Store) searchKeywords(ctx context.Context, query string, opts domain.SearchOptions, limit int) ([]*domain.SearchResult, error) {
	filterFrag, filterArgs := applyFilterClauses(opts)
	return s.queryKeywordTier(ctx, query, sanitizeFTS(query), filterFrag, filterArgs, limit)
}

// searchRelaxedKeywords runs ONLY the relaxed tiers of a natural-language query
// whose strict tier already produced nothing. It is a rescue, never a
// replacement: callers invoke it only when every other ranked list is empty, so
// it can add candidates to a pool that had nothing to give and can never
// displace results strict retrieval already returned.
func (s *Store) searchRelaxedKeywords(ctx context.Context, query string, opts domain.SearchOptions, limit int) ([]*domain.SearchResult, error) {
	tiers := relaxedKeywordFTSTiers(query)
	if len(tiers) == 0 {
		return nil, nil
	}
	filterFrag, filterArgs := applyFilterClauses(opts)
	for _, ftsQuery := range tiers {
		results, err := s.queryKeywordTier(ctx, query, ftsQuery, filterFrag, filterArgs, limit)
		if err != nil {
			return nil, err
		}
		if len(results) > 0 {
			return results, nil
		}
	}
	return nil, nil
}

// queryKeywordTier runs one MATCH expression against the weighted FTS index
// (BM25 weights: content=2.0, title=1.0, others=0.5) under the caller's
// filter fragments. The raw BM25 score is carried on the breakdown but is
// IGNORED by RRF — only list position contributes (score-as-rank defect pin).
func (s *Store) queryKeywordTier(ctx context.Context, query, ftsQuery, filterFrag string, filterArgs []any, limit int) ([]*domain.SearchResult, error) {
	baseQuery := `
		SELECT 
			o.id, o.title, o.content, o.type, o.project, o.scope, o.session_id,
			o.topic_key, o.confidence, o.source, o.tags, o.created_at, o.updated_at,
			bm25(observations_fts, 1.0, 2.0, 0.5, 0.5, 0.5, 0.5, 0.5) as rank
		FROM observations_fts fts
		JOIN observations o ON o.id = fts.rowid
		WHERE observations_fts MATCH ? AND o.deleted_at IS NULL
	` + filterFrag + `
		ORDER BY rank, o.id LIMIT ?`
	args := append([]any{ftsQuery}, filterArgs...)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []*domain.SearchResult
	for rows.Next() {
		result, err := s.scanSearchResult(rows)
		if err != nil {
			return nil, err
		}
		result.ScoreBreakdown = domain.SearchScoreBreakdown{
			Strategy:    "keyword",
			KeywordBM25: result.Rank,
		}
		result.Content = previewContent(query, result.Content, answerSpanPreviewChars)
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

// scanSearchResult is the SINGLE unified row scanner (REQ-RET-002 helper
// unification). Every retrieval query selects the same 14-column shape ending
// in a `rank` column (keyword: BM25; topic/graph/PRF: a fixed priority). The
// raw rank is carried on the item but is IGNORED by RRF — only list POSITION
// contributes (score-as-rank defect pin). This replaces the prior duplicate
// scanSearchResult/scanSearchResultWithRank pair.
func (s *Store) scanSearchResult(rows *sql.Rows) (*domain.SearchResult, error) {
	var result domain.SearchResult
	var createdAtStr, updatedAtStr string
	var topicKey, source, tagsJSON sql.NullString
	var rank float64

	err := rows.Scan(
		&result.ID, &result.Title, &result.Content, &result.Type,
		&result.Project, &result.Scope, &result.SessionID, &topicKey,
		&result.Confidence, &source, &tagsJSON,
		&createdAtStr, &updatedAtStr, &rank,
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

// applyFilterClauses is the SINGLE unified filter-fragment builder
// (REQ-RET-002 helper unification). It emits the WHERE-clause fragments for the
// type/project/scope filters in a FIXED order (type, project, scope) with a
// leading " AND " on each fragment, plus the bound args. Every retrieval path
// routes its filter application through this helper so the filter semantics can
// never drift between retrievers.
func applyFilterClauses(opts domain.SearchOptions) (string, []any) {
	var frag strings.Builder
	var args []any
	if opts.Type != "" {
		frag.WriteString(" AND o.type = ?")
		args = append(args, opts.Type)
	}
	if opts.Project != "" {
		frag.WriteString(" AND o.project = ?")
		args = append(args, opts.Project)
	}
	if opts.Scope != "" {
		frag.WriteString(" AND o.scope = ?")
		args = append(args, normalizeScope(opts.Scope))
	}
	return frag.String(), args
}

// ftsOperatorReplacer removes FTS5 special operators (* ^ ~ + - ( )) that
// could cause syntax errors. It is shared byte-identically by sanitizeFTS and
// sanitizeFactFTS so the two query builders can never drift.
var ftsOperatorReplacer = strings.NewReplacer(
	"*", "",
	"^", "",
	"~", "",
	"+", "",
	"-", " ",
	"(", "",
	")", "",
)

// stripFTSOperators applies ftsOperatorReplacer to a raw query.
func stripFTSOperators(query string) string {
	return ftsOperatorReplacer.Replace(query)
}

// splitFTSTerms normalizes a raw query into FTS terms: operator characters
// stripped, double quotes escaped, whitespace-split. It returns every term plus
// the subset that survives stopword pruning (factStopWords), both in query
// order.
func splitFTSTerms(query string) (all []string, content []string) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, nil
	}
	normalized := strings.ReplaceAll(stripFTSOperators(trimmed), `"`, `'`)
	for _, term := range strings.Fields(normalized) {
		all = append(all, term)
		if !factStopWords[strings.ToLower(term)] {
			content = append(content, term)
		}
	}
	return all, content
}

// quoteFTSTerms wraps each term in double quotes and joins them with the given
// FTS5 boolean operator. The last term keeps prefix matching so "auth" still
// matches "authentication".
func quoteFTSTerms(terms []string, operator string) string {
	quoted := make([]string, len(terms))
	for i, term := range terms {
		if i == len(terms)-1 {
			quoted[i] = fmt.Sprintf(`"%s*"`, term)
		} else {
			quoted[i] = fmt.Sprintf(`"%s"`, term)
		}
	}
	return strings.Join(quoted, " "+operator+" ")
}

// sanitizeFTS sanitizes a query string for FTS5 full-text search.
// It wraps each term in double quotes to prevent FTS5 syntax errors
// from special characters, and adds prefix matching for the last term.
func sanitizeFTS(query string) string {
	all, _ := splitFTSTerms(query)
	if len(all) == 0 {
		return ""
	}
	return quoteFTSTerms(all, "AND")
}

// relaxedKeywordFTSTiers returns the relaxed MATCH expressions for a
// natural-language query: stopword-dropped AND, then OR over the surviving
// content terms. It returns nil for a keyword-only query (nothing to relax) or
// a query that is nothing but stopwords, which is exactly the gate that keeps
// keyword lookups on the strict sanitizeFTS contract. A strict AND over a full
// interrogative sentence starves retrieval because no single session contains
// every function word (LongMemEval honest-03 root cause 1), so the relaxed
// forms let the same ranked list start from content vocabulary instead of
// returning nothing.
func relaxedKeywordFTSTiers(query string) []string {
	all, content := splitFTSTerms(query)
	if len(all) == 0 || len(content) == 0 || len(content) == len(all) {
		return nil
	}
	return []string{quoteFTSTerms(content, "AND"), quoteFTSTerms(content, "OR")}
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

// answerSpanPreviewChars bounds the content window every retrieval path hands
// back to callers. The previous 300-char window placed the answer span outside
// the preview in 75% of correctly retrieved LongMemEval evidence (honest-03
// root cause 2: the judge scores the preview, not the session). A 1200-char
// window keeps results a bounded preview — never a full dump — while giving a
// retrieved span room to land in view; measured as the best joint point on the
// LongMemEval/LOCOMO A/B matrix (see the honest-10 evidence refs).
const answerSpanPreviewChars = 1200

// previewContent returns a bounded window of content chosen by
// previewWindowStart.
func previewContent(query, content string, maxLength int) string {
	if maxLength <= 0 || len(content) <= maxLength {
		return content
	}

	start := previewWindowStart(previewAnchorTerms(query), strings.ToLower(content), maxLength)
	// lowercasing can change byte length for a few Unicode characters; clamp
	// against the raw content so the window can never run past it.
	if maxStart := len(content) - maxLength; start > maxStart {
		start = maxStart
	}
	end := start + maxLength
	if end > len(content) {
		end = len(content)
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

// previewAnchorTerms returns distinct query terms in anchor priority order:
// content terms first (query order), then function words, so a window is never
// anchored on a greeting just because the question starts with "What" or "I".
// Punctuation is trimmed so "with?" classifies and matches as "with".
func previewAnchorTerms(query string) []string {
	seen := make(map[string]bool)
	var content, stopwords []string
	for _, field := range strings.Fields(strings.ToLower(query)) {
		term := strings.Trim(field, `"'*/?.!,;:`)
		if term == "" || seen[term] {
			continue
		}
		seen[term] = true
		if factStopWords[term] {
			stopwords = append(stopwords, term)
		} else {
			content = append(content, term)
		}
	}
	return append(content, stopwords...)
}

// previewWindowStart picks the window start for a preview: among the first
// occurrence of each anchor term, the window covering the most distinct anchor
// terms wins, and the earliest window breaks ties. The result is always within
// [0, len(lowerContent)-maxLength] so the window fits the content.
func previewWindowStart(anchors []string, lowerContent string, maxLength int) int {
	positions := make([]int, 0, len(anchors))
	for _, term := range anchors {
		if idx := strings.Index(lowerContent, term); idx >= 0 {
			positions = append(positions, idx)
		}
	}
	if len(positions) == 0 {
		return 0
	}

	maxStart := len(lowerContent) - maxLength
	if maxStart < 0 {
		maxStart = 0
	}
	best, bestScore := 0, -1
	for _, pos := range positions {
		start := pos - maxLength/3
		if start < 0 {
			start = 0
		}
		if start > maxStart {
			start = maxStart
		}
		window := lowerContent[start:min(start+maxLength, len(lowerContent))]
		score := 0
		for _, term := range anchors {
			if strings.Contains(window, term) {
				score++
			}
		}
		if score > bestScore || (score == bestScore && start < best) {
			best, bestScore = start, score
		}
	}
	return best
}

// --- Enhanced Search Pipeline ---

// searchEnhanced combines FTS5 keyword search with topic key expansion,
// pseudo-relevance feedback, and graph neighborhood expansion using
// multi-signal RRF fusion. Recency and importance are applied as a FINAL
// multiplicative re-rank after RRF — they are NEVER injected as
// pseudo-ranked-lists into RRF (REQ-RET-002).
func (s *Store) searchEnhanced(ctx context.Context, query string, opts domain.SearchOptions, limit int, factReady bool) ([]*domain.SearchResult, error) {
	// Retriever 1: FTS5 keyword results (true ranked list; fetch extra candidates).
	keywordResults, err := s.searchKeywords(ctx, query, opts, limit*3)
	if err != nil {
		return nil, err
	}

	// Retriever 2: topic key expansion (LIKE matching) — true ranked list.
	topicExpResults, err := s.searchByTopicKeyExpansion(ctx, query, opts, limit)
	if err != nil {
		log.Printf("search: topic key expansion error (non-fatal): %v", err)
		topicExpResults = nil
	}

	// Retriever 3: fact-augmented keys (REQ-LME-001) — true ranked list.
	factKeys := s.factKeyRankedList(ctx, query, opts, limit, factReady)

	// The relaxed keyword tier (searchRelaxedKeywords) stays OUT of this
	// pipeline: its results populate the prior-stage FusionScores that
	// SkewRoute routes on, flipping tier decisions and regressing LOCOMO
	// 0.56 -> 0.51-0.53 in both measured rank profiles (Cortex gotcha
	// gotchas/honest-10-retrieval-basics-fix). Seam redesign deferred.

	// Retriever 4: Pseudo-Relevance Feedback — extract distinctive terms from
	// top results and run a second FTS5 query to improve recall (true ranked list).
	prfResults := s.pseudoRelevanceFeedback(ctx, query, keywordResults, opts, limit)

	// Retriever 5 (optional): graph neighborhood expansion — true ranked list.
	var graphResults []*domain.SearchResult
	if opts.GraphExpand && len(keywordResults) > 0 {
		graphResults = s.graphNeighborExpansion(ctx, keywordResults, opts, limit)
	}

	// Unified assembly path (REQ-RET-002 W5.3): fuse -> revalidate vs SQLite ->
	// multiplicative re-rank (stable-ID tie-break) -> deterministic pagination.
	return s.assembleResults(ctx, []rankedList{
		{name: "keyword", items: keywordResults},
		{name: "topic_expansion", items: topicExpResults},
		{name: "prf", items: prfResults},
		{name: "graph", items: graphResults},
		{name: "fact_keys", items: factKeys},
	}, query, opts, limit), nil
}

// resolveFusionK returns the RRF smoothing constant, defaulting to the standard
// k=60 when the caller did not configure one.
func resolveFusionK(configured float64) float64 {
	if configured > 0 {
		return configured
	}
	return rrfConstant
}

// recencyFactor computes the recency decay multiplier 0.995^hours where hours is
// the age of the observation in hours since its timestamp (REQ-RET-002). A
// recently-touched observation gets a factor near 1.0; an old one decays toward
// 0. Negative ages (future timestamps) are clamped to 0 so the factor never
// exceeds 1.0. This is a FINAL multiplicative re-rank input, NEVER an RRF input.
func recencyFactor(hours float64) float64 {
	if hours < 0 {
		hours = 0
	}
	return math.Pow(0.995, hours)
}

// importanceFactor computes the importance multiplier applied as a FINAL
// multiplicative re-rank. It is neutral (1.0) when no importance data exists or
// the score is 0; otherwise (1 + score) — a monotonic boost around 1.0 that
// never zeroes a legitimate match. This is NEVER an RRF input (REQ-RET-002).
func importanceFactor(score float64, found bool) float64 {
	if !found {
		return 1.0
	}
	return 1.0 + score
}

// rerankByRecencyImportance applies recency (0.995^hours from the observation's
// timestamp) and importance as a FINAL multiplicative re-rank over the
// RRF-fused candidate set. NEITHER signal is fed into RRF as a
// pseudo-ranked-list (REQ-RET-002). The fused RRF score (candidate.Rank) is
// multiplied by recency*importance; results are re-sorted by the product
// (ties broken by ID ascending for determinism) and trimmed to limit.
func (s *Store) rerankByRecencyImportance(ctx context.Context, candidates []*domain.SearchResult, limit int) []*domain.SearchResult {
	if len(candidates) == 0 {
		return candidates
	}

	// Fetch importance scores once for the whole candidate set.
	ids := make([]int64, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.ID)
	}
	importance := s.fetchImportanceScores(ctx, ids)

	now := time.Now()
	for _, c := range candidates {
		rrf := c.Rank // RRF fusion score produced by rrfFuse

		// Recency from the observation's own timestamp (zero timestamp -> neutral).
		var hours float64
		if !c.UpdatedAt.IsZero() {
			hours = now.Sub(c.UpdatedAt).Hours()
		}
		recency := recencyFactor(hours)

		score, found := importance[c.ID]
		imp := importanceFactor(score, found)

		finalScore := rrf * recency * imp

		bd := c.ScoreBreakdown
		bd.RecencyBoost = recency
		if found {
			bd.ImportanceRank = score
		}
		bd.FusionScore = rrf
		bd.Strategy = "enhanced"
		c.ScoreBreakdown = bd
		c.Rank = finalScore
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Rank != candidates[j].Rank {
			return candidates[i].Rank > candidates[j].Rank
		}
		return candidates[i].ID < candidates[j].ID // deterministic tie-break
	})

	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

// fetchImportanceScores returns a map of observation_id -> importance score for
// the given IDs. Missing rows are simply absent from the map (callers treat
// absence as neutral). Failures are non-fatal (skip importance re-rank).
func (s *Store) fetchImportanceScores(ctx context.Context, ids []int64) map[int64]float64 {
	out := make(map[int64]float64, len(ids))
	if len(ids) == 0 {
		return out
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`
		SELECT observation_id, score
		FROM importance_scores
		WHERE observation_id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return out // non-fatal
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var obsID int64
		var score float64
		if err := rows.Scan(&obsID, &score); err == nil {
			out[obsID] = score
		}
	}
	return out
}

// --- Unified Retrieval Assembly (REQ-RET-002: W5.3 helper unification) ---
//
// rerankGate resolves search.rerank_provider once per Store and applies the
// configured reranker as the post-fusion, pre-limit ordering step of the
// unified assembly path (REQ-RNK-002).
type rerankGate struct {
	mu       sync.Mutex
	loaded   bool
	reranker retrieval.Reranker
}

func (g *rerankGate) current() retrieval.Reranker {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.loaded {
		g.reranker = resolveStoreReranker()
		g.loaded = true
	}
	return g.reranker
}

// resolveStoreReranker follows the local composition rules: the disabled
// default builds no machinery, and the server-only openai-compatible preset is
// refused fail-closed by the local factory, so a reranker that cannot be
// constructed locally degrades to "rerank off" with a warning instead of
// failing the search.
func resolveStoreReranker() retrieval.Reranker {
	cfg, err := config.Load("")
	if err != nil || cfg == nil {
		cfg = config.DefaultConfig()
	}
	reranker, err := retrieval.NewReranker(retrieval.RerankConfig{
		Provider: cfg.Search.RerankProvider,
		Model:    cfg.Search.RerankModel,
		BaseURL:  cfg.Search.RerankBaseURL,
	})
	if err != nil {
		log.Printf("warning: rerank disabled: %v", err)
		return nil
	}
	return reranker
}

// applyRerank orders fused candidates with the configured reranker. It runs as
// the LAST ordering input before pagination so a reranked order reaches the
// response; rerank errors and set-breaking provider output degrade to the
// un-reranked fusion order and never fail the search.
func (g *rerankGate) applyRerank(query string, candidates []*domain.SearchResult) []*domain.SearchResult {
	reranker := g.current()
	if reranker == nil || len(candidates) == 0 {
		return candidates
	}
	ordered, err := reranker.Rerank(query, candidates)
	if err != nil {
		log.Printf("warning: rerank failed; keeping fusion order: %v", err)
		return candidates
	}
	if !sameResultSet(ordered, candidates) {
		log.Printf("warning: rerank changed the candidate set; keeping fusion order")
		return candidates
	}
	return ordered
}

// sameResultSet enforces the Reranker contract (same pointers, same
// multiplicity) so a misbehaving provider can never silently drop a candidate.
func sameResultSet(got, want []*domain.SearchResult) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[*domain.SearchResult]int, len(want))
	for _, r := range want {
		seen[r]++
	}
	for _, r := range got {
		if seen[r] == 0 {
			return false
		}
		seen[r]--
	}
	return true
}

// assembleResults is the SINGLE unified retrieval result-assembly path. Both
// routing profiles (searchDualLevel, searchEnhanced) funnel their ranked lists
// through it so there is exactly ONE execution / result-assembly /
// response-formatting path. It preserves the W5.2 contract (RRF over true
// ranked lists + final multiplicative re-rank) and the W5.1 SearchID stamp
// (applied by the caller Search()). The pipeline is:
//
//  1. rrfFuse: fuse TRUE ranked lists (position-only; k=60).
//  2. revalidateCandidates: drop phantom/soft-deleted candidates against SQLite.
//  3. rerankByRecencyImportance: final multiplicative re-rank with a stable-ID
//     tie-break (deterministic).
//  4. applyPagination: deterministic, opaque, context-bound cursor pagination.
func (s *Store) assembleResults(ctx context.Context, lists []rankedList, query string, opts domain.SearchOptions, limit int) []*domain.SearchResult {
	k := resolveFusionK(opts.FusionK)
	pool := limit * 3
	if pool < limit {
		pool = limit
	}
	// Time-aware query expansion (REQ-LME-001): prune ranked lists by the
	// inferred time range BEFORE fusion so pruned candidates never become RRF
	// inputs. No temporal signal / inverted range / missing timestamps leave
	// the lists untouched (unpruned fallback).
	lists = s.pruneListsByTimeRange(lists, query)
	fused := rrfFuse(lists, k, pool)
	validated := s.revalidateCandidates(ctx, fused)
	reranked := s.rerankByRecencyImportance(ctx, validated, pool)
	// Post-fusion, PRE-limit: the configured reranker orders the full fused
	// pool and only applyPagination below truncates it to the request limit.
	reranked = s.rerank.applyRerank(query, reranked)
	return s.applyPagination(reranked, query, opts, limit)
}

// revalidateCandidates confirms each fused candidate still exists and is not
// soft-deleted in the live SQLite store, returning only live candidates in
// their original order. This eliminates phantom/stale results that entered the
// ranked set before a concurrent delete (REQ-RET-002 revalidation requirement).
//
// Note: at this layer an observation is "live" when deleted_at IS NULL (the
// observations table has no separate archived_at column here; archival maps to
// soft-delete). The revalidation is a correctness belt-and-suspenders over the
// per-retriever deleted_at filters, closing the window between ranking and
// return.
func (s *Store) revalidateCandidates(ctx context.Context, candidates []*domain.SearchResult) []*domain.SearchResult {
	if len(candidates) == 0 {
		return candidates
	}
	ids := make([]int64, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.ID)
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`
		SELECT id FROM observations
		WHERE id IN (%s) AND deleted_at IS NULL
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		// Non-fatal: if revalidation cannot run, return the candidates as-is
		// rather than failing the whole search. The per-retriever filters still
		// apply; revalidation is an additional guarantee, not the only one.
		return candidates
	}
	live := make(map[int64]bool, len(ids))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			live[id] = true
		}
	}
	_ = rows.Close()

	out := make([]*domain.SearchResult, 0, len(candidates))
	for _, c := range candidates {
		if live[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// applyPagination applies deterministic, opaque, context-bound pagination
// (REQ-RET-002). When opts.Cursor is empty, it returns the first `limit` entries
// of the already-deterministically-sorted slice. When opts.Cursor is present it
// is decoded and its context hash is compared to the active filter context; a
// match resumes strictly AFTER the encoded resume point, a mismatch (or corrupt
// cursor) is treated as a fresh page 0 — a cursor from one query/filter can
// NEVER leak results into another. When more results remain, the last returned
// result carries an opaque NextCursor bound to the current context.
func (s *Store) applyPagination(reranked []*domain.SearchResult, query string, opts domain.SearchOptions, limit int) []*domain.SearchResult {
	if len(reranked) == 0 {
		return reranked
	}
	if limit <= 0 {
		limit = 10
	}

	wantCtx := cursorContextHash(query, opts)
	start := 0
	if opts.Cursor != "" {
		if p, ok := decodeCursor(opts.Cursor); ok && p.Context == wantCtx {
			// Resume strictly AFTER the encoded stable ID. The final order is a
			// stable total order (rank DESC, ID asc) whose relative ordering is
			// time-invariant for fixed data: recency is a per-item multiplier
			// whose RATIO between any two fixed observations is constant (both
			// scale by 0.995^Δ as now advances), so locating the resume item by
			// its stable ID is drift-free and deterministic across calls. Rank
			// is carried in the cursor for diagnostics only, never used for
			// resume (a stale absolute rank would drift the boundary).
			resumeAt := -1
			for i, r := range reranked {
				if r.ID == p.ID {
					resumeAt = i
					break
				}
			}
			if resumeAt < 0 {
				// Resume ID absent from the current set (deleted, or a different
				// candidate pool): treat as fresh page 0 rather than guessing.
				start = 0
			} else {
				start = resumeAt + 1
			}
			// A cursor whose context matches but whose resume point is past the
			// end yields an empty page (no more results).
			if start >= len(reranked) {
				return []*domain.SearchResult{}
			}
		}
		// Mismatched or corrupt cursor: treat as fresh page 0 (start stays 0).
	}
	end := start + limit
	if end > len(reranked) {
		end = len(reranked)
	}
	page := reranked[start:end]

	// Emit an opaque next cursor on the last result when more results remain.
	if end < len(reranked) && len(page) > 0 {
		last := page[len(page)-1]
		if next, err := encodeCursor(cursorPayload{
			Context: wantCtx,
			Rank:    last.Rank,
			ID:      last.ID,
			Version: cursorVersion,
		}); err == nil {
			last.NextCursor = next
		}
	}
	return page
}

// --- Opaque, context-bound pagination cursor (REQ-RET-002) ---
//
// LOCAL MODE constraint: the cursor binds to query + project + scope + type + a
// local-mode stable identity. There is NO tenant/principal/grant yet (W11/W13).
// The binding is forward-compatible: cursorContextHash produces an opaque hash
// that W11/W13 can extend to include principal+grant without changing the cursor
// wire format. Cursor encoding uses only stdlib (hash/fnv, encoding/base64) —
// no Postgres/authz dependencies — keeping local zero-CGO/zero-LLM intact.
//
// A cursor is OPAQUE (base64 of versioned JSON), never a secret. It carries the
// resume rank + stable ID plus the context hash that binds it.

// cursorVersion is the wire-format version of the opaque cursor. Bumping it lets
// future waves evolve the payload while rejecting cross-version cursors safely.
const cursorVersion = 1

// localModeIdentity is the LOCAL MODE stable identity bound into the cursor
// context. W11/W13 will add a resolved Principal/grant; until then this constant
// anchors the context hash so cursors are scoped to local mode. It MUST NOT be
// client-supplied.
const localModeIdentity = "cortex-local-mode"

// cursorPayload is the internal, non-secret content of an opaque cursor.
type cursorPayload struct {
	Context string  `json:"c"` // opaque hash binding the active filter context
	Rank    float64 `json:"r"` // final score at the page boundary (resume rank)
	ID      int64   `json:"i"` // observation ID at the boundary (stable tie-break key)
	Version int     `json:"v"` // wire-format version
}

// cursorContextHash returns an opaque, stable hash binding the cursor to the
// active filter context. The same (query, opts) always yields the same hash;
// any change to query/project/scope/type (or the local-mode identity) yields a
// different hash, so a cursor decoded against a different context is rejected.
func cursorContextHash(query string, opts domain.SearchOptions) string {
	h := fnv.New64a()
	// Delimiters prevent ambiguity (e.g., "a"+"b" vs "ab"). hash.Hash writers
	// never return an error; the discarded return satisfies errcheck.
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s", query, opts.Project, opts.Scope, opts.Type, localModeIdentity)
	return hex.EncodeToString(h.Sum(nil))
}

// encodeCursor produces an opaque, versioned, base64-encoded cursor. It is not
// encrypted and carries no secret; opacity only prevents callers from depending
// on the internal structure.
func encodeCursor(p cursorPayload) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeCursor reverses encodeCursor. It returns ok=false for any malformed,
// truncated, or wrong-version input so callers can treat such values as "no
// cursor" (fresh page) rather than erroring.
func decodeCursor(raw string) (cursorPayload, bool) {
	if raw == "" {
		return cursorPayload{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursorPayload{}, false
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return cursorPayload{}, false
	}
	if p.Version != cursorVersion {
		return cursorPayload{}, false
	}
	return p, true
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

	filterFrag, filterArgs := applyFilterClauses(opts)
	baseQuery := fmt.Sprintf(`
		SELECT o.id, o.title, o.content, o.type, o.project, o.scope, o.session_id,
		       o.topic_key, o.confidence, o.source, o.tags, o.created_at, o.updated_at,
		       -500.0 as rank
		FROM observations o
		WHERE (%s) AND o.topic_key IS NOT NULL AND o.topic_key != '' AND o.deleted_at IS NULL
	`+filterFrag+`
		ORDER BY o.updated_at DESC, o.id LIMIT ?`, strings.Join(conditions, " OR "))
	args = append(args, filterArgs...)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("topic key expansion: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []*domain.SearchResult
	for rows.Next() {
		result, err := s.scanSearchResult(rows) // Priority between topic exact (-1000) and keyword
		if err != nil {
			return nil, err
		}
		result.ScoreBreakdown = domain.SearchScoreBreakdown{
			Strategy:       "topic_key_expansion",
			TopicKeyExpand: true,
		}
		result.Content = previewContent(query, result.Content, answerSpanPreviewChars)
		results = append(results, result)
	}

	return results, rows.Err()
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
// When opts.AsOf is set, only edges valid at that time are considered.
// When Graph provider is available, uses it for temporal filtering; otherwise falls back to SQL.
func (s *Store) graphNeighborExpansion(ctx context.Context, topResults []*domain.SearchResult, opts domain.SearchOptions, limit int) []*domain.SearchResult {
	if len(topResults) == 0 {
		return nil
	}

	topN := topResults
	if len(topN) > 5 {
		topN = topN[:5]
	}

	existing := make(map[int64]bool)
	for _, r := range topResults {
		existing[r.ID] = true
	}

	var neighborIDs []int64

	for _, r := range topN {
		if s.Graph != nil && opts.AsOf != nil {
			// Temporal-aware: only edges valid at the given time
			edges, err := s.Graph.GetEdgesValidAt(ctx, r.ID, *opts.AsOf)
			if err != nil {
				continue
			}
			for _, e := range edges {
				nID := e.ToObsID
				if nID == r.ID {
					nID = e.FromObsID
				}
				if !existing[nID] {
					neighborIDs = append(neighborIDs, nID)
					existing[nID] = true
				}
			}
		} else if s.Graph != nil {
			// Non-temporal but filter out deprecated/superseded edges
			edges, err := s.Graph.GetEdgesForObservation(ctx, r.ID)
			if err != nil {
				continue
			}
			for _, e := range edges {
				if e.FactState == domain.FactStateDeprecated || e.FactState == domain.FactStateSuperseded {
					continue
				}
				nID := e.ToObsID
				if nID == r.ID {
					nID = e.FromObsID
				}
				if !existing[nID] {
					neighborIDs = append(neighborIDs, nID)
					existing[nID] = true
				}
			}
		} else {
			// Fallback: raw SQL without temporal filtering
			rows, err := s.db.QueryContext(ctx, `
				SELECT DISTINCT CASE
					WHEN from_obs_id = ? THEN to_obs_id
					ELSE from_obs_id
				END as neighbor_id
				FROM edges
				WHERE (from_obs_id = ? OR to_obs_id = ?)
				  AND COALESCE(fact_state, 'current') NOT IN ('deprecated', 'superseded')
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

	filterFrag, filterArgs := applyFilterClauses(opts)

	// When searching as-of a date, exclude observations created after that date
	temporalFilter := ""
	if opts.AsOf != nil {
		temporalFilter = " AND o.created_at <= ?"
	}

	query := fmt.Sprintf(`
		SELECT o.id, o.title, o.content, o.type, o.project, o.scope, o.session_id,
		       o.topic_key, o.confidence, o.source, o.tags, o.created_at, o.updated_at,
		       -200.0 as rank
		FROM observations o
		WHERE o.id IN (%s) AND o.deleted_at IS NULL%s%s
		ORDER BY o.updated_at DESC, o.id
		LIMIT ?
	`, strings.Join(placeholders, ","), filterFrag, temporalFilter)
	args = append(args, filterArgs...)
	if opts.AsOf != nil {
		args = append(args, opts.AsOf.UTC().Format(time.RFC3339))
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var results []*domain.SearchResult
	for rows.Next() {
		result, err := s.scanSearchResult(rows)
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

// --- Dual-Level Routing & Ranked-List RRF Fusion (REQ-RET-002) ---

// searchProfile names a retrieval routing strategy. It is selected by
// classifyQuery, a pure function of the query, so the routing decision is
// deterministic and observable/testable.
type searchProfile string

const (
	// profileKeyword: a plain query (no '/') routes to keyword/global retrieval.
	profileKeyword searchProfile = "keyword"
	// profileDualLevel: a query containing '/' routes to topic-exact + keyword
	// fusion (two explicit ranked lists fused by RRF).
	profileDualLevel searchProfile = "dual_level"
)

// classifyQuery is the deterministic dual-level router (REQ-RET-002). A query
// whose trimmed form contains '/' is routed to topic-exact+keyword fusion;
// otherwise it routes to keyword-only retrieval. It is a PURE function of the
// query — no I/O — so tests can assert the routing decision directly.
func classifyQuery(query string) searchProfile {
	if strings.Contains(strings.TrimSpace(query), "/") {
		return profileDualLevel
	}
	return profileKeyword
}

// rrfConstant is the Reciprocal Rank Fusion smoothing constant (k=60, the
// standard value). RRF scores a candidate by its POSITION in each input list:
// score = sum over lists of 1/(k + rank). A raw relevance SCORE (BM25/FTS5) MUST
// NEVER be used as a rank input — only the 1-based position contributes
// (score-as-rank defect pin, REQ-RET-002).
const rrfConstant = 60.0

// rankedList is a named, ordered list of candidates from ONE retriever. Only
// the ORDER (position) is meaningful to RRF; per-item Rank/score values are
// IGNORED by fusion. Each retriever (keyword, topic-exact, topic-expansion,
// PRF, graph) produces one such list. Recency and importance do NOT produce
// ranked lists — they are final multiplicative re-rank inputs.
type rankedList struct {
	name  string
	items []*domain.SearchResult
}

// rrfFuse performs Reciprocal Rank Fusion (k) over one or more TRUE ranked
// lists. It is a PURE function: only the position of each candidate within its
// list contributes; raw scores (BM25/FTS5) are never treated as ranks
// (score-as-rank defect pin, REQ-RET-002). Returns the union of candidates,
// each stamped with its RRF fusion score in both Rank and ScoreBreakdown
// .FusionScore, ordered DESC by fusion score (ties broken by ID ascending for
// determinism). Breakdown flags (KeywordBM25, TopicKeyExact, TopicKeyExpand)
// are merged across all lists an item appears in.
func rrfFuse(lists []rankedList, k float64, limit int) []*domain.SearchResult {
	type acc struct {
		obs       *domain.SearchResult
		fusion    float64
		breakdown domain.SearchScoreBreakdown
	}
	scores := make(map[int64]*acc)

	for _, list := range lists {
		for i, r := range list.items {
			id := r.ID
			// 1-based rank position; the ONLY thing RRF consumes (score-as-rank pin).
			contribution := 1.0 / (k + float64(i+1))

			a, ok := scores[id]
			if !ok {
				a = &acc{obs: r}
				// Seed breakdown from the first observation of this candidate.
				a.breakdown = r.ScoreBreakdown
				scores[id] = a
			}
			a.fusion += contribution

			// Merge breakdown flags/scores from every list membership.
			bd := a.breakdown
			if r.ScoreBreakdown.KeywordBM25 != 0 {
				bd.KeywordBM25 = r.ScoreBreakdown.KeywordBM25
			}
			if r.ScoreBreakdown.RecencyBoost != 0 {
				bd.RecencyBoost = r.ScoreBreakdown.RecencyBoost
			}
			if r.ScoreBreakdown.TopicKeyExact {
				bd.TopicKeyExact = true
			}
			if r.ScoreBreakdown.TopicKeyExpand {
				bd.TopicKeyExpand = true
			}
			if r.ScoreBreakdown.ImportanceRank != 0 {
				bd.ImportanceRank = r.ScoreBreakdown.ImportanceRank
			}
			a.breakdown = bd
		}
	}

	combined := make([]*domain.SearchResult, 0, len(scores))
	for _, a := range scores {
		bd := a.breakdown
		bd.Strategy = "enhanced"
		bd.FusionScore = a.fusion
		obs := a.obs
		obs.Rank = a.fusion
		obs.ScoreBreakdown = bd
		combined = append(combined, obs)
	}

	sort.Slice(combined, func(i, j int) bool {
		if combined[i].Rank != combined[j].Rank {
			return combined[i].Rank > combined[j].Rank
		}
		return combined[i].ID < combined[j].ID // deterministic tie-break
	})

	if limit > 0 && len(combined) > limit {
		combined = combined[:limit]
	}
	return combined
}

// extractSearchTerms extracts clean search terms from a query string.
func extractSearchTerms(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	replacer := strings.NewReplacer("*", "", "^", "", "~", "", "+", "", "-", " ", "(", "", ")", "", `"`, "", "'", "")
	query = replacer.Replace(query)
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

// --- LongMemEval retrieval pack (REQ-LME-001) ---

// factUnitCap is the byte bound for one decomposed session unit before it is
// sentence-split into fact keys. Long sessions are decomposed into bounded
// units BEFORE indexing so no text is dropped (byte-exact reassembly).
const factUnitCap = 1200

// decomposeSession splits content into byte-exact bounded units: joining the
// returned units reproduces content byte-for-byte (no data loss), and every
// unit is at most unitCap bytes when possible (a hard cut is the final
// fallback for content with no sentence enders or whitespace inside the cap).
func decomposeSession(content string, unitCap int) []string {
	if unitCap <= 0 || len(content) <= unitCap {
		return []string{content}
	}
	cut := findSessionCut(content, unitCap)
	rest := decomposeSession(content[cut:], unitCap)
	return append([]string{content[:cut]}, rest...)
}

// findSessionCut picks the split point within the first unitCap bytes: the
// last sentence ender, else the last whitespace in the back half of the cap,
// else a hard cut at the cap.
func findSessionCut(content string, unitCap int) int {
	if i := strings.LastIndexAny(content[:unitCap], ".!?;\n"); i >= 0 {
		return i + 1
	}
	half := unitCap / 2
	if i := strings.LastIndexAny(content[half:unitCap], " \t\r\n"); i >= 0 {
		if cut := half + i + 1; cut > 0 && cut <= unitCap {
			return cut
		}
	}
	return unitCap
}

// extractFactKeys decomposes session content into bounded units and emits one
// fact key per sentence (trimmed, non-empty). No count cap is applied so every
// sentence of the original session becomes an indexable key.
func extractFactKeys(content string) []string {
	units := decomposeSession(content, factUnitCap)
	keys := make([]string, 0, len(units))
	for _, unit := range units {
		for _, sentence := range splitFactSentences(unit) {
			if trimmed := strings.TrimSpace(sentence); trimmed != "" {
				keys = append(keys, trimmed)
			}
		}
	}
	return keys
}

// splitFactSentences cuts text after each sentence ender, keeping the ender in
// the preceding sentence so the pieces remain substrings of the original text.
func splitFactSentences(text string) []string {
	var sentences []string
	var builder strings.Builder
	for _, r := range text {
		builder.WriteRune(r)
		if r == '.' || r == '!' || r == '?' || r == ';' || r == '\n' {
			sentences = append(sentences, builder.String())
			builder.Reset()
		}
	}
	if builder.Len() > 0 {
		sentences = append(sentences, builder.String())
	}
	return sentences
}

// factStopWords are interrogative/auxiliary terms pruned from fact-key queries
// so "What is the name of ..." matches the fact that states the answer without
// requiring the interrogative vocabulary to appear in the session text.
var factStopWords = map[string]bool{
	"what": true, "is": true, "was": true, "were": true, "are": true,
	"am": true, "the": true, "a": true, "an": true, "in": true, "on": true,
	"at": true, "for": true, "of": true, "and": true, "or": true, "to": true,
	"her": true, "his": true, "its": true, "did": true, "does": true,
	"do": true, "has": true, "have": true, "had": true, "when": true,
	"where": true, "why": true, "who": true, "how": true, "which": true,
	"that": true, "this": true, "these": true, "those": true, "it": true,
	"they": true, "them": true, "he": true, "she": true, "we": true,
	"you": true, "i": true, "my": true, "your": true, "our": true,
	"their": true, "be": true, "been": true, "being": true, "by": true,
	"with": true, "from": true, "as": true, "if": true, "into": true,
	"about": true, "during": true, "before": true, "after": true,
}

// sanitizeFactFTS builds the FTS5 MATCH expression for the fact-key table:
// same operator stripping and quoting as sanitizeFTS, but interrogative/aux
// stop words are pruned first. Returns "" when nothing but stop words remains.
func sanitizeFactFTS(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	query = stripFTSOperators(query)
	query = strings.ReplaceAll(query, `"`, `'`)

	fields := strings.Fields(query)
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		if factStopWords[strings.ToLower(field)] {
			continue
		}
		terms = append(terms, field)
	}
	if len(terms) == 0 {
		return ""
	}
	for i, term := range terms {
		if i == len(terms)-1 {
			terms[i] = fmt.Sprintf(`"%s*"`, term)
		} else {
			terms[i] = fmt.Sprintf(`"%s"`, term)
		}
	}
	return strings.Join(terms, " AND ")
}

// createFactKeyTables lazily creates the store-owned auxiliary tables. They
// are intentionally OUTSIDE the migration set: migrations are raw-SQL
// checksum-pinned, so the fact index must never mutate them.
func (s *Store) createFactKeyTables(ctx context.Context) error {
	stmts := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS search_fact_keys USING fts5(observation_id UNINDEXED, fact_text, tokenize='porter unicode61')`,
		`CREATE TABLE IF NOT EXISTS search_fact_meta (observation_id INTEGER PRIMARY KEY, source_updated_at TEXT NOT NULL)`,
	}
	for _, ddl := range stmts {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("fact key ddl: %w", err)
		}
	}
	return nil
}

// reconcileFactKeysLocked brings search_fact_keys in sync with the live
// observations table: hard/soft-deleted ids are purged first, then new or
// updated observations are re-keyed in a single transaction. Caller holds
// factMu.
func (s *Store) reconcileFactKeysLocked(ctx context.Context) error {
	stale, err := s.staleFactIDs(ctx)
	if err != nil {
		return err
	}
	for _, chunk := range chunkIDs(stale, 500) {
		if err := s.deleteFactIDs(ctx, chunk); err != nil {
			return err
		}
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id, COALESCE(o.content, ''), COALESCE(o.updated_at, '')
		FROM observations o
		LEFT JOIN search_fact_meta m ON m.observation_id = o.id
		WHERE o.deleted_at IS NULL
		  AND (m.observation_id IS NULL
		       OR COALESCE(m.source_updated_at, '') <> COALESCE(o.updated_at, ''))
		ORDER BY o.id`)
	if err != nil {
		return fmt.Errorf("fact key changed scan: %w", err)
	}
	type changedObs struct {
		id        int64
		content   string
		updatedAt string
	}
	var changed []changedObs
	for rows.Next() {
		var c changedObs
		if err := rows.Scan(&c.id, &c.content, &c.updatedAt); err != nil {
			_ = rows.Close()
			return fmt.Errorf("fact key changed scan row: %w", err)
		}
		changed = append(changed, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("fact key changed iterate: %w", err)
	}
	_ = rows.Close()
	if len(changed) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("fact key reconcile tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, c := range changed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_fact_keys WHERE observation_id = ?`, c.id); err != nil {
			return fmt.Errorf("fact key replace: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO search_fact_meta (observation_id, source_updated_at)
			VALUES (?, ?)
			ON CONFLICT(observation_id) DO UPDATE SET source_updated_at = excluded.source_updated_at`,
			c.id, c.updatedAt); err != nil {
			return fmt.Errorf("fact meta upsert: %w", err)
		}
		for _, key := range extractFactKeys(c.content) {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO search_fact_keys (observation_id, fact_text) VALUES (?, ?)`,
				c.id, key); err != nil {
				return fmt.Errorf("fact key insert: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("fact key reconcile commit: %w", err)
	}
	return nil
}

// staleFactIDs returns meta rows whose observation is hard- or soft-deleted.
func (s *Store) staleFactIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.observation_id
		FROM search_fact_meta m
		LEFT JOIN observations o ON o.id = m.observation_id
		WHERE o.id IS NULL OR o.deleted_at IS NOT NULL
		ORDER BY m.observation_id`)
	if err != nil {
		return nil, fmt.Errorf("fact key stale scan: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("fact key stale row: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) deleteFactIDs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM search_fact_keys WHERE observation_id IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("fact key purge: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM search_fact_meta WHERE observation_id IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("fact meta purge: %w", err)
	}
	return nil
}

func chunkIDs(ids []int64, size int) [][]int64 {
	var chunks [][]int64
	for len(ids) > size {
		chunks = append(chunks, ids[:size])
		ids = ids[size:]
	}
	if len(ids) > 0 {
		chunks = append(chunks, ids)
	}
	return chunks
}

// ensureFactKeys runs lazy DDL once and reconciles per call. It reports
// whether fact-key retrieval is usable; every failure degrades to
// session-text-only retrieval (never fails the search).
func (s *Store) ensureFactKeys(ctx context.Context) bool {
	s.factMu.Lock()
	defer s.factMu.Unlock()
	if !s.factDDLReady {
		if err := s.createFactKeyTables(ctx); err != nil {
			log.Printf("search: fact key DDL unavailable, degrading to session text: %v", err)
			return false
		}
		s.factDDLReady = true
	}
	if err := s.reconcileFactKeysLocked(ctx); err != nil {
		log.Printf("search: fact key reconcile failed, degrading to session text: %v", err)
		return false
	}
	return true
}

// searchFactKeys queries the fact-key FTS table and emits a true ranked list
// ordered by bm25 rank. Errors are non-fatal: they log and return nil so the
// remaining ranked lists still fuse.
func (s *Store) searchFactKeys(ctx context.Context, query string, opts domain.SearchOptions, limit int) []*domain.SearchResult {
	ftsQuery := sanitizeFactFTS(query)
	if ftsQuery == "" {
		return nil
	}
	filterFrag, filterArgs := applyFilterClauses(opts)
	// bm25() is only valid while the FTS5 table is the direct scan target and
	// rejects every GROUP BY context in this SQLite build, so rank rows in the
	// subquery, join observations outside, and dedupe in Go.
	baseQuery := `
		SELECT
			o.id, o.title, o.content, o.type, o.project, o.scope, o.session_id,
			o.topic_key, o.confidence, o.source, o.tags, o.created_at, o.updated_at,
			f.rank
		FROM (
			SELECT observation_id, bm25(search_fact_keys) AS rank
			FROM search_fact_keys
			WHERE search_fact_keys MATCH ?
			ORDER BY rank
		) f
		JOIN observations o ON o.id = f.observation_id
		WHERE o.deleted_at IS NULL
	` + filterFrag + `
		ORDER BY f.rank, o.id LIMIT ?`
	args := append([]any{ftsQuery}, filterArgs...)
	// Over-fetch: the rows are one per fact key, so many collapse onto the
	// same observation during the Go-side first-seen dedupe below.
	args = append(args, limit*20)

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		log.Printf("search: fact key search non-fatal error: %v", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var results []*domain.SearchResult
	seen := make(map[int64]bool, limit)
	for rows.Next() {
		result, err := s.scanSearchResult(rows)
		if err != nil {
			log.Printf("search: fact key scan non-fatal error: %v", err)
			return nil
		}
		if seen[result.ID] {
			continue
		}
		seen[result.ID] = true
		result.ScoreBreakdown = domain.SearchScoreBreakdown{Strategy: "fact_keys"}
		result.Content = previewContent(query, result.Content, answerSpanPreviewChars)
		results = append(results, result)
		if len(results) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("search: fact key iterate non-fatal error: %v", err)
		return nil
	}
	return results
}

// factKeyRankedList gates fact-key retrieval on the reconcile outcome so a
// degraded (unusable) fact index contributes no ranked list.
func (s *Store) factKeyRankedList(ctx context.Context, query string, opts domain.SearchOptions, limit int, factReady bool) []*domain.SearchResult {
	if !factReady {
		return nil
	}
	return s.searchFactKeys(ctx, query, opts, limit)
}

// --- Time-aware query expansion (REQ-LME-001) ---

// TimeRangeInferencer infers the time range a query implies. The second
// return value is false when the query carries no temporal signal (no
// pruning). Store.TimeRangeInferencer is the LLM-backed seam; nil selects the
// offline zero-CGO heuristic.
type TimeRangeInferencer func(query string, reference time.Time) (*domain.TimeRange, bool)

var (
	isoDatePattern     = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	monthNamePattern   = regexp.MustCompile(`(?i)\b(january|february|march|april|may|june|july|august|september|october|november|december)\b`)
	yearPattern        = regexp.MustCompile(`\b((?:19|20)\d{2})\b`)
	relativeDayPattern = regexp.MustCompile(`(?i)\b(today|yesterday|tomorrow)\b`)
	lastUnitPattern    = regexp.MustCompile(`(?i)\blast\s+(week|month|year)\b`)
)

var monthNumbers = map[string]time.Month{
	"january": time.January, "february": time.February, "march": time.March,
	"april": time.April, "may": time.May, "june": time.June,
	"july": time.July, "august": time.August, "september": time.September,
	"october": time.October, "november": time.November, "december": time.December,
}

// heuristicTimeRange is the ordered, offline, zero-CGO temporal pattern
// matcher. Order matters: ISO date, month+year, relative day, last unit,
// bare year. No match means no temporal signal (nil, false) — never a guess.
func heuristicTimeRange(query string, reference time.Time) (*domain.TimeRange, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, false
	}

	if m := isoDatePattern.FindStringSubmatch(query); m != nil {
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		if month >= 1 && month <= 12 && day >= 1 && day <= 31 {
			start := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
			return &domain.TimeRange{From: start, To: start.AddDate(0, 0, 1)}, true
		}
	}

	if monthMatch := monthNamePattern.FindStringSubmatch(query); monthMatch != nil {
		if yearMatch := yearPattern.FindStringSubmatch(query); yearMatch != nil {
			month, known := monthNumbers[strings.ToLower(monthMatch[1])]
			year, err := strconv.Atoi(yearMatch[1])
			if known && err == nil {
				start := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
				return &domain.TimeRange{From: start, To: start.AddDate(0, 1, 0)}, true
			}
		}
	}

	utc := reference.UTC()
	dayStart := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	if m := relativeDayPattern.FindStringSubmatch(query); m != nil {
		start := dayStart
		switch strings.ToLower(m[1]) {
		case "yesterday":
			start = dayStart.AddDate(0, 0, -1)
		case "tomorrow":
			start = dayStart.AddDate(0, 0, 1)
		}
		return &domain.TimeRange{From: start, To: start.AddDate(0, 0, 1)}, true
	}

	if m := lastUnitPattern.FindStringSubmatch(query); m != nil {
		var from time.Time
		switch strings.ToLower(m[1]) {
		case "week":
			from = reference.AddDate(0, 0, -7)
		case "month":
			from = reference.AddDate(0, -1, 0)
		case "year":
			from = reference.AddDate(-1, 0, 0)
		}
		return &domain.TimeRange{From: from, To: reference}, true
	}

	if m := yearPattern.FindStringSubmatch(query); m != nil {
		year, err := strconv.Atoi(m[1])
		if err == nil {
			start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
			return &domain.TimeRange{From: start, To: start.AddDate(1, 0, 0)}, true
		}
	}

	return nil, false
}

// pruneListsByTimeRange drops candidates outside the inferred time range from
// EVERY ranked list before fusion. No signal, a nil range, or an inverted
// range returns the lists untouched. A candidate with a missing or malformed
// timestamp (zero time) is always KEPT — the unpruned fallback of REQ-LME-001.
func (s *Store) pruneListsByTimeRange(lists []rankedList, query string) []rankedList {
	infer := s.TimeRangeInferencer
	if infer == nil {
		infer = heuristicTimeRange
	}
	tr, ok := infer(query, time.Now())
	if !ok || tr == nil {
		return lists
	}
	if !tr.From.IsZero() && !tr.To.IsZero() && tr.From.After(tr.To) {
		return lists
	}

	pruned := make([]rankedList, len(lists))
	for i, list := range lists {
		items := make([]*domain.SearchResult, 0, len(list.items))
		for _, item := range list.items {
			if timeRangeContains(tr, eventTimeOf(item)) {
				items = append(items, item)
			}
		}
		pruned[i] = rankedList{name: list.name, items: items}
	}
	return pruned
}

// eventTimeOf prefers CreatedAt (when the event happened), falling back to
// UpdatedAt.
func eventTimeOf(r *domain.SearchResult) time.Time {
	if !r.CreatedAt.IsZero() {
		return r.CreatedAt
	}
	return r.UpdatedAt
}

// timeRangeContains reports whether t falls inside tr; zero bounds are open
// sides and a zero t (missing/malformed timestamp) is always contained.
func timeRangeContains(tr *domain.TimeRange, t time.Time) bool {
	if t.IsZero() {
		return true
	}
	if !tr.From.IsZero() && t.Before(tr.From) {
		return false
	}
	if !tr.To.IsZero() && t.After(tr.To) {
		return false
	}
	return true
}

// Ensure Store implements domain.SearchRepository
var _ domain.SearchRepository = (*Store)(nil)
