package contextpack

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

// Options specifies criteria for building an agent context pack.
type Options struct {
	Project        string
	Format         string // "markdown", "xml", "json", "compact" (default "markdown")
	MaxDecisions   int    // default 5
	MaxBugfixes    int    // default 5
	MaxGodNodes    int    // default 5
	IncludeRules   bool   // default true
	IncludeRepoMap bool   // default false
	RepoMapBudget  int    // default 1024
	MaxTokens      int    // token budget for compact rendering (default 1500)
}

// Pack represents the assembled intelligence packet for an AI agent.
type Pack struct {
	Project   string                `json:"project"`
	Rules     []*domain.Observation `json:"rules,omitempty"`
	Decisions []*domain.Observation `json:"decisions,omitempty"`
	Bugfixes  []*domain.Observation `json:"bugfixes,omitempty"`
	GodNodes  []code.GodNode        `json:"god_nodes,omitempty"`
	RepoMap   string                `json:"repo_map,omitempty"`
}

// BuildPack filters and extracts context items into a unified Pack.
func BuildPack(
	project string,
	allObs []*domain.Observation,
	codeGraph *code.CodeGraph,
	opts Options,
) *Pack {
	if opts.MaxDecisions <= 0 {
		opts.MaxDecisions = 5
	}
	if opts.MaxBugfixes <= 0 {
		opts.MaxBugfixes = 5
	}
	if opts.MaxGodNodes <= 0 {
		opts.MaxGodNodes = 5
	}

	pack := &Pack{
		Project:   project,
		Rules:     []*domain.Observation{},
		Decisions: []*domain.Observation{},
		Bugfixes:  []*domain.Observation{},
		GodNodes:  []code.GodNode{},
	}

	for _, o := range allObs {
		if o == nil {
			continue
		}

		// Check for rules / directives
		isRule := o.Type == "pattern" || o.Type == "config" ||
			strings.HasPrefix(o.TopicKey, "rules/") || strings.HasPrefix(o.TopicKey, "directive/")
		for _, tag := range o.Tags {
			if tag == "rule" || tag == "directive" {
				isRule = true
				break
			}
		}

		if isRule && opts.IncludeRules {
			pack.Rules = append(pack.Rules, o)
			continue
		}

		// Decisions
		if o.Type == "decision" && len(pack.Decisions) < opts.MaxDecisions {
			pack.Decisions = append(pack.Decisions, o)
			continue
		}

		// Bugfixes / Gotchas
		if o.Type == "bugfix" && len(pack.Bugfixes) < opts.MaxBugfixes {
			pack.Bugfixes = append(pack.Bugfixes, o)
			continue
		}
	}

	// Architectural Hubs (God Nodes)
	if codeGraph != nil && len(codeGraph.Symbols) > 0 {
		report := code.ComputeAnalytics(codeGraph)
		if len(report.GodNodes) > opts.MaxGodNodes {
			pack.GodNodes = report.GodNodes[:opts.MaxGodNodes]
		} else {
			pack.GodNodes = report.GodNodes
		}

		if opts.IncludeRepoMap {
			budget := opts.RepoMapBudget
			if budget <= 0 {
				budget = 1024
			}
			pack.RepoMap = code.GenerateRepoMap(codeGraph, budget)
		}
	}

	return pack
}

// Render formats the pack according to the requested format ("markdown", "xml", "json", "compact").
func Render(pack *Pack, format string) (string, error) {
	switch strings.ToLower(format) {
	case "compact", "compact-markdown", "compact-md":
		return RenderCompact(pack, 1500), nil
	case "xml":
		return RenderXML(pack), nil
	case "json":
		return RenderJSON(pack)
	case "markdown", "md", "":
		return RenderMarkdown(pack), nil
	default:
		return RenderMarkdown(pack), nil
	}
}

