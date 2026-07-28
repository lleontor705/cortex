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
	"unicode"

	"github.com/lleontor705/cortex/internal/domain"
	"golang.org/x/text/unicode/norm"
)

type Options struct {
	Vault, Project  string
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
	clock        func() time.Time
	fs           fileSystem
}

func NewWriter(o domain.ObservationRepository, g domain.GraphRepository, e domain.EntityRepository, s domain.ScoringRepository) *Writer {
	return &Writer{observations: o, graph: g, entities: e, scoring: s, clock: func() time.Time { return time.Now().UTC() }, fs: osFS{}}
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

type fileSystem interface {
	Lstat(string) (os.FileInfo, error)
	Stat(string) (os.FileInfo, error)
	ReadFile(string) ([]byte, error)
	WriteFile(string, []byte, fs.FileMode) error
	MkdirAll(string, fs.FileMode) error
	MkdirTemp(string, string) (string, error)
	Rename(string, string) error
	Remove(string) error
	RemoveAll(string) error
	Open(string) (fileHandle, error)
}
type fileHandle interface {
	Sync() error
	Close() error
}
type osFS struct{}

func (osFS) Lstat(p string) (os.FileInfo, error)               { return os.Lstat(p) }
func (osFS) Stat(p string) (os.FileInfo, error)                { return os.Stat(p) }
func (osFS) ReadFile(p string) ([]byte, error)                 { return os.ReadFile(p) }
func (osFS) WriteFile(p string, b []byte, m fs.FileMode) error { return os.WriteFile(p, b, m) }
func (osFS) MkdirAll(p string, m fs.FileMode) error            { return os.MkdirAll(p, m) }
func (osFS) MkdirTemp(d, p string) (string, error)             { return os.MkdirTemp(d, p) }
func (osFS) Rename(a, b string) error                          { return os.Rename(a, b) }
func (osFS) Remove(p string) error                             { return os.Remove(p) }
func (osFS) RemoveAll(p string) error                          { return os.RemoveAll(p) }
func (osFS) Open(p string) (fileHandle, error)                 { return os.Open(p) }

func (w *Writer) Export(ctx context.Context, opts Options) (Result, error) {
	var result Result
	vault, err := safeVault(opts.Vault)
	if err != nil {
		return result, err
	}
	if w == nil || w.observations == nil {
		return result, errors.New("obsidian: observation repository is required")
	}
	if w.clock == nil {
		w.clock = func() time.Time { return time.Now().UTC() }
	}
	if w.fs == nil {
		w.fs = osFS{}
	}
	obs, err := w.observations.List(ctx, domain.ObservationFilter{Project: opts.Project, Limit: 100000, IncludeArchived: true})
	if err != nil {
		return result, fmt.Errorf("obsidian: list observations: %w", err)
	}
	sort.Slice(obs, func(i, j int) bool { return obs[i].ID < obs[j].ID })
	visible := map[int64]*domain.Observation{}
	for _, o := range obs {
		if o == nil {
			continue
		}
		private := strings.EqualFold(o.Scope, domain.ScopePersonal) || strings.EqualFold(o.Scope, "private")
		if private && !opts.IncludePersonal {
			result.Warnings = append(result.Warnings, fmt.Sprintf("excluded personal observation #%d (use --include-personal to export)", o.ID))
			continue
		}
		if _, ok := visible[o.ID]; ok {
			return result, fmt.Errorf("obsidian: duplicate cortex_id %d", o.ID)
		}
		visible[o.ID] = o
	}
	if opts.IncludePersonal {
		result.Warnings = append(result.Warnings, "including personal/private observations by explicit opt-in")
	}
	statePath := filepath.Join(vault, ".cortex-sync-state.json")
	prior, priorBytes := loadState(w.fs, statePath)
	paths, err := findCortexFiles(w.fs, vault)
	if err != nil {
		return result, err
	}
	byID := map[string]string{}
	for p, id := range paths {
		if old, ok := byID[id]; ok {
			return result, fmt.Errorf("obsidian: duplicate cortex_id %s in %s and %s", id, old, p)
		}
		byID[id] = p
	}
	changes := map[string][]byte{}
	next := syncState{Version: 1, ExportedAt: prior.ExportedAt, Files: map[string]stateFile{}}
	if next.ExportedAt.IsZero() {
		next.ExportedAt = w.clock()
	}
	for _, o := range visible {
		id := fmt.Sprintf("%d", o.ID)
		target := filepath.Join(vault, projectFolder(o.Project), "observations", safeSlug(o.Title)+"-"+idHash(id)+".md")
		for existing := range paths {
			if filepath.Clean(existing) != filepath.Clean(target) && strings.EqualFold(existing, target) {
				return result, fmt.Errorf("obsidian: case-insensitive filename collision between %s and %s", existing, target)
			}
		}
		if old, ok := byID[id]; ok && filepath.Clean(old) != filepath.Clean(target) {
			b, e := w.fs.ReadFile(old)
			if e != nil {
				return result, e
			}
			if prior.Files[id].Hash == "" || contentHash(string(b)) != prior.Files[id].Hash {
				return result, fmt.Errorf("obsidian: edited renamed note %s; refusing reconciliation", old)
			}
			if _, e = w.fs.Stat(target); e == nil {
				return result, fmt.Errorf("obsidian: rename conflict at %s", target)
			}
			changes[old] = nil
			changes[target] = b
			byID[id] = target
		}
		content, e := w.render(ctx, o, visible)
		if e != nil {
			return result, e
		}
		hash := contentHash(content)
		rel, _ := filepath.Rel(vault, target)
		next.Files[id] = stateFile{Path: rel, Hash: hash, UpdatedAt: o.UpdatedAt}
		if old, ok := byID[id]; ok && filepath.Clean(old) == filepath.Clean(target) && shouldSkipFS(w.fs, old, prior.Files[id], hash) {
			result.Skipped++
			continue
		}
		if st, e := w.fs.Lstat(target); e == nil {
			if st.Mode()&os.ModeSymlink != 0 {
				return result, fmt.Errorf("obsidian: refusing symlink target %s", target)
			}
			if _, owned := byID[id]; !owned {
				return result, fmt.Errorf("obsidian: case-insensitive filename collision at %s", target)
			}
		}
		if e := ensureSafeParentsFS(w.fs, vault, filepath.Dir(target)); e != nil {
			return result, e
		}
		changes[target] = []byte(content)
		result.Written++
	}
	for id := range prior.Files {
		if _, ok := visibleID(visible, id); !ok {
			result.Stale++
		}
	}
	if len(changes) == 0 && priorBytes != nil {
		return result, nil
	}
	next.ExportedAt = w.clock()
	manifest, _ := json.MarshalIndent(next, "", "  ")
	manifest = append(manifest, '\n')
	changes[statePath] = manifest
	if err := commitTransaction(w.fs, vault, changes); err != nil {
		return result, err
	}
	return result, nil
}

func (w *Writer) render(ctx context.Context, o *domain.Observation, all map[int64]*domain.Observation) (string, error) {
	fm, e := RenderFrontmatter(o, FrontmatterOptions{})
	if e != nil {
		return "", e
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
			if t, ok := all[id]; ok {
				links[fmt.Sprintf("[[%s-%s]]", safeSlug(t.Title), idHash(fmt.Sprintf("%d", t.ID)))] = true
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
	return fm + body, nil
}

func safeVault(v string) (string, error) {
	if strings.TrimSpace(v) == "" {
		return "", errors.New("obsidian: --vault is required")
	}
	abs, e := filepath.Abs(v)
	if e != nil {
		return "", e
	}
	if st, e := os.Lstat(abs); e == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("obsidian: vault must not be a symlink")
	}
	for cur := abs; ; cur = filepath.Dir(cur) {
		if st, e := os.Lstat(cur); e == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("obsidian: unsafe symlink path %s", cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
	}
	if e := os.MkdirAll(abs, 0700); e != nil {
		return "", e
	}
	return abs, nil
}
func ensureSafeParentsFS(f fileSystem, vault, target string) error {
	rel, e := filepath.Rel(vault, target)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("obsidian: target escapes vault")
	}
	cur := vault
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		if st, e := f.Lstat(cur); e == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("obsidian: refusing symlink traversal at %s", cur)
		}
	}
	return f.MkdirAll(target, 0700)
}

