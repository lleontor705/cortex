package server

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/domain"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	domaingraph "github.com/lleontor705/cortex/v2/internal/domain/graph"
	"github.com/lleontor705/cortex/v2/internal/embedding"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
	postgresstore "github.com/lleontor705/cortex/v2/internal/store/postgres"
)

const (
	agentStageOK                    = "ok"
	agentStageDegraded              = "degraded"
	agentStageSkipped               = "skipped"
	agentRRFConstant                = 60.0
	agentGraphMaxHops               = 2
	agentGraphMaxNodes              = 96
	agentGraphMaxEdges              = 192
	agentCRAGInsufficientConfidence = "crag_insufficient_confidence"
)

type agentGraphOperations interface {
	GetAgentGraphSnapshot(context.Context, string, string, []string, int, int, int) (*domain.GraphSubgraph, error)
	GetAgentCodeGraphSnapshot(context.Context, string, string, int, int, int) (*code.CodeGraph, error)
}

// These methods keep the production graph path on the same authenticated,
// context-bound Operations instance selected by middleware. The broad public
// Operations contract need not expose graph-specific acquisition to transports.
func (requestOperations) GetAgentGraphSnapshot(ctx context.Context, projectID, projectLabel string, seeds []string, hops, nodes, edges int) (*domain.GraphSubgraph, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	graphOps, ok := ops.(agentGraphOperations)
	if !ok {
		return nil, errors.New("server: authenticated graph operations are unavailable")
	}
	return graphOps.GetAgentGraphSnapshot(ctx, projectID, projectLabel, seeds, hops, nodes, edges)
}

func (requestOperations) GetAgentCodeGraphSnapshot(ctx context.Context, projectID, query string, hops, nodes, edges int) (*code.CodeGraph, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	graphOps, ok := ops.(agentGraphOperations)
	if !ok {
		return nil, errors.New("server: authenticated graph operations are unavailable")
	}
	return graphOps.GetAgentCodeGraphSnapshot(ctx, projectID, query, hops, nodes, edges)
}

// scopedAgentRetriever is the server's single deep retrieval module. It owns
// scope validation, authorized lexical and dense acquisition, fusion,
// reranking, and bounded evidence conversion.
type scopedAgentRetriever struct {
	ops        agentRetrievalOperations
	vectors    domain.VectorIndex
	embeddings embedding.Service
	summaries  *agentSummaryCache
}

func (r scopedAgentRetriever) RetrieveScoped(ctx context.Context, scope agentdomain.Scope, query string, limit int) (agentdomain.RetrievalResult, error) {
	first, err := r.retrieveScopedOnce(ctx, scope, query, limit)
	if err != nil {
		return agentdomain.RetrievalResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return agentdomain.RetrievalResult{}, err
	}
	grade, err := evaluateAgentCRAGResult(first.Evidence)
	if err != nil {
		return agentdomain.RetrievalResult{}, err
	}
	if grade != retrieval.ConfidenceGradeLow {
		first.Trace.Stages = append(first.Trace.Stages, agentdomain.RetrievalStage{Name: "crag", Status: agentStageOK})
		return first, nil
	}
	if err = ctx.Err(); err != nil {
		return agentdomain.RetrievalResult{}, err
	}
	refined := retrieval.RefineAgentCRAGQuery(query)
	second, err := r.retrieveScopedOnce(ctx, scope, refined, limit)
	if err != nil {
		return agentdomain.RetrievalResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return agentdomain.RetrievalResult{}, err
	}
	grade, err = evaluateAgentCRAGResult(second.Evidence)
	if err != nil {
		return agentdomain.RetrievalResult{}, err
	}
	status := agentStageOK
	if grade == retrieval.ConfidenceGradeLow {
		second.Evidence = nil
		status = agentStageDegraded
		second.Trace.Degraded = appendAgentCRAGDegradation(second.Trace.Degraded, agentCRAGInsufficientConfidence)
	}
	second.Trace.Stages = append(second.Trace.Stages, agentdomain.RetrievalStage{Name: "crag", Status: status, Count: 1})
	return second, nil
}