// RenderMarkdown produces human-and-agent readable Markdown.
func RenderMarkdown(pack *Pack) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Cortex Context — %s\n\n", pack.Project)

	if len(pack.Rules) > 0 {
		sb.WriteString("## Active Rules & Directives\n")
		for _, r := range pack.Rules {
			fmt.Fprintf(&sb, "- **%s**", r.Title)
			if r.TopicKey != "" {
				fmt.Fprintf(&sb, " (`%s`)", r.TopicKey)
			}
			sb.WriteString(":\n")
			content := strings.TrimSpace(r.Content)
			if content != "" {
				for _, line := range strings.Split(content, "\n") {
					fmt.Fprintf(&sb, "  > %s\n", line)
				}
			}
		}
		sb.WriteString("\n")
	}

	if len(pack.Decisions) > 0 {
		sb.WriteString("## Architectural Decisions\n")
		for _, d := range pack.Decisions {
			fmt.Fprintf(&sb, "- **#%d %s**:\n", d.ID, d.Title)
			content := strings.TrimSpace(d.Content)
			if content != "" {
				for _, line := range strings.Split(content, "\n") {
					fmt.Fprintf(&sb, "  %s\n", line)
				}
			}
		}
		sb.WriteString("\n")
	}

	if len(pack.Bugfixes) > 0 {
		sb.WriteString("## Gotchas & Bugfix Lessons\n")
		for _, b := range pack.Bugfixes {
			fmt.Fprintf(&sb, "- **#%d %s**:\n", b.ID, b.Title)
			content := strings.TrimSpace(b.Content)
			if content != "" {
				for _, line := range strings.Split(content, "\n") {
					fmt.Fprintf(&sb, "  %s\n", line)
				}
			}
		}
		sb.WriteString("\n")
	}

	if len(pack.GodNodes) > 0 {
		sb.WriteString("## Core Architectural Hubs (AST God Nodes)\n")
		for _, gn := range pack.GodNodes {
			fmt.Fprintf(&sb, "- `[%s] %s` in `%s` (degree: %d, score: %.1f)\n", gn.Kind, gn.Name, gn.FilePath, gn.Degree, gn.Score)
		}
		sb.WriteString("\n")
	}

	if pack.RepoMap != "" {
		sb.WriteString("## Repository Map\n\n```\n")
		sb.WriteString(pack.RepoMap)
		sb.WriteString("\n```\n")
	}

	return strings.TrimSpace(sb.String())
}

// RenderXML produces compact XML suitable for system prompt injections.
func RenderXML(pack *Pack) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<cortex-context project=%q>\n", pack.Project)

	if len(pack.Rules) > 0 {
		sb.WriteString("  <rules>\n")
		for _, r := range pack.Rules {
			fmt.Fprintf(&sb, "    <rule title=%q topic=%q>\n", r.Title, r.TopicKey)
			fmt.Fprintf(&sb, "      <![CDATA[%s]]>\n", strings.TrimSpace(r.Content))
			sb.WriteString("    </rule>\n")
		}
		sb.WriteString("  </rules>\n")
	}

	if len(pack.Decisions) > 0 {
		sb.WriteString("  <architectural-decisions>\n")
		for _, d := range pack.Decisions {
			fmt.Fprintf(&sb, "    <decision id=\"%d\" title=%q>\n", d.ID, d.Title)
			fmt.Fprintf(&sb, "      <![CDATA[%s]]>\n", strings.TrimSpace(d.Content))
			sb.WriteString("    </decision>\n")
		}
		sb.WriteString("  </architectural-decisions>\n")
	}

	if len(pack.Bugfixes) > 0 {
		sb.WriteString("  <gotchas>\n")
		for _, b := range pack.Bugfixes {
			fmt.Fprintf(&sb, "    <bugfix id=\"%d\" title=%q>\n", b.ID, b.Title)
			fmt.Fprintf(&sb, "      <![CDATA[%s]]>\n", strings.TrimSpace(b.Content))
			sb.WriteString("    </bugfix>\n")
		}
		sb.WriteString("  </gotchas>\n")
	}

	if len(pack.GodNodes) > 0 {
		sb.WriteString("  <architectural-hubs>\n")
		for _, gn := range pack.GodNodes {
			fmt.Fprintf(&sb, "    <hub name=%q kind=%q file=%q degree=\"%d\" score=\"%.1f\" />\n",
				gn.Name, gn.Kind, gn.FilePath, gn.Degree, gn.Score)
		}
		sb.WriteString("  </architectural-hubs>\n")
	}

	if pack.RepoMap != "" {
		sb.WriteString("  <repo-map>\n")
		fmt.Fprintf(&sb, "    <![CDATA[%s]]>\n", pack.RepoMap)
		sb.WriteString("  </repo-map>\n")
	}

	sb.WriteString("</cortex-context>")
	return sb.String()
}