func projectFolder(p string) string {
	if p == "" {
		p = "default"
	}
	return filepath.Join("cortex", "projects", safeSlug(p))
}

var unsafeChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)

func safeSlug(s string) string {
	s = norm.NFC.String(strings.TrimSpace(strings.ToLower(s)))
	s = unsafeChars.ReplaceAllString(s, "-")
	s = strings.TrimRight(s, " .-")
	if s == "" {
		s = "untitled"
	}
	upper := strings.ToUpper(s)
	reserved := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}
	if reserved[upper] || (len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9') {
		s = "_" + s
	}
	r := []rune(s)
	if len(r) > 80 {
		r = r[:80]
		s = string(r) + "-" + idHash(s)[:8]
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "untitled"
		}
	}
	return s
}
func idHash(id string) string     { h := sha256.Sum256([]byte(id)); return hex.EncodeToString(h[:])[:12] }
func contentHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func loadState(f fileSystem, p string) (syncState, []byte) {
	b, e := f.ReadFile(p)
	if e != nil {
		return syncState{Files: map[string]stateFile{}}, nil
	}
	var s syncState
	if json.Unmarshal(b, &s) != nil || s.Files == nil {
		s.Files = map[string]stateFile{}
	}
	return s, b
}
func shouldSkipFS(f fileSystem, p string, prior stateFile, next string) bool {
	b, e := f.ReadFile(p)
	if e != nil {
		return false
	}
	return contentHash(string(b)) == next
}
func findCortexFiles(f fileSystem, vault string) (map[string]string, error) {
	out := map[string]string{}
	root := filepath.Join(vault, "cortex")
	e := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		st, e := f.Lstat(path)
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("obsidian: refusing symlink %s", path)
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		b, e := f.ReadFile(path)
		if e != nil {
			return e
		}
		fm, e := parseFrontmatter(string(b))
		if e == nil {
			out[path] = fm.CortexID
		}
		return nil
	})
	if errors.Is(e, os.ErrNotExist) {
		return out, nil
	}
	return out, e
}
func commitTransaction(f fileSystem, vault string, changes map[string][]byte) (err error) {
	stage, e := f.MkdirTemp(filepath.Join(vault, ".cortex-staging"), "export-")
	if e != nil {
		if e = f.MkdirAll(filepath.Join(vault, ".cortex-staging"), 0700); e != nil {
			return e
		}
		stage, e = f.MkdirTemp(filepath.Join(vault, ".cortex-staging"), "export-")
	}
	if e != nil {
		return e
	}
	defer func() {
		cleanupErr := f.RemoveAll(stage)
		if err == nil && cleanupErr != nil {
			err = cleanupErr
		}
	}()
	keys := make([]string, 0, len(changes))
	for p := range changes {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	manifest := filepath.Join(vault, ".cortex-sync-state.json")
	for i, p := range keys {
		if p == manifest {
			keys = append(append(keys[:i], keys[i+1:]...), p)
			break
		}
	}
	for _, p := range keys {
		if changes[p] == nil {
			continue
		}
		rel, _ := filepath.Rel(vault, p)
		q := filepath.Join(stage, rel)
		if e = f.MkdirAll(filepath.Dir(q), 0700); e != nil {
			return e
		}
		if e = f.WriteFile(q, changes[p], 0600); e != nil {
			return e
		}
	}
	for _, p := range keys {
		if changes[p] == nil {
			continue
		}
		rel, _ := filepath.Rel(vault, p)
		if e := syncPath(f, filepath.Join(stage, rel)); e != nil {
			return e
		}
	}
	type backup struct{ target, bak string }
	backs := []backup{}
	installed := []string{}
	rollback := func() {
		for i := len(installed) - 1; i >= 0; i-- {
			_ = f.Remove(installed[i])
		}
		for i := len(backs) - 1; i >= 0; i-- {
			_ = f.Remove(backs[i].target)
			_ = f.Rename(backs[i].bak, backs[i].target)
		}
	}
	for _, p := range keys {
		if _, e = f.Lstat(p); e == nil {
			b := p + ".cortex-backup"
			_ = f.Remove(b)
			if e = f.Rename(p, b); e != nil {
				rollback()
				return e
			}
			backs = append(backs, backup{p, b})
		}
		if changes[p] == nil {
			continue
		}
		rel, _ := filepath.Rel(vault, p)
		if e = f.Rename(filepath.Join(stage, rel), p); e != nil {
			rollback()
			return e
		}
		installed = append(installed, p)
		if e = syncPath(f, filepath.Dir(p)); e != nil {
			rollback()
			return e
		}
	}
	for _, b := range backs {
		if e = f.Remove(b.bak); e != nil {
			return e
		}
	}
	if e = syncPath(f, vault); e != nil {
		return e
	}
	return nil
}
func syncPath(f fileSystem, p string) error {
	h, e := f.Open(p)
	if e != nil {
		return nil
	}
	// Directory/file fsync is best effort: Windows may deny Sync on handles
	// opened through the portable os.File API, while POSIX filesystems support it.
	_ = h.Sync()
	return h.Close()
}
func visibleID(m map[int64]*domain.Observation, id string) (*domain.Observation, bool) {
	for k, v := range m {
		if fmt.Sprintf("%d", k) == id {
			return v, true
		}
	}
	return nil, false
}