func (r scopedAgentRetriever) retrieveScopedOnce(ctx context.Context, scope agentdomain.Scope, query string, limit int) (agentdomain.RetrievalResult, error) {
	projectID, _ := ctx.Value(agentProjectIDKey{}).(string)
	if r.ops == nil || strings.TrimSpace(scope.TenantID) == "" || strings.TrimSpace(scope.WorkspaceID) == "" || strings.TrimSpace(scope.Project) == "" || strings.TrimSpace(projectID) == "" || strings.TrimSpace(query) == "" || limit <= 0 {
		return agentdomain.RetrievalResult{}, errors.New("agent retrieval scope unavailable")
	}

	tier := agentRetrievalTier(retrieval.ClassifyQueryComplexity(query))
	trace := agentdomain.RetrievalTrace{Tier: tier, Stages: make([]agentdomain.RetrievalStage, 0, 4), Degraded: []string{}}
	lexical, err := r.ops.SearchAgentObservations(ctx, projectID, scope.Project, query, domain.SearchOptions{
		Query: query, Project: scope.Project, Limit: limit,
	})
	if err != nil {
		return agentdomain.RetrievalResult{}, err
	}
	trace.Stages = append(trace.Stages, agentdomain.RetrievalStage{Name: "lexical", Status: agentStageOK, Count: len(lexical)})

	var memory []agentdomain.Evidence
	var dense []*domain.VectorSearchResult
	graphTier := tier == agentdomain.RetrievalTierMultiHopGraph || tier == agentdomain.RetrievalTierArchitecturalGlobal
	graphHandled := false
	if tier == agentdomain.RetrievalTierSemanticHybrid || graphTier {
		dense, err = r.semanticDense(ctx, scope, projectID, query, limit)
		if err != nil {
			if ctx.Err() != nil {
				return agentdomain.RetrievalResult{}, ctx.Err()
			}
			trace.Stages = append(trace.Stages, agentdomain.RetrievalStage{Name: "dense", Status: agentStageDegraded})
			trace.Degraded = append(trace.Degraded, agentdomain.DegradedDenseUnavailable)
			dense = nil
		} else {
			trace.Stages = append(trace.Stages, agentdomain.RetrievalStage{Name: "dense", Status: agentStageOK, Count: len(dense)})
		}
	} else {
		trace.Stages = append(trace.Stages, agentdomain.RetrievalStage{Name: "dense", Status: agentStageSkipped})
	}
	if tier == agentdomain.RetrievalTierSemanticHybrid {
		memory, err = semanticAgentEvidence(query, lexical, dense, limit)
		trace.Stages = append(trace.Stages, agentdomain.RetrievalStage{Name: "rrf_maxsim", Status: agentStageOK, Count: len(memory)})
	} else if graphTier {
		if len(dense) > 0 {
			memory, err = semanticAgentEvidence(query, lexical, dense, limit)
		} else {
			memory, err = lexicalAgentEvidence(lexical, limit)
		}
		if err == nil {
			var expanded []agentdomain.Evidence
			var degraded bool
			expanded, degraded, err = r.multiHopArchitecturalEvidence(ctx, scope, projectID, query, lexical, dense, limit, tier == agentdomain.RetrievalTierArchitecturalGlobal)
			if err == nil {
				memory = mergeAgentEvidence(memory, expanded, limit)
				graphHandled = true
				status := agentStageOK
				if degraded {
					status = agentStageDegraded
				}
				trace.Stages = append(trace.Stages, agentdomain.RetrievalStage{Name: "graph_ppr", Status: status, Count: len(expanded)})
			}
		}
	} else {
		memory, err = lexicalAgentEvidence(lexical, limit)
	}
	if tier == agentdomain.RetrievalTierArchitecturalGlobal {
		count := countAgentCommunityEvidence(memory)
		status := agentStageOK
		if count == 0 {
			status = agentStageDegraded
		}
		trace.Stages = append(trace.Stages, agentdomain.RetrievalStage{Name: "community_summary", Status: status, Count: count})
	}
	if err != nil {
		return agentdomain.RetrievalResult{}, err
	}
	var codeEvidence []agentdomain.Evidence
	var codeErr error
	if !graphHandled {
		codeEvidence, codeErr = (agentCodeRetriever{ops: r.ops}).Retrieve(ctx, scope, query, limit)
	}
	if codeErr != nil {
		trace.Stages = append(trace.Stages, agentdomain.RetrievalStage{Name: "code", Status: agentStageDegraded})
		trace.Degraded = append(trace.Degraded, agentdomain.DegradedCodeUnavailable)
	} else {
		for i := range codeEvidence {
			codeEvidence[i].Kind = agentdomain.EvidenceCode
		}
		trace.Stages = append(trace.Stages, agentdomain.RetrievalStage{Name: "code", Status: agentStageOK, Count: len(codeEvidence)})
	}
	return agentdomain.RetrievalResult{Evidence: append(memory, codeEvidence...), Trace: trace}, nil
}

