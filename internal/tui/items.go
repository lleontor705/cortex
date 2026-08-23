package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/store/session"
)

// observationItem wraps a domain.Observation for bubbles/list.
type observationItem struct {
	obs *domain.Observation
}

func (i observationItem) Title() string {
	tColor := typeColor(i.obs.Type)
	badge := lipgloss.NewStyle().Foreground(tColor).Bold(true).Render(fmt.Sprintf("[%s]", i.obs.Type))
	return fmt.Sprintf("#%-5d %s %s", i.obs.ID, badge, truncateStr(i.obs.Title, 45))
}

func (i observationItem) Description() string {
	desc := truncateStr(i.obs.Content, 80)
	if i.obs.Project != "" {
		desc = i.obs.Project + " | " + desc
	}
	return desc
}

func (i observationItem) FilterValue() string {
	return i.obs.Title + " " + i.obs.Content + " " + i.obs.Type + " " + i.obs.Project
}

// searchResultItem wraps a SearchResult with score info.
type searchResultItem struct {
	result *domain.SearchResult
}

func (i searchResultItem) Title() string {
	tColor := typeColor(i.result.Type)
	badge := lipgloss.NewStyle().Foreground(tColor).Bold(true).Render(fmt.Sprintf("[%s]", i.result.Type))
	score := fmt.Sprintf("%.0f%%", i.result.Rank*100)
	return fmt.Sprintf("#%-5d %s %s  %s", i.result.ID, badge, truncateStr(i.result.Title, 40), score)
}

func (i searchResultItem) Description() string {
	desc := truncateStr(i.result.Content, 80)
	if i.result.ScoreBreakdown.Strategy != "" {
		desc = i.result.ScoreBreakdown.Strategy + " | " + desc
	}
	return desc
}

func (i searchResultItem) FilterValue() string {
	return i.result.Title + " " + i.result.Content
}

// sessionItem wraps session stats.
type sessionItem struct {
	session *session.SessionStats
}

func (i sessionItem) Title() string {
	return fmt.Sprintf("%-20s  %d observations", i.session.Session.Project, i.session.ObservationCount)
}

func (i sessionItem) Description() string {
	desc := formatTime(i.session.Session.StartedAt)
	if i.session.Session.Summary != "" {
		desc += " | " + truncateStr(i.session.Session.Summary, 50)
	}
	return desc
}

func (i sessionItem) FilterValue() string {
	return i.session.Session.Project + " " + i.session.Session.Summary
}

// graphItem wraps an observation with edge info.
type graphItem struct {
	obs       *domain.Observation
	edgeLabel string
}

func (i graphItem) Title() string {
	tColor := typeColor(i.obs.Type)
	badge := lipgloss.NewStyle().Foreground(tColor).Bold(true).Render(fmt.Sprintf("[%s]", i.obs.Type))
	title := fmt.Sprintf("#%-5d %s %s", i.obs.ID, badge, truncateStr(i.obs.Title, 40))
	if i.edgeLabel != "" {
		title += "  " + graphEdgeStyle.Render("["+i.edgeLabel+"]")
	}
	return title
}

func (i graphItem) Description() string {
	return truncateStr(i.obs.Content, 70)
}

func (i graphItem) FilterValue() string {
	return i.obs.Title + " " + i.obs.Content
}
