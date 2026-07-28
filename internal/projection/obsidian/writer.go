// Package obsidian implements a one-way, deterministic Markdown projection.
package obsidian

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

type Options struct {
	Vault           string
	Project         string
	IncludePersonal bool
}
type Result struct {
	Written, Skipped, Stale int
	Warnings                []string
}
type Writer struct {
	observations domain.ObservationRepository
	graph        domain.GraphRepository
	entities     domain.EntityRepository
	scoring      domain.ScoringRepository
}

func NewWriter(observations domain.ObservationRepository, graph domain.GraphRepository, entities domain.EntityRepository, scoring domain.ScoringRepository) *Writer {
	return &Writer{observations: observations, graph: graph, entities: entities, scoring: scoring}
}

type syncState struct {
	Version    int                  `json:"version"`
	ExportedAt time.Time            `json:"exported_at"`
	Files      map[string]stateFile `json:"files"`
}
type stateFile struct {
	Path      string    `json:"path"`
	Hash      string    `json:"hash"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Export writes only Cortex-owned notes. Existing user-edited notes are never overwritten.
func (w *Writer) Export(ctx context.Context, opts Options) (Result, error) {
	var result Result
	vault, err := safeVault(opts.Vault)
	if err != nil {
		return result, err
	}
	if w == nil || w.observations == nil {
		return result, errors.New("obsidian: observation repository is required")
	}
	filter := domain.ObservationFilter{Project: opts.Project, Limit: 100000, IncludeArchived: true}
	obs, err := w.observations.List(ctx, filter)
	if err != nil {
		return result, fmt.Errorf("obsidian: list observations: %w", err)
	}
	if opts.IncludePersonal {
		result.Warnings = append(result.Warnings, "including personal/private observations by explicit opt-in")
	}
	sort.Slice(obs, func(i, j int) bool { return obs[i].ID < obs[j].ID })
	state := loadState(filepath.Join(vault, ".cortex-sync-state.json"))
	if state.Files == nil {
		state.Files = map[string]stateFile{}
	}
	visible := make(map[int64]*domain.Observation, len(obs))
	for _, o := range obs {
		private := o != nil && (strings.EqualFold(o.Scope, domain.ScopePersonal) || strings.EqualFold(o.Scope, "private"))
		if o == nil || (private && !opts.IncludePersonal) {
			if private {
				result.Warnings = append(result.Warnings, fmt.Sprintf("excluded personal observation #%d (use --include-personal to export)", o.ID))
			}
			continue
		}
		visible[o.ID] = o
	}
	paths, err := findCortexFiles(vault)
	if err != nil {
		return result, err
	}
	byID := map[string]string{}
	for p, id := range paths {
		byID[id] = p
	}
	stage, err := os.MkdirTemp(filepath.Join(vault, ".cortex-staging"), "export-")
	if err != nil {
		if err := os.MkdirAll(filepath.Join(vault, ".cortex-staging"), 0700); err != nil {
			return result, err
		}
		stage, err = os.MkdirTemp(filepath.Join(vault, ".cortex-staging"), "export-")
		if err != nil {
			return result, err
		}
	}
	defer func() { _ = os.RemoveAll(stage) }()
	now := time.Now().UTC()
	next := syncState{Version: 1, ExportedAt: now, Files: map[string]stateFile{}}
	for _, o := range visible {
		id := fmt.Sprintf("%d", o.ID)
		target := filepath.Join(vault, projectFolder(o.Project), "observations", safeSlug(o.Title)+"-"+idHash(id)+".md")
		if old, ok := byID[id]; ok && old != target {
			if _, exists := os.Stat(target); errors.Is(exists, os.ErrNotExist) {
				if err := os.Rename(old, target); err != nil {
					result.Warnings = append(result.Warnings, fmt.Sprintf("could not rename %s: %v", old, err))
					target = old
				}
				if target != old {
					byID[id] = target
				}
			}
		}
		content, err := w.render(ctx, o, visible)
		if err != nil {
			return result, err
		}
		hash := contentHash(content)
		if existing, ok := byID[id]; ok && filepath.Clean(existing) == filepath.Clean(target) {
			if shouldSkip(existing, state.Files[id], hash) {
				next.Files[id] = state.Files[id]
				result.Skipped++
				continue
			}
		}
		if existing, statErr := os.Stat(target); statErr == nil && existing != nil {
			if known, ok := byID[id]; !ok || filepath.Clean(known) != filepath.Clean(target) {
				return result, fmt.Errorf("obsidian: refusing overwrite conflict at %s", target)
			}
		}
		if err := ensureSafeParents(vault, filepath.Dir(target)); err != nil {
			return result, err
		}
		rel, _ := filepath.Rel(vault, target)
		staged := filepath.Join(stage, rel)
		if err := os.MkdirAll(filepath.Dir(staged), 0700); err != nil {
			return result, err
		}
		if err := os.WriteFile(staged, []byte(content), 0600); err != nil {
			return result, err
		}
		if err := commitFile(staged, target); err != nil {
			return result, err
		}
		next.Files[id] = stateFile{Path: rel, Hash: hash, UpdatedAt: o.UpdatedAt}
		result.Written++
	}
	for id := range state.Files {
		if _, ok := visibleID(visible, id); !ok {
			result.Stale++
		}
	}
	b, _ := json.MarshalIndent(next, "", "  ")
	tmp := filepath.Join(vault, ".cortex-sync-state.json.tmp")
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return result, err
	}
	if err := os.Rename(tmp, filepath.Join(vault, ".cortex-sync-state.json")); err != nil {
		return result, err
	}
	return result, nil
}

func (w *Writer) render(ctx context.Context, o *domain.Observation, all map[int64]*domain.Observation) (string, error) {
	fm, err := RenderFrontmatter(o, FrontmatterOptions{})
	if err != nil {
		return "", err
	}
	body := strings.TrimRight(o.Content, "\n") + "\n"
	if w.graph != nil {
		edges, e := w.graph.GetEdgesForObservation(ctx, o.ID)
		if e != nil {
			return "", e
		}
		links := map[string]bool{}
		for _, edge := range edges {
			id := edge.ToObsID
			if id == o.ID {
				id = edge.FromObsID
			}
			if target, ok := all[id]; ok {
				links[fmt.Sprintf("[[%s-%s]]", safeSlug(target.Title), idHash(fmt.Sprintf("%d", target.ID)))] = true
			}
		}
		keys := make([]string, 0, len(links))
		for k := range links {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			body += "\n## Related\n\n"
			for _, k := range keys {
				body += "- " + k + "\n"
			}
		}
	}
	if w.entities != nil {
		links, e := w.entities.GetByObservation(ctx, o.ID)
		if e != nil {
			return "", e
		}
		values := make([]string, 0, len(links))
		seen := map[string]bool{}
		for _, link := range links {
			if link == nil {
				continue
			}
			name := safeSlug(link.EntityValue)
			if name != "" && !seen[name] {
				seen[name] = true
				values = append(values, name)
			}
		}
		sort.Strings(values)
		if len(values) > 0 {
			body += "\n## Entities\n\n"
			for _, value := range values {
				body += "- [[" + value + "]]\n"
			}
		}
	}
	return fm + body, nil
}

func safeVault(v string) (string, error) {
	if strings.TrimSpace(v) == "" {
		return "", errors.New("obsidian: --vault is required")
	}
	abs, err := filepath.Abs(v)
	if err != nil {
		return "", err
	}
	if st, e := os.Lstat(abs); e == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("obsidian: vault must not be a symlink")
	}
	cur := abs
	for {
		st, e := os.Lstat(cur)
		if e == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("obsidian: unsafe symlink path %s", cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return "", err
	}
	return abs, nil
}

func ensureSafeParents(vault, target string) error {
	rel, err := filepath.Rel(vault, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("obsidian: target escapes vault")
	}
	cur := vault
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		if st, e := os.Lstat(cur); e == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("obsidian: refusing symlink traversal at %s", cur)
		}
	}
	return nil
}
func projectFolder(p string) string {
	if p == "" {
		p = "default"
	}
	return filepath.Join("cortex", "projects", safeSlug(p))
}

var unsafeChars = regexp.MustCompile(`[^\pL\pN._-]+`)

func safeSlug(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = unsafeChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		s = "untitled"
	}
	r := []rune(s)
	if len(r) > 80 {
		r = r[:80]
	}
	return string(r)
}
func idHash(id string) string     { h := sha256.Sum256([]byte(id)); return hex.EncodeToString(h[:])[:12] }
func contentHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func loadState(p string) syncState {
	b, e := os.ReadFile(p)
	if e != nil {
		return syncState{Files: map[string]stateFile{}}
	}
	var s syncState
	_ = json.Unmarshal(b, &s)
	if s.Files == nil {
		s.Files = map[string]stateFile{}
	}
	return s
}
func shouldSkip(path string, prior stateFile, nextHash string) bool {
	b, e := os.ReadFile(path)
	if e != nil {
		return false
	}
	current := contentHash(string(b))
	if current == nextHash {
		return true
	}
	if prior.Hash != "" && current == prior.Hash {
		return false
	}
	return true
}
func findCortexFiles(vault string) (map[string]string, error) {
	out := map[string]string{}
	root := filepath.Join(vault, "cortex")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		fm, e := parseFrontmatter(string(b))
		if e == nil {
			out[path] = fm.CortexID
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	return out, err
}
func commitFile(staged, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	if _, e := os.Stat(target); e == nil {
		if err := os.Remove(target); err != nil {
			return err
		}
	}
	return os.Rename(staged, target)
}
func visibleID(m map[int64]*domain.Observation, id string) (*domain.Observation, bool) {
	for k, v := range m {
		if fmt.Sprintf("%d", k) == id {
			return v, true
		}
	}
	return nil, false
}
