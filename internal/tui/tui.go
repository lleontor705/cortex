// Package tui implements the terminal user interface for Cortex.
//
// It provides an interactive terminal interface for browsing, searching,
// and managing memories using BubbleTea.
//
// DEPENDENCY: This package requires github.com/charmbracelet/bubbletea
// and github.com/charmbracelet/lipgloss. Add them with:
//
//	go get github.com/charmbracelet/bubbletea
//	go get github.com/charmbracelet/lipgloss
package tui

import (
	"context"
	"fmt"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/store/search"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
)

// Deps bundles store dependencies for the TUI.
type Deps struct {
	Observations *sqlitestore.Store
	Search       *search.Store
}

// State represents the current TUI view.
type State int

const (
	StateList   State = iota // Browsing observation list
	StateSearch              // Search input mode
	StateDetail              // Viewing observation detail
)

// Model holds the TUI state.
type Model struct {
	deps         *Deps
	state        State
	observations []*domain.Observation
}

// New creates a new TUI model.
func New(deps *Deps) *Model {
	return &Model{
		deps:  deps,
		state: StateList,
	}
}

// Run starts the TUI in non-interactive mode (fallback without BubbleTea).
// This is a simplified version that works without the BubbleTea dependency.
// Once BubbleTea is added, this should be replaced with tea.NewProgram().
func Run(deps *Deps) error {
	m := New(deps)

	// Load initial data
	ctx := context.Background()
	obs, err := m.deps.Observations.List(ctx, domain.ObservationFilter{Limit: 50})
	if err != nil {
		return fmt.Errorf("tui: load observations: %w", err)
	}
	m.observations = obs

	// Print a simple interactive-style view
	fmt.Println("+----------------------------------------------------------+")
	fmt.Println("|  Cortex Memory Browser                                  |")
	fmt.Println("+----------------------------------------------------------+")

	if len(m.observations) == 0 {
		fmt.Println("-  No observations found.                                  -")
	} else {
		for i, obs := range m.observations {
			title := truncate(obs.Title, 45)
			fmt.Printf("-  %3d. [%-9s] %-45s -\n", i+1, obs.Type, title)
		}
	}

	fmt.Println("+----------------------------------------------------------+")
	fmt.Printf("\nShowing %d observations. Full TUI requires BubbleTea dependency.\n", len(m.observations))
	fmt.Println("Install with: go get github.com/charmbracelet/bubbletea")

	return nil
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
