// Package code defines domain entities, ports, and analytics for static code
// graphs, symbol indexes, and architectural insights.
package code

import (
	"context"
	"time"
)

// Confidence levels for code relationships, matching Graphify standards.
const (
	ConfidenceExtracted = 1.0 // Stated directly in AST (e.g. import, direct call)
	ConfidenceInferred  = 0.8 // Deduced (e.g. 2-pass member call resolution)
	ConfidenceAmbiguous = 0.5 // Plausible but uncertain
)

// Standard relation types.
const (
	RelationCalls      = "calls"
	RelationImports    = "imports"
	RelationImplements = "implements"
	RelationDefines    = "defines"
	RelationUses       = "uses"
	RelationContains   = "contains"
)

// Standard symbol kinds.
const (
	KindFunc      = "func"
	KindStruct    = "struct"
	KindInterface = "interface"
	KindClass     = "class"
	KindModule    = "module"
	KindPackage   = "package"
	KindType      = "type"
	KindTable     = "table"
)

// Symbol represents an extracted code entity (function, struct, class, interface, etc.).
type Symbol struct {
	ID          string    `json:"id"`           // Deterministic unique hash or ID
	Project     string    `json:"project"`      // Project namespace
	FilePath    string    `json:"file_path"`    // Relative file path (e.g. "internal/cli/cli.go")
	LineNumber  int       `json:"line_number"`  // 1-based source line
	Kind        string    `json:"kind"`         // "func", "struct", "interface", "class", etc.
	Name        string    `json:"name"`         // Symbol identifier (e.g. "NewExtractor")
	PackageName string    `json:"package_name"` // Package or namespace
	Signature   string    `json:"signature"`    // Type signature or header
	DocSummary  string    `json:"doc_summary"`  // Extracted docstring / comment summary
	FileHash    string    `json:"file_hash"`    // SHA-256 of file for incremental scanning
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Relation represents a directed architectural edge between two code symbols.
type Relation struct {
	ID         int64     `json:"id,omitempty"`
	Project    string    `json:"project"`
	SourceID   string    `json:"source_id"`  // Caller / Importer symbol ID
	TargetID   string    `json:"target_id"`  // Callee / Imported symbol ID
	Relation   string    `json:"relation"`   // "calls", "imports", "implements", "defines", "uses"
	Confidence float64   `json:"confidence"` // 1.0 = EXTRACTED, 0.8 = INFERRED, 0.5 = AMBIGUOUS
	Reasoning  string    `json:"reasoning,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// SymbolFilter specifies criteria for querying symbols.
type SymbolFilter struct {
	Project     string
	FilePath    string
	Kind        string
	PackageName string
	Query       string
	Limit       int
	Offset      int
}

// CodeGraph encapsulates the complete structural graph for a project or component.
type CodeGraph struct {
	Project   string     `json:"project"`
	Symbols   []Symbol   `json:"symbols"`
	Relations []Relation `json:"relations"`
}

// GodNode identifies high-centrality architectural hubs.
type GodNode struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	FilePath  string  `json:"file_path"`
	Degree    int     `json:"degree"`
	InDegree  int     `json:"in_degree"`
	OutDegree int     `json:"out_degree"`
	Score     float64 `json:"score"`
}

// ImportCycle represents a circular dependency loop between files or modules.
type ImportCycle struct {
	ID        string   `json:"id"`
	Files     []string `json:"files"`
	CyclePath []string `json:"cycle_path"`
}

// CommunityCohesion measures the internal coupling density of a module or cluster.
type CommunityCohesion struct {
	CommunityID   int      `json:"community_id"`
	Label         string   `json:"label"`
	Members       []string `json:"members"`
	InternalEdges int      `json:"internal_edges"`
	PossibleEdges int      `json:"possible_edges"`
	CohesionScore float64  `json:"cohesion_score"` // 2*E / (N*(N-1))
}

// AnalyticsReport provides architectural diagnostic metrics.
type AnalyticsReport struct {
	Project         string              `json:"project"`
	TotalSymbols    int                 `json:"total_symbols"`
	TotalRelations  int                 `json:"total_relations"`
	TotalFiles      int                 `json:"total_files"`
	GodNodes        []GodNode           `json:"god_nodes"`
	ImportCycles    []ImportCycle       `json:"import_cycles"`
	Communities     []CommunityCohesion `json:"communities"`
	AverageCohesion float64             `json:"average_cohesion"`
	GeneratedAt     time.Time           `json:"generated_at"`
}

// Store defines storage operations for code symbols and relations.
type Store interface {
	SaveSymbols(ctx context.Context, symbols []Symbol) error
	GetSymbolByID(ctx context.Context, id string) (*Symbol, error)
	ListSymbols(ctx context.Context, filter SymbolFilter) ([]Symbol, error)
	CountSymbols(ctx context.Context, filter SymbolFilter) (int, error)
	DeleteSymbolsByProject(ctx context.Context, project string) error
	DeleteSymbolsByFile(ctx context.Context, project, filePath string) error

	SaveRelations(ctx context.Context, relations []Relation) error
	GetGraph(ctx context.Context, project string) (*CodeGraph, error)
	ListRelationsBySymbol(ctx context.Context, symbolID string) ([]Relation, error)
	DeleteRelationsByProject(ctx context.Context, project string) error
	DeleteRelationsByFile(ctx context.Context, project, filePath string) error
}

// Service coordinates code AST ingestion, querying, and analytics.
type Service interface {
	IngestPath(ctx context.Context, rootPath string, project string, maxFiles int) (*AnalyticsReport, error)
	GetGraph(ctx context.Context, project string) (*CodeGraph, error)
	ListSymbols(ctx context.Context, filter SymbolFilter) ([]Symbol, error)
	Analyze(ctx context.Context, project string) (*AnalyticsReport, error)
}
