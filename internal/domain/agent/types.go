// Package agent implements the transport-neutral, read-only conversational RAG domain.
package agent

import "context"

const (
	MaxQuestionBytes       = 8 * 1024
	MaxHistoryMessages     = 12
	MaxHistoryMessageBytes = 4 * 1024
	MaxHistoryBytes        = 24 * 1024
	MaxEvidencePerCorpus   = 8
	MaxContextBytes        = 32 * 1024
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

type ErrorCode string

const (
	ErrorInvalidRequest      ErrorCode = "invalid_request"
	ErrorInvalidHistoryRole  ErrorCode = "invalid_history_role"
	ErrorHistoryTooLarge     ErrorCode = "history_too_large"
	ErrorQuestionTooLarge    ErrorCode = "question_too_large"
	ErrorProviderUnavailable ErrorCode = "provider_unavailable"
)

type Error struct {
	Code ErrorCode
	Err  error
}

func (e *Error) Error() string { return string(e.Code) }
func (e *Error) Unwrap() error { return e.Err }

type Scope struct {
	TenantID    string `json:"-"`
	WorkspaceID string `json:"-"`
	Project     string `json:"project"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Scope    Scope     `json:"-"`
	Question string    `json:"question"`
	History  []Message `json:"history,omitempty"`
}

type EvidenceKind string

const (
	EvidenceMemory EvidenceKind = "memory"
	EvidenceCode   EvidenceKind = "code"
)

// Evidence is trusted scope-wise by its adapter. Content is prompt-only and never returned.
type Evidence struct {
	Kind      EvidenceKind `json:"type"`
	Title     string       `json:"title"`
	Path      string       `json:"path,omitempty"`
	LineStart int          `json:"line_start,omitempty"`
	LineEnd   int          `json:"line_end,omitempty"`
	Content   string       `json:"-"`
	Score     float64      `json:"-"`
}

type Source struct {
	Handle    string       `json:"handle"`
	Type      EvidenceKind `json:"type"`
	Title     string       `json:"title"`
	Path      string       `json:"path,omitempty"`
	LineStart int          `json:"line_start,omitempty"`
	LineEnd   int          `json:"line_end,omitempty"`
}

type Confidence struct {
	Level string  `json:"level"`
	Score float64 `json:"score"`
}

const (
	ConfidenceLow    = "low"
	ConfidenceMedium = "medium"
	ConfidenceHigh   = "high"

	DegradedMemoryUnavailable = "memory_unavailable"
	DegradedCodeUnavailable   = "code_unavailable"
	DegradedNoEvidence        = "no_authorized_evidence"
)

type RetrievalStatus struct {
	Tier             string                 `json:"tier,omitempty"`
	Stages           []RetrievalStageStatus `json:"stages,omitempty"`
	RefinementCount  int                    `json:"refinement_count,omitempty"`
	Generation       string                 `json:"generation,omitempty"`
	Degraded         []string               `json:"degraded"`
	InvalidCitations int                    `json:"invalid_citations,omitempty"`
}

// RetrievalStageStatus is the public, content-free projection of one
// retrieval stage. Generation remains empty until a trusted corpus generation
// is carried by the retrieval port; transports must never synthesize one from
// request data or expose internal checksums.
type RetrievalStageStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type Answer struct {
	Answer     string          `json:"answer"`
	Sources    []Source        `json:"sources"`
	Confidence Confidence      `json:"confidence"`
	Retrieval  RetrievalStatus `json:"retrieval"`
	Usage      CompletionUsage `json:"-"`
}

type CompletionUsage struct {
	InputTokens  int
	OutputTokens int
}

// Retriever has no write method and receives scope resolved by the server.
type Retriever interface {
	Retrieve(context.Context, Scope, string, int) ([]Evidence, error)
}

// CompletionProvider intentionally exposes no tools, URL, model or credentials.
type CompletionProvider interface {
	Complete(context.Context, CompletionRequest) (CompletionResult, error)
}

// StreamingCompletionProvider emits complete, provider-produced claims as soon
// as they can be parsed. Claims are still untrusted until Service resolves
// their handles against the evidence issued for the current request.
type StreamingCompletionProvider interface {
	Stream(context.Context, CompletionRequest, func(CompletionClaim) error) (CompletionUsage, error)
}

// StreamCallbacks is transport-neutral. Meta precedes every delta, Sources is
// emitted once after all claims have been validated, and the returned Answer is
// the canonical terminal representation shared with the JSON transport.
type StreamCallbacks struct {
	Meta    func(RetrievalStatus) error
	Delta   func(string) error
	Sources func([]Source) error
}

type CompletionRequest struct {
	SystemPrompt string
	UserPrompt   string
}

// CompletionClaim is the provider's smallest factual output unit. A claim is
// eligible for the public answer only when at least one handle was issued for
// this request and resolves to authorized evidence.
type CompletionClaim struct {
	Text            string   `json:"text"`
	CitationHandles []string `json:"citation_handles"`
}

type CompletionResult struct {
	Claims       []CompletionClaim
	InputTokens  int
	OutputTokens int
}

func (r CompletionResult) Usage() CompletionUsage {
	return CompletionUsage{InputTokens: r.InputTokens, OutputTokens: r.OutputTokens}
}
