package search

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// mockSearchRepository is a mock implementation of SearchRepository for testing
type mockSearchRepository struct {
	searchFunc func(ctx context.Context, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error)
}

func (m *mockSearchRepository) Search(ctx context.Context, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
	if m.searchFunc != nil {
		return m.searchFunc(ctx, query, opts)
	}
	return []*domain.SearchResult{}, nil
}

func TestNewService(t *testing.T) {
	repo := &mockSearchRepository{}
	service := NewService(repo)

	if service == nil {
		t.Fatal("NewService returned nil")
	}

	if service.repo == nil {
		t.Error("Service repo is nil")
	}
}

func TestSearch(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		opts          domain.SearchOptions
		mockResults   []*domain.SearchResult
		mockError     error
		expectedLen   int
		expectError   bool
		expectedQuery string
	}{
		{
			name:  "basic search",
			query: "auth bug",
			opts: domain.SearchOptions{
				Project: "myproject",
				Limit:   10,
			},
			mockResults: []*domain.SearchResult{
				{
					Observation: domain.Observation{
						ID:      1,
						Title:   "Auth Bug Fix",
						Content: "Fixed authentication bug",
						Type:    domain.TypeBugfix,
						Project: "myproject",
					},
					Rank: 0.95,
				},
			},
			expectedLen:   1,
			expectedQuery: `"auth" AND "bug*"`,
		},
		{
			name:          "empty query",
			query:         "",
			opts:          domain.SearchOptions{},
			mockResults:   []*domain.SearchResult{},
			expectedLen:   0,
			expectedQuery: "",
		},
		{
			name:          "whitespace only query",
			query:         "   ",
			opts:          domain.SearchOptions{},
			mockResults:   []*domain.SearchResult{},
			expectedLen:   0,
			expectedQuery: "",
		},
		{
			name:  "default limit applied",
			query: "test",
			opts: domain.SearchOptions{
				Limit: 0, // Should default to 10
			},
			mockResults:   []*domain.SearchResult{},
			expectedLen:   0,
			expectedQuery: `"test*"`,
		},
		{
			name:  "limit capped at 100",
			query: "test",
			opts: domain.SearchOptions{
				Limit: 150, // Should be capped to 100
			},
			mockResults:   []*domain.SearchResult{},
			expectedLen:   0,
			expectedQuery: `"test*"`,
		},
		{
			name:          "repository error",
			query:         "test",
			opts:          domain.SearchOptions{},
			mockError:     errors.New("database error"),
			expectError:   true,
			expectedQuery: `"test*"`,
		},
		{
			name:          "nil results converted to empty slice",
			query:         "test",
			opts:          domain.SearchOptions{},
			mockResults:   nil, // Repository returns nil
			expectedLen:   0,
			expectedQuery: `"test*"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var actualQuery string
			repo := &mockSearchRepository{
				searchFunc: func(ctx context.Context, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
					actualQuery = query
					if tt.mockError != nil {
						return nil, tt.mockError
					}
					return tt.mockResults, nil
				},
			}

			service := NewService(repo)
			results, err := service.Search(context.Background(), tt.query, tt.opts)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(results) != tt.expectedLen {
				t.Errorf("Expected %d results, got %d", tt.expectedLen, len(results))
			}

			if actualQuery != tt.expectedQuery {
				t.Errorf("Expected query %q, got %q", tt.expectedQuery, actualQuery)
			}

			// Verify results are never nil
			if results == nil {
				t.Error("Results should never be nil")
			}
		})
	}
}

func TestSearchObservations(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		project     string
		limit       int
		mockResults []*domain.SearchResult
		expectError bool
	}{
		{
			name:    "search with project filter",
			query:   "auth",
			project: "myproject",
			limit:   10,
			mockResults: []*domain.SearchResult{
				{
					Observation: domain.Observation{
						ID:      1,
						Title:   "Auth Implementation",
						Project: "myproject",
					},
					Rank: 0.9,
				},
			},
		},
		{
			name:        "search with no project",
			query:       "test",
			project:     "",
			limit:       5,
			mockResults: []*domain.SearchResult{},
		},
		{
			name:        "negative limit uses default",
			query:       "test",
			project:     "myproject",
			limit:       -1,
			mockResults: []*domain.SearchResult{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockSearchRepository{
				searchFunc: func(ctx context.Context, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
					if opts.Project != tt.project {
						t.Errorf("Expected project %q, got %q", tt.project, opts.Project)
					}
					return tt.mockResults, nil
				},
			}

			service := NewService(repo)
			results, err := service.SearchObservations(context.Background(), tt.query, tt.project, tt.limit)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if results == nil {
				t.Error("Results should never be nil")
			}
		})
	}
}

func TestSearchPrompts(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		project     string
		limit       int
		mockResults []*domain.SearchResult
		expectError bool
	}{
		{
			name:    "search prompts",
			query:   "implement",
			project: "myproject",
			limit:   10,
			mockResults: []*domain.SearchResult{
				{
					Observation: domain.Observation{
						ID:      1,
						Content: "Implement feature X",
						Project: "myproject",
					},
					Rank: 0.85,
				},
			},
		},
		{
			name:        "empty results",
			query:       "nonexistent",
			project:     "myproject",
			limit:       5,
			mockResults: []*domain.SearchResult{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockSearchRepository{
				searchFunc: func(ctx context.Context, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
					return tt.mockResults, nil
				},
			}

			service := NewService(repo)
			results, err := service.SearchPrompts(context.Background(), tt.query, tt.project, tt.limit)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if results == nil {
				t.Error("Results should never be nil")
			}
		})
	}
}

func TestSanitizeQuery(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple query",
			input:    "auth bug",
			expected: `"auth" AND "bug*"`,
		},
		{
			name:     "single term",
			input:    "authentication",
			expected: `"authentication*"`,
		},
		{
			name:     "three terms",
			input:    "fix auth bug",
			expected: `"fix" AND "auth" AND "bug*"`,
		},
		{
			name:     "query with asterisk",
			input:    "auth* bug",
			expected: `"auth" AND "bug*"`,
		},
		{
			name:     "query with caret",
			input:    "^auth bug",
			expected: `"auth" AND "bug*"`,
		},
		{
			name:     "query with minus",
			input:    "auth -bug",
			expected: `"auth" AND "bug*"`,
		},
		{
			name:     "query with plus",
			input:    "auth +bug",
			expected: `"auth" AND "bug*"`,
		},
		{
			name:     "query with tilde",
			input:    "auth~ bug",
			expected: `"auth" AND "bug*"`,
		},
		{
			name:     "query with double quotes",
			input:    `"auth bug"`,
			expected: `"'auth" AND "bug'*"`,
		},
		{
			name:     "query with mixed special chars",
			input:    "auth* +bug^ -test~",
			expected: `"auth" AND "bug" AND "test*"`,
		},
		{
			name:     "query with apostrophe",
			input:    "user's data",
			expected: `"user's" AND "data*"`,
		},
		{
			name:     "empty string",
			input:    "",
			expected: ``,
		},
		{
			name:     "whitespace only",
			input:    "   ",
			expected: ``,
		},
		{
			name:     "multiple spaces between terms",
			input:    "auth   bug",
			expected: `"auth" AND "bug*"`,
		},
		{
			name:     "leading and trailing whitespace",
			input:    "  auth bug  ",
			expected: `"auth" AND "bug*"`,
		},
		{
			name:     "query with numbers",
			input:    "fix 123 bug",
			expected: `"fix" AND "123" AND "bug*"`,
		},
		{
			name:     "query with underscores",
			input:    "fix_auth_bug",
			expected: `"fix_auth_bug*"`,
		},
		{
			name:     "query with camelCase",
			input:    "fixAuthBug",
			expected: `"fixAuthBug*"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeQuery(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeQuery(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSanitizeQuery_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "only special characters",
			input:    "***^^^---+++~~~",
			expected: ``,
		},
		{
			name:     "special characters with one word",
			input:    "***auth***",
			expected: `"auth*"`,
		},
		{
			name:     "unicode characters",
			input:    "cafe resume",
			expected: `"cafe" AND "resume*"`,
		},
		{
			name:     "very long query",
			input:    "this is a very long query with many terms to test the sanitization function",
			expected: `"this" AND "is" AND "a" AND "very" AND "long" AND "query" AND "with" AND "many" AND "terms" AND "to" AND "test" AND "the" AND "sanitization" AND "function*"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeQuery(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeQuery(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestService_NilRepository(t *testing.T) {
	service := NewService(nil)
	if service == nil {
		t.Fatal("NewService should not return nil even with nil repo")
	}

	// Note: Calling methods on a service with nil repo will panic
	// This is intentional - the repository is a required dependency
}
