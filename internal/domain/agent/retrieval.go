package agent

import "context"

// RetrievalTier is the bounded route selected for one scoped read.
type RetrievalTier string

const (
	RetrievalTierDirectFactual       RetrievalTier = "direct_factual"
	RetrievalTierSemanticHybrid      RetrievalTier = "semantic_hybrid"
	RetrievalTierMultiHopGraph       RetrievalTier = "multi_hop_graph"
	RetrievalTierArchitecturalGlobal RetrievalTier = "architectural_global"
	DegradedDenseUnavailable                       = "dense_unavailable"
)

// RetrievalStage reports only safe pipeline state; it never carries query,
// content, internal identifiers, or authorization details.
type RetrievalStage struct {
	Name   string
	Status string
	Count  int
}

// RetrievalTrace is transport-neutral metadata from the scoped retriever.
type RetrievalTrace struct {
	Tier     RetrievalTier
	Stages   []RetrievalStage
	Degraded []string
}

// RetrievalResult keeps evidence and its safe execution trace together.
type RetrievalResult struct {
	Evidence []Evidence
	Trace    RetrievalTrace
}

// ScopedRetriever is the single read-only retrieval port used by the agent.
// Server composition resolves tenant, workspace, and project authority before
// the implementation touches lexical, dense, or graph dependencies.
type ScopedRetriever interface {
	RetrieveScoped(context.Context, Scope, string, int) (RetrievalResult, error)
}