// RenderJSON serializes the Pack into indented JSON.
func RenderJSON(pack *Pack) (string, error) {
	bytes, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// RenderCompact formats the pack into an ultra-dense, token-budgeted representation.
// It prioritizes critical directives/rules, gotchas/bugfixes, architectural decisions, and
// core God Nodes, truncating gracefully when the estimated token budget is reached.
// If maxTokens <= 0, a default budget of 1500 tokens is used.
func RenderCompact(pack *Pack, maxTokens int) string {
	if pack == nil {
		return ""
	}
	if maxTokens <= 0 {
		maxTokens = 1500
	}
	// Conservative heuristic: ~4 characters per token
	charBudget := maxTokens * 4

	var sb strings.Builder
	header := fmt.Sprintf("# Cortex Intelligence: %s [Rules: %d | Bugs: %d | Decisions: %d | Hubs: %d]\n\n",
		pack.Project, len(pack.Rules), len(pack.Bugfixes), len(pack.Decisions), len(pack.GodNodes))
	sb.WriteString(header)

	budgetRemaining := charBudget - sb.Len()

	appendItem := func(sectionTitle string, items []string) {
		if len(items) == 0 || budgetRemaining <= 100 {
			return
		}
		var sec strings.Builder
		sec.WriteString(sectionTitle)
		sec.WriteString("\n")
		for _, it := range items {
			line := "- " + it + "\n"
			if len(line) > budgetRemaining {
				if budgetRemaining > 40 {
					line = line[:budgetRemaining-10] + "...\n"
					sec.WriteString(line)
				}
				budgetRemaining = 0
				break
			}
			sec.WriteString(line)
			budgetRemaining -= len(line)
		}
		sec.WriteString("\n")
		sb.WriteString(sec.String())
	}

	// 1. Rules & Directives (Highest Priority)
	var ruleItems []string
	for _, r := range pack.Rules {
		c := strings.Join(strings.Fields(strings.TrimSpace(r.Content)), " ")
		if len(c) > 120 {
			c = c[:117] + "..."
		}
		ruleItems = append(ruleItems, fmt.Sprintf("**%s**: %s", r.Title, c))
	}
	appendItem("## Active Rules & Directives", ruleItems)

	// 2. Gotchas & Bugfix Lessons (High Priority)
	var bugItems []string
	for _, b := range pack.Bugfixes {
		c := strings.Join(strings.Fields(strings.TrimSpace(b.Content)), " ")
		if len(c) > 120 {
			c = c[:117] + "..."
		}
		bugItems = append(bugItems, fmt.Sprintf("[#%d] **%s**: %s", b.ID, b.Title, c))
	}
	appendItem("## Gotchas & Bugfixes", bugItems)

	// 3. Architectural Decisions (Medium Priority)
	var decItems []string
	for _, d := range pack.Decisions {
		c := strings.Join(strings.Fields(strings.TrimSpace(d.Content)), " ")
		if len(c) > 120 {
			c = c[:117] + "..."
		}
		decItems = append(decItems, fmt.Sprintf("[#%d] **%s**: %s", d.ID, d.Title, c))
	}
	appendItem("## Key Decisions", decItems)

	// 4. Core Hubs / God Nodes (Architectural Context)
	var hubItems []string
	for _, gn := range pack.GodNodes {
		hubItems = append(hubItems, fmt.Sprintf("`%s` in `%s` (degree: %d)", gn.Name, gn.FilePath, gn.Degree))
	}
	appendItem("## Architectural Hubs", hubItems)

	// 5. Repo Map (if present and budget allows)
	if pack.RepoMap != "" && budgetRemaining > 150 {
		var sec strings.Builder
		sec.WriteString("## Repo Map\n```\n")
		rm := pack.RepoMap
		if len(rm)+20 > budgetRemaining {
			rm = rm[:budgetRemaining-20] + "\n..."
		}
		sec.WriteString(rm)
		sec.WriteString("\n```\n")
		sb.WriteString(sec.String())
	}

	return strings.TrimSpace(sb.String())
}
