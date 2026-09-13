package payload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// DefaultExternalizeThreshold is the threshold (100 KB) above which raw content
// is externalized to transient SQLite FTS5 storage instead of returning it to the LLM prompt.
const DefaultExternalizeThreshold = 100 * 1024

// EstimateTokens calculates an approximate token count based on typical byte/character ratios (~4 chars/token).
func EstimateTokens(content string) int {
	chars := len(content)
	if chars == 0 {
		return 0
	}
	tokens := chars / 4
	if tokens == 0 {
		return 1
	}
	return tokens
}

// ShouldExternalize checks if a text's byte length meets or exceeds the threshold.
func ShouldExternalize(byteCount int, threshold ...int) bool {
	limit := DefaultExternalizeThreshold
	if len(threshold) > 0 && threshold[0] > 0 {
		limit = threshold[0]
	}
	return byteCount >= limit
}

// SearchResult represents a snippet match from a transient payload FTS5 search.
type SearchResult struct {
	Snippet   string  `json:"snippet"`
	Rank      float64 `json:"rank"`
	ByteStart int     `json:"byte_start"`
	ByteEnd   int     `json:"byte_end"`
}

// TransientPayload represents an externalized payload stored in SQLite.
type TransientPayload struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"session_id"`
	Project     string    `json:"project"`
	SourceTool  string    `json:"source_tool"`
	ContentType string    `json:"content_type"`
	ByteCount   int       `json:"byte_count"`
	TokensSaved int       `json:"tokens_saved"`
	Snippet     string    `json:"snippet"`
	Content     string    `json:"content,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// NewTransientPayload constructs a TransientPayload, generating an ID and creating a compact snippet.
func NewTransientPayload(sessionID, project, sourceTool, content string) *TransientPayload {
	id := generateID()
	byteCount := len(content)
	totalTokens := EstimateTokens(content)

	snippet := buildSnippet(content, 20)
	snippetTokens := EstimateTokens(snippet)

	tokensSaved := totalTokens - snippetTokens
	if tokensSaved < 0 {
		tokensSaved = 0
	}

	return &TransientPayload{
		ID:          id,
		SessionID:   sessionID,
		Project:     project,
		SourceTool:  sourceTool,
		ContentType: "text/plain",
		ByteCount:   byteCount,
		TokensSaved: tokensSaved,
		Snippet:     snippet,
		Content:     content,
		CreatedAt:   time.Now().UTC(),
	}
}

func buildSnippet(content string, maxLines int) string {
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	if totalLines <= maxLines {
		return content
	}

	head := strings.Join(lines[:maxLines], "\n")
	remaining := totalLines - maxLines
	return fmt.Sprintf("%s\n\n[... %d more lines (%d total bytes) externalized to transient store ...]", head, remaining, len(content))
}

func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("payload-%d", time.Now().UnixNano())
	}
	return "payload-" + hex.EncodeToString(b)
}

// Store defines persistence operations for transient payloads.
type Store interface {
	Save(ctx context.Context, p *TransientPayload) error
	Get(ctx context.Context, id string) (*TransientPayload, error)
	Search(ctx context.Context, id string, query string, limit int) ([]SearchResult, error)
	PurgeSession(ctx context.Context, sessionID string) error
	Stats(ctx context.Context, sessionID string) (totalSavedTokens int, totalBytes int, count int, err error)
}
