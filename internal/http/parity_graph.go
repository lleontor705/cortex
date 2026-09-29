package http

import (
	"context"
	"sort"
	"strconv"

	"github.com/lleontor705/cortex/v2/internal/domain"
	graphstore "github.com/lleontor705/cortex/v2/internal/store/graph"
)

// Local project-graph traversal bounds. The neutral internal/api handler does
// not widen an untrusted query, but the Port can be called directly, so the
// same envelope is re-applied here: depth defaults to 2 inside 1..10 and
// max_nodes to 100 inside 1..200. The legacy server traversal read at most 150
// project observations before fanning out, which bounds the seed set.
const (
	localProjectGraphObservationLimit = 150
	localProjectGraphDefaultDepth     = 2
	localProjectGraphMinDepth         = 1
	localProjectGraphMaxDepth         = 10
	localProjectGraphDefaultMaxNodes  = 100
	localProjectGraphMinMaxNodes      = 1
	localProjectGraphMaxMaxNodes      = 200
)

// allProjectsRoot is the aggregate sentinel projectGraphRootLabel emits for an
// unscoped request. It names the absence of a project boundary, so expansion
// must not treat it as a real project to compare against.
const allProjectsRoot = "all_projects"

// projectObservationReader is the observation slice the traversal needs; the
// concrete *sqlitestore.Store satisfies it through Deps.Observations.
type projectObservationReader interface {
	List(ctx context.Context, filter domain.ObservationFilter) ([]*domain.Observation, error)
	GetByID(ctx context.Context, id int64) (*domain.Observation, error)
}

// localProjectGraph is the graph slice of the local parity Port (REQ-SH-012).
// It is embeddable in the composite local port so internal/api handlers reach
// ProjectGraph, while keeping the breadth-first traversal separable from the
// stats/projects surface.
type localProjectGraph struct {
	observations projectObservationReader
	graph        *graphstore.Store
}

// newLocalProjectGraph binds the local parity graph traversal to the SQLite
// bundle. A nil Deps, a nil observation store, or a nil graph store degrades to
// an empty subgraph rather than panicking.
func newLocalProjectGraph(deps *Deps) localProjectGraph {
	if deps == nil {
		return localProjectGraph{}
	}
	return localProjectGraph{observations: deps.Observations, graph: deps.Graph}
}

// ProjectGraph implements internal/api.Port for local mode: a bounded
// breadth-first search over bundle.Graph edges seeded by the requested project's
// observations. It returns the same domain.GraphSubgraph payload the server
// adapter emits (observation nodes plus semantic links), terminating on empty,
// disconnected, or cyclic graphs.
func (g localProjectGraph) ProjectGraph(ctx context.Context, project string, depth, maxNodes int) (*domain.GraphSubgraph, error) {
	depth, maxNodes = clampProjectGraphBounds(depth, maxNodes)
	result := &domain.GraphSubgraph{Root: projectGraphRootLabel(project)}
	if g.observations == nil || g.graph == nil {
		return result, nil
	}

	seeds, err := g.observations.List(ctx, domain.ObservationFilter{Project: project, Limit: localProjectGraphObservationLimit})
	if err != nil {
		return nil, err
	}
	return buildProjectGraphSubgraph(ctx, result.Root, seeds, depth, maxNodes, g.graph.CurrentEdges, g.observations.GetByID), nil
}

func projectGraphRootLabel(project string) string {
	if project == "" {
		return allProjectsRoot
	}
	return project
}

// projectGraphNodeInProject enforces the same project boundary the seed query
// applies. Expansion resolves neighbours by identifier through an unscoped
// GetByID, so without this check a single edge would serialize a foreign
// project's title, kind and scope into the response (REQ-SQ-SEC-005). The
// aggregate root denotes no boundary and admits every project.
func projectGraphNodeInProject(root string, observation *domain.Observation) bool {
	return root == allProjectsRoot || observation.Project == root
}

func clampProjectGraphBounds(depth, maxNodes int) (int, int) {
	return clampProjectGraphBound(depth, localProjectGraphDefaultDepth, localProjectGraphMinDepth, localProjectGraphMaxDepth),
		clampProjectGraphBound(maxNodes, localProjectGraphDefaultMaxNodes, localProjectGraphMinMaxNodes, localProjectGraphMaxMaxNodes)
}

