// Package graph provides graph analytics, clustering, and structural code
// intelligence algorithms inspired by Graphify, ported natively to Go with zero-CGO.
package graph

import (
	"fmt"
	"sort"
	"strings"
)

// CommunitySummary encapsulates a high-level architectural summary of a functional cluster.
type CommunitySummary struct {
	CommunityID     int      `json:"community_id"`
	Label           string   `json:"label"`
	HubNodeID       string   `json:"hub_node_id"`
	HubNodeLabel    string   `json:"hub_node_label"`
	MemberCount     int      `json:"member_count"`
	CohesionScore   float64  `json:"cohesion_score"`
	KeySymbols      []string `json:"key_symbols"`
	ExternalDeps    []string `json:"external_deps"`
	SummaryMarkdown string   `json:"summary_markdown"`
}

// GenerateCommunitySummaries creates structured architectural summaries for each detected community (LightRAG).
func GenerateCommunitySummaries(
	communities []Community,
	nodes []GraphAnalyticsNode,
	edges []GraphAnalyticsEdge,
) []CommunitySummary {
	if len(communities) == 0 || len(nodes) == 0 {
		return nil
	}

	nodeMap := make(map[string]GraphAnalyticsNode, len(nodes))
	for _, n := range nodes {
		nodeMap[n.ID] = n
	}

	nodeToCommunity := make(map[string]int, len(nodes))
	for _, comm := range communities {
		for _, memberID := range comm.Members {
			nodeToCommunity[memberID] = comm.ID
		}
	}

	var summaries []CommunitySummary

	for _, comm := range communities {
		memberSet := make(map[string]bool, len(comm.Members))
		var keySymbols []string

		for _, mID := range comm.Members {
			memberSet[mID] = true
			if n, ok := nodeMap[mID]; ok {
				if n.Label != "" {
					keySymbols = append(keySymbols, n.Label)
				} else {
					keySymbols = append(keySymbols, n.ID)
				}
			}
		}

		sort.Strings(keySymbols)
		if len(keySymbols) > 10 {
			keySymbols = keySymbols[:10]
		}

		// Find external dependencies (edges leaving the community)
		extDepSet := make(map[string]bool)
		for _, e := range edges {
			if memberSet[e.Source] && !memberSet[e.Target] {
				if tgtNode, ok := nodeMap[e.Target]; ok && tgtNode.Label != "" {
					extDepSet[tgtNode.Label] = true
				} else {
					extDepSet[e.Target] = true
				}
			}
		}

		var extDeps []string
		for dep := range extDepSet {
			extDeps = append(extDeps, dep)
		}
		sort.Strings(extDeps)
		if len(extDeps) > 8 {
			extDeps = extDeps[:8]
		}

		hubLabel := comm.HubNodeID
		if hubNode, ok := nodeMap[comm.HubNodeID]; ok && hubNode.Label != "" {
			hubLabel = hubNode.Label
		}

		// Format structured markdown summary
		var sb strings.Builder
		fmt.Fprintf(&sb, "## Resumen Arquitectónico: Comunidad #%d (%s)\n\n", comm.ID, comm.Label)
		fmt.Fprintf(&sb, "- **Pivote Central (Hub)**: `%s`\n", hubLabel)
		fmt.Fprintf(&sb, "- **Tamaño del Módulo**: %d elementos (%d clave listados)\n", comm.Size, len(keySymbols))
		fmt.Fprintf(&sb, "- **Índice de Cohesión Interna**: %.2f\n", comm.CohesionScore)
		if len(keySymbols) > 0 {
			fmt.Fprintf(&sb, "- **Símbolos / Componentes Clave**: `%s`\n", strings.Join(keySymbols, "`, `"))
		}
		if len(extDeps) > 0 {
			fmt.Fprintf(&sb, "- **Dependencias Externas**: `%s`\n", strings.Join(extDeps, "`, `"))
		}

		summaries = append(summaries, CommunitySummary{
			CommunityID:     comm.ID,
			Label:           comm.Label,
			HubNodeID:       comm.HubNodeID,
			HubNodeLabel:    hubLabel,
			MemberCount:     comm.Size,
			CohesionScore:   comm.CohesionScore,
			KeySymbols:      keySymbols,
			ExternalDeps:    extDeps,
			SummaryMarkdown: sb.String(),
		})
	}

	return summaries
}
