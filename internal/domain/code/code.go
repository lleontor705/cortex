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
	RelationCalls        = "calls"
	RelationImports      = "imports"
	RelationImplements   = "implements"
	RelationDefines      = "defines"
	RelationUses         = "uses"
	RelationContains     = "contains"
	RelationExtends      = "extends"
	RelationInstantiates = "instantiates"
	RelationUsesType     = "uses_type"
	RelationReferences   = "references"
	RelationExports      = "exports"
)

// Standard symbol kinds.
const (
	KindFunc      = "func"
	KindMethod    = "method"
	KindStruct    = "struct"
	KindInterface = "interface"
	KindClass     = "class"
	KindModule    = "module"
	KindPackage   = "package"
	KindType      = "type"
	KindTable     = "table"
	KindEnum      = "enum"
	KindVariable  = "variable"
	KindConstant  = "constant"
)

// Standard visibility levels.
const (
	VisibilityPublic    = "public"
	VisibilityPrivate   = "private"
	VisibilityProtected = "protected"
	VisibilityInternal  = "internal"
)

// Parameter represents a typed input parameter of a function or method.
type Parameter struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// Symbol represents an extracted code entity with rich semantic metadata.
type Symbol struct {
	ID          string         `json:"id"`                    // Deterministic unique ID (e.g. "func:domain.NewService")
	Project     string         `json:"project"`               // Project namespace
	FilePath    string         `json:"file_path"`             // Relative file path (e.g. "internal/domain/service.go")
	LineNumber  int            `json:"line_number"`           // 1-based start line
	EndLine     int            `json:"end_line,omitempty"`    // 1-based end line
	StartColumn int            `json:"start_col,omitempty"`   // Start column offset
	EndColumn   int            `json:"end_col,omitempty"`     // End column offset
	Kind        string         `json:"kind"`                  // "func", "method", "struct", "class", "interface", "table", "module", "enum"
	Name        string         `json:"name"`                  // Symbol identifier (e.g. "CalculateBlastRadius")
	PackageName string         `json:"package_name"`          // Package or namespace (e.g. "domain")
	ParentID    string         `json:"parent_id,omitempty"`   // Hierarchical parent ID (e.g. class ID for methods, struct ID for fields)
	Visibility  string         `json:"visibility,omitempty"`  // "public", "private", "protected", "internal"
	Signature   string         `json:"signature,omitempty"`   // Full declaration signature
	DocSummary  string         `json:"doc_summary,omitempty"` // GoDoc / JSDoc / Docstring description
	Parameters  []Parameter    `json:"parameters,omitempty"`  // Ordered list of typed parameters
	ReturnType  string         `json:"return_type,omitempty"` // Explicit return type
	Complexity  int            `json:"complexity,omitempty"`  // Cyclomatic complexity score (branches + 1)
	Metadata    map[string]any `json:"metadata,omitempty"`    // Language-specific extras (async, static, decorators, fields)
	FileHash    string         `json:"file_hash,omitempty"`   // SHA-256 of file for incremental scanning
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// Relation represents a directed architectural edge between two code symbols.
type Relation struct {
	ID         int64     `json:"id,omitempty"`
	Project    string    `json:"project"`
	SourceID   string    `json:"source_id"`  // Caller / Importer symbol ID
	TargetID   string    `json:"target_id"`  // Callee / Imported symbol ID
	Relation   string    `json:"relation"`   // "calls", "imports", "implements", "defines", "uses", "contains", "extends", "instantiates", "uses_type", "references", "exports"
	Confidence float64   `json:"confidence"` // 1.0 = EXTRACTED, 0.85 = INFERRED, 0.5 = AMBIGUOUS
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

// CodeBlastRadius represents the impacted symbols, callers, and files when a code entity changes.
type CodeBlastRadius struct {
	Target         string        `json:"target"`
	TargetKind     string        `json:"target_kind"` // "symbol", "file", "git_diff"
	RootNode       string        `json:"root_node,omitempty"`
	ChangedFiles   []string      `json:"changed_files,omitempty"`
	DirectCallers  []string      `json:"direct_callers"`
	ImpactedFiles  []string      `json:"impacted_files"`
	TotalImpacted  []string      `json:"total_impacted"`
	BlastRadiusPct float64       `json:"blast_radius_pct"`
	AffectedCycles []ImportCycle `json:"affected_cycles,omitempty"`
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