func evaluateAgentCRAGResult(evidence []agentdomain.Evidence) (retrieval.ConfidenceGrade, error) {
	scores := make([]float64, len(evidence))
	for i, item := range evidence {
		scores[i] = item.Score
	}
	return retrieval.EvaluateAgentCRAG(scores, retrieval.DefaultCRAGConfig())
}

func appendAgentCRAGDegradation(current []string, reason string) []string {
	for _, existing := range current {
		if existing == reason {
			return current
		}
	}
	return append(current, reason)
}

func (r scopedAgentRetriever) multiHopArchitecturalEvidence(ctx context.Context, scope agentdomain.Scope, projectID, query string, lexical []*domain.SearchResult, dense []*domain.VectorSearchResult, limit int, architectural bool) ([]agentdomain.Evidence, bool, error) {
	graphOps, ok := r.ops.(agentGraphOperations)
	if !ok {
		return nil, false, errors.New("agent graph retrieval boundary unavailable")
	}
	degraded := false
	memorySeeds, err := agentHybridGraphSeeds(lexical, dense, nil)
	if err != nil {
		return nil, false, err
	}
	seedPublicIDs := make([]string, 0, len(memorySeeds))
	for id := range memorySeeds {
		if strings.HasPrefix(id, "observation:") {
			seedPublicIDs = append(seedPublicIDs, strings.TrimPrefix(id, "observation:"))
		}
	}
	sort.Strings(seedPublicIDs)
	snapshot, err := graphOps.GetAgentGraphSnapshot(ctx, projectID, scope.Project, seedPublicIDs, agentGraphMaxHops, agentGraphMaxNodes, agentGraphMaxEdges)
	if err != nil {
		if !isAgentGraphAvailability(err) {
			return nil, false, err
		}
		degraded, snapshot = true, &domain.GraphSubgraph{}
	}
	if snapshot == nil {
		return nil, false, errors.New("agent memory graph scope uncertain")
	}
	codeGraph, err := graphOps.GetAgentCodeGraphSnapshot(ctx, projectID, query, agentGraphMaxHops, agentGraphMaxNodes, agentGraphMaxEdges)
	if err != nil {
		if !isAgentGraphAvailability(err) {
			return nil, false, err
		}
		degraded, codeGraph = true, &code.CodeGraph{Project: projectID}
	}
	if codeGraph == nil || codeGraph.Project != projectID {
		return nil, false, errors.New("agent code graph scope uncertain")
	}
	seedSymbols := make([]code.Symbol, 0)
	for _, symbol := range codeGraph.Symbols {
		if seeded, _ := symbol.Metadata["agent_seed"].(bool); seeded {
			seedSymbols = append(seedSymbols, symbol)
		}
	}
	seeds, err := agentHybridGraphSeeds(lexical, dense, seedSymbols)
	if err != nil {
		return nil, false, err
	}

	nodes := make([]domaingraph.GraphAnalyticsNode, 0, len(snapshot.Nodes)+len(codeGraph.Symbols))
	edges := make([]domaingraph.GraphAnalyticsEdge, 0, len(snapshot.Edges)+len(codeGraph.Relations))
	known := make(map[string]bool, len(snapshot.Nodes)+len(codeGraph.Symbols))
	memoryNodes := make(map[string]domain.GraphNode)
	for _, node := range snapshot.Nodes {
		if node.Kind != "observation" || node.Project != projectID {
			return nil, false, errors.New("agent memory graph scope mismatch")
		}
		if known[node.ID] {
			continue
		}
		known[node.ID], memoryNodes[node.ID] = true, node
		nodes = append(nodes, domaingraph.GraphAnalyticsNode{ID: node.ID, Label: node.Label, Kind: "memory", Subtype: node.Subtype})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for _, edge := range snapshot.Edges {
		if known[edge.Source] && known[edge.Target] {
			edges = append(edges, domaingraph.GraphAnalyticsEdge{ID: edge.ID, Source: edge.Source, Target: edge.Target, Type: edge.Type, Weight: edge.Weight, Confidence: edge.Confidence})
		}
	}

	symbols := make(map[string]code.Symbol, len(codeGraph.Symbols))
	for _, symbol := range codeGraph.Symbols {
		if symbol.Project != projectID {
			return nil, false, errors.New("agent code graph scope mismatch")
		}
		symbols[symbol.ID] = symbol
		nodeID := "code:" + symbol.ID
		if known[nodeID] {
			continue
		}
		known[nodeID] = true
		nodes = append(nodes, domaingraph.GraphAnalyticsNode{ID: nodeID, Label: symbol.Name, Kind: "code", Subtype: symbol.Kind, SourceFile: symbol.FilePath})
	}
	for _, relation := range codeGraph.Relations {
		source, target := "code:"+relation.SourceID, "code:"+relation.TargetID
		if known[source] && known[target] {
			edges = append(edges, domaingraph.GraphAnalyticsEdge{Source: source, Target: target, Type: relation.Relation, Weight: max(relation.Confidence, .01), Confidence: relation.Confidence})
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		if edges[i].Target != edges[j].Target {
			return edges[i].Target < edges[j].Target
		}
		if edges[i].Type != edges[j].Type {
			return edges[i].Type < edges[j].Type
		}
		return edges[i].ID < edges[j].ID
	})
	if len(nodes) == 0 || len(seeds) == 0 {
		return nil, degraded, nil
	}
	opts := domaingraph.DefaultPPROptions()
	opts.Directed = false
	rawScores := domaingraph.ComputePersonalizedPageRank(nodes, edges, seeds, opts)
	scores := make([]retrieval.AgentScore, 0, len(rawScores))
	for id, score := range rawScores {
		scores = append(scores, retrieval.AgentScore{Signal: retrieval.AgentScorePPR, Raw: &score, SourceKind: "graph", PublicHandle: id})
	}
	ranked, err := retrieval.NormalizeAgentScores(scores)
	if err != nil {
		return nil, false, err
	}
	evidence := make([]agentdomain.Evidence, 0, min(limit, len(ranked)))
	for _, rankedNode := range ranked {
		if len(evidence) >= limit {
			break
		}
		if node, exists := memoryNodes[rankedNode.PublicHandle]; exists {
			internalID, valid := agentGraphObservationID(node.Metadata["observation_id"])
			if !valid {
				continue
			}
			observation, hydrateErr := r.ops.GetAgentObservationByID(ctx, projectID, scope.Project, internalID)
			if hydrateErr != nil {
				return nil, false, hydrateErr
			}
			if observation == nil {
				return nil, false, errors.New("agent graph hydration scope uncertain")
			}
			evidence = append(evidence, agentdomain.Evidence{Kind: agentdomain.EvidenceMemory, Title: observation.Title, Content: observation.Content, Score: rankedNode.Normalized})
			continue
		}
		if strings.HasPrefix(rankedNode.PublicHandle, "code:") {
			symbol, exists := symbols[strings.TrimPrefix(rankedNode.PublicHandle, "code:")]
			if !exists {
				continue
			}
			content := strings.TrimSpace(strings.Join([]string{symbol.Kind + " " + symbol.Name, symbol.Signature, symbol.DocSummary}, "\n"))
			evidence = append(evidence, agentdomain.Evidence{Kind: agentdomain.EvidenceCode, Title: symbol.Name, Path: symbol.FilePath, LineStart: symbol.LineNumber, LineEnd: symbol.EndLine, Content: content, Score: rankedNode.Normalized})
		}
	}
	if architectural {
		cache := r.summaries
		if cache == nil {
			cache = sharedAgentSummaryCache
		}
		summaries, _, summaryErr := cache.architecturalEvidence(ctx, scope, projectID, snapshot, codeGraph, !degraded)
		if summaryErr != nil {
			return nil, false, summaryErr
		}
		evidence = mergeAgentArchitecturalEvidence(summaries, evidence, limit)
	}
	return evidence, degraded, nil
}

func isAgentGraphAvailability(err error) bool {
	return errors.Is(err, postgresstore.ErrCodeIndexUnavailable)
}

func agentHybridGraphSeeds(lexical []*domain.SearchResult, dense []*domain.VectorSearchResult, ast []code.Symbol) (map[string]float64, error) {
	result := map[string]float64{}
	add := func(scores []retrieval.AgentScore) error {
		if len(scores) == 0 {
			return nil
		}
		normalized, err := retrieval.NormalizeAgentScores(scores)
		if err != nil {
			return err
		}
		sum := 0.0
		for _, score := range normalized {
			sum += score.Normalized
		}
		if sum <= 0 {
			return nil
		}
		for _, score := range normalized {
			result[score.PublicHandle] += score.Normalized / sum
		}
		return nil
	}
	lexicalScores := make([]retrieval.AgentScore, 0, len(lexical))
	for _, item := range lexical {
		if item == nil || strings.TrimSpace(item.PublicID) == "" {
			continue
		}
		raw := item.Rank
		lexicalScores = append(lexicalScores, retrieval.AgentScore{Signal: retrieval.AgentScoreLexical, Raw: &raw, SourceKind: "memory", PublicHandle: "observation:" + item.PublicID})
	}
	denseScores := make([]retrieval.AgentScore, 0, len(dense))
	for _, item := range dense {
		if item == nil || strings.TrimSpace(item.PublicID) == "" {
			continue
		}
		raw := item.Similarity
		denseScores = append(denseScores, retrieval.AgentScore{Signal: retrieval.AgentScoreDense, Raw: &raw, SourceKind: "memory", PublicHandle: "observation:" + item.PublicID})
	}
	sort.Slice(ast, func(i, j int) bool { return ast[i].ID < ast[j].ID })
	astScores := make([]retrieval.AgentScore, 0, len(ast))
	for i, item := range ast {
		raw := 1 / float64(i+1)
		astScores = append(astScores, retrieval.AgentScore{Signal: retrieval.AgentScoreMaxSim, Raw: &raw, SourceKind: "code", PublicHandle: "code:" + item.ID})
	}
	if err := add(lexicalScores); err != nil {
		return nil, err
	}
	if err := add(denseScores); err != nil {
		return nil, err
	}
	if err := add(astScores); err != nil {
		return nil, err
	}
	return result, nil
}

func agentGraphObservationID(value any) (int64, bool) {
	switch id := value.(type) {
	case int64:
		return id, id > 0
	case int:
		return int64(id), id > 0
	case float64:
		return int64(id), id > 0 && id == float64(int64(id))
	default:
		return 0, false
	}
}

func mergeAgentEvidence(base, expanded []agentdomain.Evidence, limit int) []agentdomain.Evidence {
	result := make([]agentdomain.Evidence, 0, min(limit, len(base)+len(expanded)))
	seen := map[string]bool{}
	branches := make([][]agentdomain.Evidence, 0, 3)
	if len(base) > 0 {
		branches = append(branches, base[:1])
	}
	branches = append(branches, expanded)
	if len(base) > 1 {
		branches = append(branches, base[1:])
	}
	for _, branch := range branches {
		for _, item := range branch {
			key := string(item.Kind) + "\x1f" + item.Title + "\x1f" + item.Path
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, item)
			if len(result) >= limit {
				return result
			}
		}
	}
	return result
}