// clampProjectGraphBound mirrors the handler's query contract: a value below the
// minimum (including a zero or negative one) falls back to the default, while a
// value above the maximum saturates at the maximum.
func clampProjectGraphBound(value, fallback, minimum, maximum int) int {
	if value < minimum {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

// buildProjectGraphSubgraph runs the level-order walk. Nodes are emitted in
// discovery order (seeds first) and neighbours are visited in a stable
// edge-identifier order, so identical inputs always produce identical output.
// A neighbour outside the root project is skipped unless the root is the
// all_projects aggregate, so a crossing edge is dropped instead of pulling its
// foreign endpoint (and its labels) into the payload. Only edges whose two
// endpoints are known nodes are emitted, keeping the serialized subgraph
// referentially closed.
func buildProjectGraphSubgraph(
	ctx context.Context,
	root string,
	seeds []*domain.Observation,
	depth, maxNodes int,
	neighbors func(context.Context, int64) ([]*domain.Edge, error),
	lookup func(context.Context, int64) (*domain.Observation, error),
) *domain.GraphSubgraph {
	result := &domain.GraphSubgraph{Root: root}
	if depth < 1 || maxNodes < 1 {
		return result
	}

	nodeIDByObs := make(map[int64]string, len(seeds))
	seenNodeID := make(map[string]bool, len(seeds))
	seenEdgeID := make(map[string]bool)
	frontier := make([]int64, 0, len(seeds))

	for _, seed := range seeds {
		if seed == nil {
			continue
		}
		nodeID := localObservationNodeID(seed)
		if seenNodeID[nodeID] {
			nodeIDByObs[seed.ID] = nodeID
			continue
		}
		if len(result.Nodes) >= maxNodes {
			result.Truncated = true
			break
		}
		seenNodeID[nodeID] = true
		nodeIDByObs[seed.ID] = nodeID
		result.Nodes = append(result.Nodes, localObservationNode(seed, 0))
		frontier = append(frontier, seed.ID)
	}

	for level := 1; level <= depth && len(frontier) > 0 && !result.Truncated; level++ {
		next := make([]int64, 0)
		for _, obsID := range frontier {
			edges, err := neighbors(ctx, obsID)
			if err != nil {
				continue
			}
			ordered := append([]*domain.Edge(nil), edges...)
			sort.SliceStable(ordered, func(i, j int) bool {
				return localGraphLinkID(ordered[i]) < localGraphLinkID(ordered[j])
			})

			for _, edge := range ordered {
				if edge == nil {
					continue
				}
				neighbor := edgeNeighbor(edge, obsID)
				if neighbor == 0 {
					continue
				}
				if _, known := nodeIDByObs[neighbor]; !known {
					observation, lookupErr := lookup(ctx, neighbor)
					if lookupErr != nil || observation == nil {
						continue
					}
					if !projectGraphNodeInProject(root, observation) {
						continue
					}
					nodeID := localObservationNodeID(observation)
					switch {
					case seenNodeID[nodeID]:
						nodeIDByObs[neighbor] = nodeID
					case len(result.Nodes) >= maxNodes:
						result.Truncated = true
					default:
						seenNodeID[nodeID] = true
						nodeIDByObs[neighbor] = nodeID
						result.Nodes = append(result.Nodes, localObservationNode(observation, level))
						next = append(next, neighbor)
					}
					if result.Truncated {
						break
					}
				}

				source, sourceKnown := nodeIDByObs[edge.FromObsID]
				target, targetKnown := nodeIDByObs[edge.ToObsID]
				if !sourceKnown || !targetKnown {
					continue
				}
				linkID := localGraphLinkID(edge)
				if seenEdgeID[linkID] {
					continue
				}
				seenEdgeID[linkID] = true
				result.Edges = append(result.Edges, localGraphLink(edge, source, target))
			}
			if result.Truncated {
				break
			}
		}
		frontier = next
	}

	return result
}

func edgeNeighbor(edge *domain.Edge, obsID int64) int64 {
	switch {
	case edge.FromObsID == obsID:
		return edge.ToObsID
	case edge.ToObsID == obsID:
		return edge.FromObsID
	default:
		return 0
	}
}

// localObservationNodeID prefers the opaque server identifier so the payload
// matches server mode; local SQLite rows carry only the numeric identifier.
func localObservationNodeID(observation *domain.Observation) string {
	if observation.PublicID != "" {
		return "observation:" + observation.PublicID
	}
	return "observation:" + strconv.FormatInt(observation.ID, 10)
}

func localObservationNode(observation *domain.Observation, hop int) domain.GraphNode {
	return domain.GraphNode{
		ID:      localObservationNodeID(observation),
		Kind:    observation.Type,
		Label:   observation.Title,
		Project: observation.Project,
		Hop:     hop,
		Metadata: map[string]any{
			"source": observation.Source,
			"scope":  observation.Scope,
		},
	}
}

func localGraphLinkID(edge *domain.Edge) string {
	switch {
	case edge.PublicID != "":
		return "edge:" + edge.PublicID
	case edge.ID != 0:
		return "edge:" + strconv.FormatInt(edge.ID, 10)
	default:
		return "edge:" + strconv.FormatInt(edge.FromObsID, 10) + ":" + strconv.FormatInt(edge.ToObsID, 10) + ":" + edge.RelationType
	}
}

func localGraphLink(edge *domain.Edge, source, target string) domain.GraphLink {
	return domain.GraphLink{
		ID:              localGraphLinkID(edge),
		Source:          source,
		Target:          target,
		Type:            edge.RelationType,
		Weight:          edge.Weight,
		Confidence:      edge.Confidence,
		AssertionKind:   edge.AssertionKind,
		AssertionStatus: edge.AssertionStatus,
	}
}
