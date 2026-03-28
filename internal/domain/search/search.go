// Package search provides FTS5-based full-text search business logic for Cortex.
//
// This package implements the search domain service, which provides a clean
// API layer on top of the SearchRepository. It handles query sanitization,
// option validation, and result formatting.
package search

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/lleontor705/cortex/internal/domain"
)

// Service provides business logic for full-text search operations.
// It wraps a SearchRepository to provide a clean API with query sanitization
// and result validation.
type Service struct {
	repo domain.SearchRepository
}

// NewService creates a new search service with the given repository.
func NewService(repo domain.SearchRepository) *Service {
	return &Service{
		repo: repo,
	}
}

// Search performs a full-text search with the given query and options.
// The query is sanitized for FTS5 before being passed to the repository.
//
// The method validates options and applies defaults:
//   - If limit is 0 or negative, defaults to 10
//   - If limit exceeds 100, it's capped at 100
//
// Returns search results ordered by relevance (rank).
func (s *Service) Search(ctx context.Context, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
	// Validate query
	if strings.TrimSpace(query) == "" {
		return []*domain.SearchResult{}, nil
	}

	// Apply defaults
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	if opts.Limit > 100 {
		opts.Limit = 100
	}

	// Sanitize query for FTS5
	sanitizedQuery := sanitizeQuery(query)

	// Update options with sanitized query
	opts.Query = sanitizedQuery

	// Perform search via repository
	results, err := s.repo.Search(ctx, sanitizedQuery, opts)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Ensure we never return nil
	if results == nil {
		results = []*domain.SearchResult{}
	}

	return results, nil
}

// SearchObservations searches only observations with the given filters.
// This is a convenience method that sets appropriate filters in SearchOptions.
//
// Parameters:
//   - query: the search query (will be sanitized for FTS5)
//   - project: optional project filter (empty string means no filter)
//   - limit: maximum number of results (defaults to 10 if <= 0)
//
// Returns observations matching the query, ordered by relevance.
func (s *Service) SearchObservations(ctx context.Context, query string, project string, limit int) ([]*domain.SearchResult, error) {
	opts := domain.SearchOptions{
		Project: project,
		Limit:   limit,
	}

	return s.Search(ctx, query, opts)
}

// SearchPrompts searches only user prompts with the given filters.
// This method is provided for future extensibility when prompts are
// indexed separately in FTS5.
//
// Parameters:
//   - query: the search query (will be sanitized for FTS5)
//   - project: optional project filter (empty string means no filter)
//   - limit: maximum number of results (defaults to 10 if <= 0)
//
// Returns prompts matching the query, ordered by relevance.
func (s *Service) SearchPrompts(ctx context.Context, query string, project string, limit int) ([]*domain.SearchResult, error) {
	// Note: In the current implementation, prompts are not separately indexed
	// This method is provided for future extensibility
	// For now, it searches all content types
	opts := domain.SearchOptions{
		Project: project,
		Limit:   limit,
	}

	return s.Search(ctx, query, opts)
}

// sanitizeQuery converts natural language queries to FTS5-compatible format.
//
// FTS5 has special operators and syntax that can cause errors if user input
// contains special characters. This function sanitizes the query by:
//  1. Removing FTS5 special operators (*, ^, -, +, ~)
//  2. Escaping double quotes
//  3. Wrapping each term in double quotes
//  4. Joining terms with AND operator
//  5. Adding prefix matching for the last term (to support partial matches)
//
// Examples:
//   - "fix auth bug" → `"fix" AND "auth" AND "bug*"`
//   - "JWT token" → `"JWT" AND "token*"`
//   - "user's data" → `"user's" AND "data*"`
func sanitizeQuery(query string) string {
	// Trim whitespace
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}

	// Remove FTS5 special operators
	// These characters have special meaning in FTS5 and can cause syntax errors
	re := regexp.MustCompile(`[*^~+-]`)
	query = re.ReplaceAllString(query, "")

	// Escape double quotes by replacing them with single quotes
	// This prevents breaking the quoted term syntax
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