func (r scopedAgentRetriever) semanticDense(ctx context.Context, scope agentdomain.Scope, projectID, query string, limit int) ([]*domain.VectorSearchResult, error) {
	if r.embeddings == nil || !domain.IsVectorIndexHealthy(ctx, r.vectors) {
		return nil, errors.New("agent dense retrieval unavailable")
	}
	vector, err := r.embeddings.Embed(ctx, query)
	if err != nil || len(vector) == 0 {
		return nil, errors.New("agent query embedding unavailable")
	}
	return retrieval.SearchVectors(ctx, r.vectors, domain.VectorQuery{
		Vector: vector, Limit: limit, Threshold: .3,
		Filters: map[string]any{
			"tenant_id": scope.TenantID, "workspace_id": scope.WorkspaceID, "project_id": projectID,
		},
	}, agentObservationLookup{ops: r.ops, projectID: projectID, projectLabel: scope.Project})
}

type agentRankCandidate struct {
	result       *domain.SearchResult
	publicHandle string
	rrf          float64
	maxSim       float64
	final        float64
}

func semanticAgentEvidence(query string, lexical []*domain.SearchResult, dense []*domain.VectorSearchResult, limit int) ([]agentdomain.Evidence, error) {
	lexicalRanked, err := normalizeAgentBranch(lexical, retrieval.AgentScoreLexical)
	if err != nil {
		return nil, err
	}
	denseResults := make([]*domain.SearchResult, 0, len(dense))
	for _, item := range dense {
		if item == nil {
			continue
		}
		denseResults = append(denseResults, &domain.SearchResult{Observation: item.Observation, Rank: item.Similarity})
	}
	denseRanked, err := normalizeAgentBranch(denseResults, retrieval.AgentScoreDense)
	if err != nil {
		return nil, err
	}

	candidates := make(map[string]*agentRankCandidate, len(lexicalRanked)+len(denseRanked))
	addBranch := func(branch []*domain.SearchResult) {
		for rank, result := range branch {
			handle := publicAgentHandle(result)
			candidate := candidates[handle]
			if candidate == nil {
				candidate = &agentRankCandidate{result: result, publicHandle: handle}
				candidates[handle] = candidate
			}
			// Normalize each reciprocal-rank contribution to [0,.5] before
			// adding branches; a top result in both branches reaches one.
			candidate.rrf += ((agentRRFConstant + 1) / (agentRRFConstant + float64(rank+1))) / 2
		}
	}
	addBranch(lexicalRanked)
	addBranch(denseRanked)

	maxSimScores := make([]retrieval.AgentScore, 0, len(candidates))
	for _, candidate := range candidates {
		raw := retrieval.ComputeMaxSimScore(
			retrieval.TokenizeLateInteraction(query),
			retrieval.TokenizeLateInteraction(candidate.result.Title+" "+candidate.result.TopicKey+" "+candidate.result.Content),
		)
		maxSimScores = append(maxSimScores, retrieval.AgentScore{Signal: retrieval.AgentScoreMaxSim, Raw: &raw, SourceKind: "memory", PublicHandle: candidate.publicHandle})
	}
	normalizedMaxSim, err := retrieval.NormalizeAgentScores(maxSimScores)
	if err != nil {
		return nil, err
	}
	for _, score := range normalizedMaxSim {
		candidate := candidates[score.PublicHandle]
		candidate.maxSim = score.Normalized
		candidate.final = .6*candidate.rrf + .4*candidate.maxSim
	}

	ranked := make([]*agentRankCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		ranked = append(ranked, candidate)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].final != ranked[j].final {
			return ranked[i].final > ranked[j].final
		}
		return ranked[i].publicHandle < ranked[j].publicHandle
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	evidence := make([]agentdomain.Evidence, 0, len(ranked))
	for _, candidate := range ranked {
		evidence = append(evidence, agentdomain.Evidence{Kind: agentdomain.EvidenceMemory, Title: candidate.result.Title, Content: candidate.result.Content, Score: candidate.final})
	}
	return evidence, nil
}

