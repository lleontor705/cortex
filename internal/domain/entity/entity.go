// Package entity implements entity extraction and linking for Cortex.
//
// It extracts files, URLs, packages, symbols, and concepts from observation
// content using regex patterns and stores them as entity links.
package entity

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/lleontor705/cortex/internal/domain"
)

// Extraction patterns
var (
	// File paths: src/auth/middleware.ts, internal/store/store.go, ./config.yaml
	filePattern = regexp.MustCompile(`(?:^|[\s,(])([a-zA-Z0-9_/-]+\.[a-zA-Z]{1,10})(?:[\s,).;:]|$)`)

	// URLs: https://example.com, http://localhost:8080/api
	urlPattern = regexp.MustCompile(`https?://[^\s"'>)\]]+`)

	// Go/Python/JS packages: github.com/foo/bar, @scope/package, from 'react'
	packagePattern = regexp.MustCompile(`(?:github\.com/[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+|@[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+|from\s+['"]([a-zA-Z0-9@/_.-]+)['"])`)

	// Symbols: function names, types — FooBar(), type Config, class Service
	symbolPattern = regexp.MustCompile(`(?:func\s+|type\s+|class\s+|function\s+|def\s+|const\s+|var\s+)([A-Za-z_][A-Za-z0-9_]*)`)
)

// Service handles entity extraction and storage.
type Service struct {
	repo domain.EntityRepository
}

// NewService creates a new entity service.
func NewService(repo domain.EntityRepository) *Service {
	return &Service{repo: repo}
}

// ExtractAndSave extracts entities from an observation and saves the links.
func (s *Service) ExtractAndSave(ctx context.Context, obs *domain.Observation) error {
	links := Extract(obs)
	if len(links) == 0 {
		return nil
	}

	if err := s.repo.SaveLinks(ctx, links); err != nil {
		return fmt.Errorf("entity: save links: %w", err)
	}
	return nil
}

// GetByObservation returns entity links for an observation.
func (s *Service) GetByObservation(ctx context.Context, obsID int64) ([]*domain.EntityLink, error) {
	return s.repo.GetByObservation(ctx, obsID)
}

// FindByEntity returns observations referencing a specific entity.
func (s *Service) FindByEntity(ctx context.Context, entityType, entityValue string) ([]*domain.EntityLink, error) {
	return s.repo.FindByEntity(ctx, entityType, entityValue)
}

// Extract extracts all entity links from an observation's content.
func Extract(obs *domain.Observation) []*domain.EntityLink {
	text := obs.Title + "\n" + obs.Content
	seen := make(map[string]bool)
	var links []*domain.EntityLink

	add := func(entityType, value string) {
		key := entityType + ":" + value
		if seen[key] {
			return
		}
		seen[key] = true
		links = append(links, &domain.EntityLink{
			ObservationID: obs.ID,
			EntityType:    entityType,
			EntityValue:   value,
		})
	}

	// URLs (extract before files to avoid false positives)
	for _, m := range urlPattern.FindAllString(text, -1) {
		m = strings.TrimRight(m, ".,;:!?)")
		add(domain.EntityURL, m)
	}

	// Files
	for _, m := range filePattern.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 {
			f := m[1]
			// Skip common false positives
			if isLikelyFile(f) {
				add(domain.EntityFile, f)
			}
		}
	}

	// Packages
	for _, m := range packagePattern.FindAllStringSubmatch(text, -1) {
		pkg := m[0]
		if len(m) > 1 && m[1] != "" {
			pkg = m[1] // from 'package' capture group
		}
		pkg = strings.TrimPrefix(pkg, "from ")
		pkg = strings.Trim(pkg, "'\"")
		if pkg != "" {
			add(domain.EntityPackage, pkg)
		}
	}

	// Symbols
	for _, m := range symbolPattern.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 && len(m[1]) > 1 {
			add(domain.EntitySymbol, m[1])
		}
	}

	return links
}

// isLikelyFile checks if a string looks like a real file path.
func isLikelyFile(s string) bool {
	// Must contain at least one path separator or be a dotfile
	if !strings.Contains(s, "/") && !strings.HasPrefix(s, ".") {
		// Single filename — check extension
		parts := strings.Split(s, ".")
		if len(parts) < 2 {
			return false
		}
		ext := parts[len(parts)-1]
		return isCodeExtension(ext)
	}
	return true
}

func isCodeExtension(ext string) bool {
	exts := map[string]bool{
		"go": true, "ts": true, "tsx": true, "js": true, "jsx": true,
		"py": true, "rs": true, "java": true, "kt": true, "rb": true,
		"yaml": true, "yml": true, "json": true, "toml": true, "xml": true,
		"sql": true, "sh": true, "md": true, "css": true, "html": true,
		"proto": true, "graphql": true, "vue": true, "svelte": true,
	}
	return exts[ext]
}
