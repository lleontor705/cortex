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
	Format         string // "markdown", "xml", "json" (default "markdown")
	MaxDecisions   int    // default 5
	MaxBugfixes    int    // default 5
	MaxGodNodes    int    // default 5
	IncludeRules   bool   // default true
	IncludeRepoMap bool   // default false
	RepoMapBudget  int    // default 1024
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

// Render formats the pack according to the requested format ("markdown", "xml", "json").
func Render(pack *Pack, format string) (string, error) {
	switch strings.ToLower(format) {
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