func lexicalAgentEvidence(results []*domain.SearchResult, limit int) ([]agentdomain.Evidence, error) {
	ranked, err := normalizeAgentBranch(results, retrieval.AgentScoreLexical)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	evidence := make([]agentdomain.Evidence, 0, len(ranked))
	for _, result := range ranked {
		raw := result.Rank
		normalized, err := retrieval.NormalizeAgentScores([]retrieval.AgentScore{{Signal: retrieval.AgentScoreLexical, Raw: &raw, SourceKind: "memory", PublicHandle: publicAgentHandle(result)}})
		if err != nil {
			return nil, err
		}
		evidence = append(evidence, agentdomain.Evidence{Kind: agentdomain.EvidenceMemory, Title: result.Title, Content: result.Content, Score: normalized[0].Normalized})
	}
	return evidence, nil
}

func normalizeAgentBranch(results []*domain.SearchResult, signal retrieval.AgentScoreSignal) ([]*domain.SearchResult, error) {
	byHandle := make(map[string]*domain.SearchResult, len(results))
	scores := make([]retrieval.AgentScore, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		handle := publicAgentHandle(result)
		if _, duplicate := byHandle[handle]; duplicate {
			continue
		}
		byHandle[handle] = result
		raw := result.Rank
		scores = append(scores, retrieval.AgentScore{Signal: signal, Raw: &raw, SourceKind: "memory", PublicHandle: handle})
	}
	normalized, err := retrieval.NormalizeAgentScores(scores)
	if err != nil {
		return nil, err
	}
	ranked := make([]*domain.SearchResult, 0, len(normalized))
	for _, score := range normalized {
		ranked = append(ranked, byHandle[score.PublicHandle])
	}
	return ranked, nil
}

func publicAgentHandle(result *domain.SearchResult) string {
	if publicID := strings.TrimSpace(result.PublicID); publicID != "" {
		return publicID
	}
	// Compatibility fallback for local/unit fixtures. Never use the internal
	// bigint ID as a ranking or disclosure identity.
	return result.Title + "\x1f" + result.TopicKey + "\x1f" + result.Project
}

func agentRetrievalTier(tier retrieval.QueryTier) agentdomain.RetrievalTier {
	switch tier {
	case retrieval.TierSemanticHybrid:
		return agentdomain.RetrievalTierSemanticHybrid
	case retrieval.TierMultiHopGraph:
		return agentdomain.RetrievalTierMultiHopGraph
	case retrieval.TierArchitecturalGlobal:
		return agentdomain.RetrievalTierArchitecturalGlobal
	default:
		return agentdomain.RetrievalTierDirectFactual
	}
}
