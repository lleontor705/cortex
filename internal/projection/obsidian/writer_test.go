package obsidian

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

type fakeObs struct{ list []*domain.Observation }

func (f fakeObs) Save(context.Context, *domain.Observation) error { return nil }
func (f fakeObs) GetByID(_ context.Context, id int64) (*domain.Observation, error) {
	for _, o := range f.list {
		if o.ID == id {
			return o, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (f fakeObs) GetByTopicKey(context.Context, string, string) (*domain.Observation, error) {
	return nil, domain.ErrNotFound
}
func (f fakeObs) Update(context.Context, *domain.Observation) error { return nil }
func (f fakeObs) Delete(context.Context, int64) error               { return nil }
func (f fakeObs) List(context.Context, domain.ObservationFilter) ([]*domain.Observation, error) {
	return f.list, nil
}
func (f fakeObs) CountAll(context.Context) (int, error)           { return len(f.list), nil }
func (f fakeObs) CountByRoot(context.Context, int64) (int, error) { return 0, nil }
func (f fakeObs) GetBySource(context.Context, string, int) ([]*domain.Observation, error) {
	return nil, nil
}
func (f fakeObs) GetByType(context.Context, string, int) ([]*domain.Observation, error) {
	return nil, nil
}

type fakeGraph struct{ edges map[int64][]*domain.Edge }

type failRenameFS struct {
	fileSystem
	remaining int
	failed    bool
}

type failRemoveAllFS struct {
	fileSystem
	err error
}

func (f *failRemoveAllFS) RemoveAll(string) error { return f.err }

type closeErrorHandle struct{ fileHandle }

func (h closeErrorHandle) Close() error {
	_ = h.fileHandle.Close()
	return os.ErrClosed
}

type closeErrorFS struct{ fileSystem }

func (f closeErrorFS) Open(p string) (fileHandle, error) {
	h, err := f.fileSystem.Open(p)
	if err != nil {
		return nil, err
	}
	return closeErrorHandle{h}, nil
}

func (f *failRenameFS) Rename(old, new string) error {
	if f.remaining == 0 && !f.failed {
		f.failed = true
		return os.ErrPermission
	}
	f.remaining--
	return f.fileSystem.Rename(old, new)
}

func (f fakeGraph) CreateEdge(context.Context, *domain.Edge) error { return nil }
func (f fakeGraph) GetRelated(context.Context, int64, int) ([]*domain.Observation, error) {
	return nil, nil
}
func (f fakeGraph) DeleteEdge(context.Context, int64) error { return nil }
func (f fakeGraph) GetEdgesForObservation(_ context.Context, id int64) ([]*domain.Edge, error) {
	return f.edges[id], nil
}
func (f fakeGraph) GetEdge(context.Context, int64) (*domain.Edge, error) { return nil, nil }
func (f fakeGraph) GetEvolutionChain(context.Context, int64, int64) ([]*domain.Edge, error) {
	return nil, nil
}
func (f fakeGraph) CountEdgesByObservation(context.Context, int64) (int, error) { return 0, nil }
func (f fakeGraph) CountAllEdges(context.Context) (int, error)                  { return 0, nil }
func (f fakeGraph) GetContradictions(context.Context, time.Time, time.Time) ([]*domain.Edge, error) {
	return nil, nil
}
func (f fakeGraph) UpdateEdge(context.Context, *domain.Edge) error { return nil }

func TestExportWritesDeterministicObsidianProjectionAndWikilink(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	obs := []*domain.Observation{{ID: 1, Title: "First / note", Content: "body", Project: "my/project", Scope: "project", Type: "decision", CreatedAt: now, UpdatedAt: now}, {ID: 2, Title: "Second", Content: "two", Project: "my/project", Scope: "project", Type: "manual", CreatedAt: now, UpdatedAt: now}}
	e := NewWriter(fakeObs{obs}, fakeGraph{map[int64][]*domain.Edge{1: {{FromObsID: 1, ToObsID: 2, RelationType: domain.RelationReferences}}}}, nil, nil)
	result, err := e.Export(context.Background(), Options{Vault: dir})
	if err != nil {
		t.Fatal(err)
	}
	if result.Written != 2 {
		t.Fatalf("written=%d", result.Written)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "cortex", "projects", "my-project", "observations", "*.md"))
	if len(files) != 2 {
		t.Fatalf("files=%d", len(files))
	}
	b, _ := os.ReadFile(files[0])
	if !strings.Contains(string(b), "cortex_id:") {
		t.Fatal("missing cortex_id")
	}
	if !strings.Contains(string(b), "[[") {
		t.Fatal("missing wikilink")
	}
	result, err = e.Export(context.Background(), Options{Vault: dir})
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped == 0 {
		t.Fatal("expected incremental skip")
	}
}

func TestExportExcludesPersonalUnlessOptedIn(t *testing.T) {
	dir := t.TempDir()
	obs := []*domain.Observation{{ID: 1, Title: "private", Scope: domain.ScopePersonal, CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	e := NewWriter(fakeObs{obs}, nil, nil, nil)
	r, err := e.Export(context.Background(), Options{Vault: dir})
	if err != nil {
		t.Fatal(err)
	}
	if r.Written != 0 || len(r.Warnings) == 0 {
		t.Fatalf("privacy filter failed: %+v", r)
	}
}

func TestExportExcludesPrivateAndWarnsOnPersonalOptIn(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	obs := []*domain.Observation{{ID: 3, Title: "private", Scope: "private", CreatedAt: now, UpdatedAt: now}}
	e := NewWriter(fakeObs{obs}, nil, nil, nil)
	r, err := e.Export(context.Background(), Options{Vault: dir})
	if err != nil || r.Written != 0 || len(r.Warnings) == 0 {
		t.Fatalf("private filter failed: %+v %v", r, err)
	}
	r, err = e.Export(context.Background(), Options{Vault: dir, IncludePersonal: true})
	if err != nil || r.Written != 1 || len(r.Warnings) == 0 {
		t.Fatalf("opt-in warning failed: %+v %v", r, err)
	}
}

func TestExportRefusesUnownedOverwriteAndMatchesRenameByCortexID(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	obs := []*domain.Observation{{ID: 7, Title: "Stable", Content: "body", Project: "p", Scope: "project", CreatedAt: now, UpdatedAt: now}}
	e := NewWriter(fakeObs{obs}, nil, nil, nil)
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "cortex", "projects", "p", "observations", "*.md"))
	if len(files) != 1 {
		t.Fatal("initial projection missing")
	}
	rename := filepath.Join(filepath.Dir(files[0]), "human-name.md")
	if err := os.Rename(files[0], rename); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rename); !os.IsNotExist(err) {
		t.Fatal("renamed projection should be reconciled")
	}
	files, _ = filepath.Glob(filepath.Join(dir, "cortex", "projects", "p", "observations", "*.md"))
	if len(files) != 1 {
		t.Fatal("reconciled projection missing")
	}
	conflict := filepath.Join(filepath.Dir(files[0]), "different-"+idHash("7")+".md")
	if err := os.WriteFile(conflict, []byte("user note"), 0600); err != nil {
		t.Fatal(err)
	}
	obs[0].Title = "Different"
	if _, statErr := os.Stat(conflict); statErr != nil {
		t.Fatal(statErr)
	}
	_, err := e.Export(context.Background(), Options{Vault: dir})
	if err == nil {
		t.Fatal("expected overwrite conflict")
	}
}

func TestExportRejectsDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	root := filepath.Join(dir, "cortex", "projects", "p", "observations")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	content := "---\ncortex_id: \"1\"\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	e := NewWriter(fakeObs{[]*domain.Observation{{ID: 1, Title: "a", UpdatedAt: now}}}, nil, nil, nil)
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err == nil || !strings.Contains(err.Error(), "duplicate cortex_id") || !strings.Contains(err.Error(), "a.md") || !strings.Contains(err.Error(), "b.md") {
		t.Fatalf("expected duplicate ID error with both paths, got %v", err)
	}
}

func TestExportPreservesManifestOnNoop(t *testing.T) {
	dir := t.TempDir()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	e := NewWriter(fakeObs{[]*domain.Observation{{ID: 1, Title: "same", Content: "body", UpdatedAt: clock()}}}, nil, nil, nil)
	e.clock = clock
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ".cortex-sync-state.json")
	first, _ := os.ReadFile(p)
	e.clock = func() time.Time { return clock().Add(time.Hour) }
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(p)
	if string(first) != string(second) {
		t.Fatal("manifest changed during noop export")
	}
}

func TestSafeSlugWindowsNamesAndUnicode(t *testing.T) {
	for _, in := range []string{"CON", "name.", "name ", "a:b", "ｅxample"} {
		got := safeSlug(in)
		if got == "CON" || got == "name." || got == "name " || strings.ContainsAny(got, `\\/:*?\"<>|`) || got == "" {
			t.Fatalf("unsafe slug %q -> %q", in, got)
		}
	}
}

func TestSafeSlugRejectsWindowsDeviceBasenamesBeforeFirstDot(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{name: "con extensions", in: "CON.foo.bar"},
		{name: "con trailing space", in: "CON .md"},
		{name: "con trailing dot", in: "CON...md"},
		{name: "prn", in: "prn.txt"},
		{name: "aux", in: "AUX.anything"},
		{name: "nul", in: "NUL.1.2"},
		{name: "clock", in: "CLOCK$.md"},
		{name: "com1", in: "COM1.log"},
		{name: "com9", in: "com9.foo.bar"},
		{name: "lpt1", in: "LPT1.md"},
		{name: "lpt9", in: "lpt9...md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := safeSlug(tc.in)
			if isWindowsDeviceName(got) {
				t.Fatalf("safe slug remains a Windows device basename: %q -> %q", tc.in, got)
			}
			if !strings.HasPrefix(got, "_") {
				t.Fatalf("safe slug %q was not made visibly non-reserved: %q", tc.in, got)
			}
		})
	}
}

func TestWindowsDeviceNameNearMissesAreNotReservedOrColliding(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		alias      string
		reserved   bool
		collisions bool
	}{
		{name: "conx", input: "CONX", alias: "con", reserved: false, collisions: false},
		{name: "conx lowercase extension", input: "conx.md", alias: "CON.txt", reserved: false, collisions: false},
		{name: "com10", input: "COM10", alias: "com1", reserved: false, collisions: false},
		{name: "com10 lowercase extension", input: "com10.log", alias: "COM1.md", reserved: false, collisions: false},
		{name: "lpt10", input: "LPT10", alias: "lpt1", reserved: false, collisions: false},
		{name: "lpt10 lowercase extension", input: "lpt10.txt", alias: "LPT1.md", reserved: false, collisions: false},
		{name: "clock dollar x", input: "CLOCK$X", alias: "clock$", reserved: false, collisions: false},
		{name: "clock dollar x lowercase extension", input: "clock$x.anything", alias: "CLOCK$.md", reserved: false, collisions: false},
		{name: "con alias", input: "CON.foo", alias: "con", reserved: true, collisions: true},
		{name: "com1 alias", input: "com1.txt", alias: "COM1", reserved: true, collisions: true},
		{name: "lpt9 alias", input: "LPT9...md", alias: "lpt9", reserved: true, collisions: true},
		{name: "clock dollar alias", input: "CLOCK$...foo", alias: "clock$", reserved: true, collisions: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWindowsDeviceName(tc.input); got != tc.reserved {
				t.Fatalf("isWindowsDeviceName(%q) = %v, want %v", tc.input, got, tc.reserved)
			}
			if got := canonicalPathKey(tc.input) == canonicalPathKey(tc.alias); got != tc.collisions {
				t.Fatalf("canonical collision for %q and %q = %v, want %v", tc.input, tc.alias, got, tc.collisions)
			}
		})
	}
}

func TestCanonicalPathKeyCollapsesWindowsDeviceExtensionsAndVariants(t *testing.T) {
	cases := [][]string{
		{"CON", "con.txt", "CON.foo.bar", "CON .md", "con...txt", "Con .foo"},
		{"PRN", "prn.md", "PRN.foo.bar"},
		{"CLOCK$", "clock$.md", "CLOCK$...foo"},
		{"COM1", "com1.txt", "COM1.foo.bar"},
		{"LPT9", "lpt9.md", "LPT9...foo"},
	}
	for _, variants := range cases {
		want := canonicalPathKey(variants[0])
		for _, variant := range variants[1:] {
			if got := canonicalPathKey(variant); got != want {
				t.Fatalf("canonical key %q = %q, want %q", variant, got, want)
			}
		}
	}
}

func TestSafeSlugBoundsAndHashAreStable(t *testing.T) {
	long := strings.Repeat("x", 200)
	slug := safeSlug(long)
	if len([]rune(slug)) > 89 || !strings.HasSuffix(slug, "-"+idHash(long)[:8]) {
		t.Fatalf("bounded slug = %q", slug)
	}
	if idHash("1") != "6b86b273ff34" {
		t.Fatalf("unexpected stable id hash: %s", idHash("1"))
	}
	if err := ensureSafeParentsFS(osFS{}, t.TempDir(), filepath.Join(t.TempDir(), "..", "escape")); err == nil {
		t.Fatal("outside-vault path was accepted")
	}
}

func TestExportRollbackRestoresFilesAndManifestOnCommitFailure(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	obs := []*domain.Observation{{ID: 1, Title: "rollback", Content: "before", UpdatedAt: now}}
	e := NewWriter(fakeObs{obs}, nil, nil, nil)
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, ".cortex-sync-state.json")
	manifestBefore, _ := os.ReadFile(manifestPath)
	obs[0].Content = "after"
	e.fs = &failRenameFS{fileSystem: osFS{}, remaining: 1}
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err == nil {
		t.Fatal("expected injected commit failure")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "cortex", "projects", "default", "observations", "*.md"))
	if len(files) != 1 {
		t.Fatalf("rollback left %d files", len(files))
	}
	b, _ := os.ReadFile(files[0])
	if !strings.Contains(string(b), "before") {
		t.Fatal("rollback did not restore prior note")
	}
	manifestAfter, _ := os.ReadFile(manifestPath)
	if string(manifestBefore) != string(manifestAfter) {
		t.Fatal("rollback changed manifest")
	}
}

func TestCommitTransactionCleanupErrorIsReturnedOnlyWithoutPrimaryError(t *testing.T) {
	vault := t.TempDir()
	cleanupErr := errors.New("cleanup failed")
	fs := &failRemoveAllFS{fileSystem: osFS{}, err: cleanupErr}
	if err := commitTransaction(fs, vault, map[string][]byte{filepath.Join(vault, "note.md"): []byte("ok")}); !errors.Is(err, cleanupErr) {
		t.Fatalf("got %v, want cleanup error", err)
	}
	primaryErr := errors.New("write failed")
	writeFS := &failWriteFS{fileSystem: fs, err: primaryErr}
	if err := commitTransaction(writeFS, vault, map[string][]byte{filepath.Join(vault, "other.md"): []byte("bad")}); !errors.Is(err, primaryErr) {
		t.Fatalf("got %v, want primary error", err)
	}
}

type failWriteFS struct {
	fileSystem
	err error
}

func (f *failWriteFS) WriteFile(string, []byte, fs.FileMode) error { return f.err }

func TestSyncPathReturnsCloseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := syncPath(closeErrorFS{fileSystem: osFS{}}, path); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("got %v, want close error", err)
	}
}

func TestExportRejectsSymlinkedFileAndDirectory(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(string, string) error
	}{
		{"file", func(target, link string) error { return makeLink(target, link, false) }},
		{"directory", func(target, link string) error { return makeLink(target, link, true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "cortex", "projects", "p", "observations")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "outside")
			if tc.name == "directory" {
				if err := os.MkdirAll(target, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "link")
			if err := tc.make(target, link); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			linkInfo, statErr := os.Lstat(link)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if tc.name == "directory" && runtime.GOOS == "windows" && !isReparsePoint(link) {
				t.Fatal("directory link was not created as a Windows reparse point")
			}
			if tc.name == "file" && linkInfo.Mode()&os.ModeSymlink == 0 && runtime.GOOS != "windows" {
				t.Fatal("file link was not created as a symlink")
			}
			_, err := NewWriter(fakeObs{[]*domain.Observation{{ID: 1, Title: "n", Project: "p", UpdatedAt: time.Now()}}}, nil, nil, nil).Export(context.Background(), Options{Vault: dir})
			if err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("got %v, want symlink rejection", err)
			}
			if tc.name == "file" {
				got, readErr := os.ReadFile(target)
				if readErr != nil || string(got) != "x" {
					t.Fatalf("outside file changed: bytes=%q err=%v", got, readErr)
				}
			} else if _, statErr := os.Stat(filepath.Join(target, "n-"+idHash("1")+".md")); !os.IsNotExist(statErr) {
				t.Fatalf("outside directory was mutated: %v", statErr)
			}
		})
	}
}

func makeLink(target, link string, directory bool) error {
	if runtime.GOOS == "windows" && directory {
		return exec.Command("cmd", "/c", "mklink", "/J", link, target).Run()
	}
	return os.Symlink(target, link)
}

type guardFileInfo struct {
	name string
	mode fs.FileMode
}

func (f guardFileInfo) Name() string       { return f.name }
func (f guardFileInfo) Size() int64        { return 0 }
func (f guardFileInfo) Mode() fs.FileMode  { return f.mode }
func (f guardFileInfo) ModTime() time.Time { return time.Time{} }
func (f guardFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f guardFileInfo) Sys() any           { return nil }

func TestUnsafePathGuardRejectsInjectedReparseFileAndDirectory(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode fs.FileMode
	}{
		{name: "file", mode: 0600},
		{name: "directory", mode: fs.ModeDir | 0700},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := guardFileInfo{name: tc.name, mode: tc.mode}
			if !unsafePathInfo(filepath.Join("vault", tc.name), info, func(string) bool { return true }) {
				t.Fatalf("injected reparse metadata was accepted for %s", tc.name)
			}
		})
	}
}

func TestExportRejectsCaseInsensitiveCollisionAndPreservesSource(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	obs := &domain.Observation{ID: 1, Title: "Note", Content: "source", Project: "p", UpdatedAt: now}
	before := *obs
	e := NewWriter(fakeObs{[]*domain.Observation{obs}}, nil, nil, nil)
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cortex", "projects", "p", "observations", "NOTE-"+idHash("1")+".md")
	if err := os.WriteFile(path, []byte("user"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("got %v, want collision", err)
	}
	if !reflect.DeepEqual(before, *obs) {
		t.Fatal("export mutated source observation")
	}
}

func TestCanonicalPathKeyModelsCrossPlatformCollisions(t *testing.T) {
	cases := []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{name: "case", left: "cortex/projects/Note.md", right: "CORTEX\\Projects\\NOTE.MD", want: true},
		{name: "unicode composed", left: "café.md", right: "cafe\u0301.md", want: true},
		{name: "windows trailing dot and space", left: "note.md", right: "note.md. ", want: true},
		{name: "windows reserved device extension", left: "CON.md", right: "con.txt", want: true},
		{name: "distinct", left: "note-a.md", right: "note-b.md", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canonicalPathKey(tc.left) == canonicalPathKey(tc.right); got != tc.want {
				t.Fatalf("canonical collision=%v, want %v (%q, %q)", got, tc.want, tc.left, tc.right)
			}
		})
	}
}

func TestExportCanonicalCollisionTableOnLinuxFilesystem(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		existing  string
		wantError bool
	}{
		{name: "case fold", title: "Note", existing: "NOTE-" + idHash("1") + ".md", wantError: true},
		{name: "unicode normalization", title: "Café", existing: "cafe\u0301-" + idHash("1") + ".md", wantError: true},
		{name: "unrelated file", title: "Note", existing: "another-note.md", wantError: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "cortex", "projects", "p", "observations")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, tc.existing), []byte("user file"), 0600); err != nil {
				t.Fatal(err)
			}
			o := &domain.Observation{ID: 1, Title: tc.title, Project: "p", UpdatedAt: time.Now()}
			_, err := NewWriter(fakeObs{[]*domain.Observation{o}}, nil, nil, nil).Export(context.Background(), Options{Vault: dir})
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
			if tc.wantError && !strings.Contains(err.Error(), "case-insensitive") {
				t.Fatalf("got %v, want explicit case-insensitive collision", err)
			}
		})
	}
}

func TestExportCanonicalAliasCollisionLeavesUnownedFileUntouched(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cortex", "projects", "p", "observations")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "NOTE-"+idHash("1")+".md")
	before := []byte("unowned user note\n")
	if err := os.WriteFile(alias, before, 0600); err != nil {
		t.Fatal(err)
	}
	o := &domain.Observation{ID: 1, Title: "Note", Project: "p", UpdatedAt: time.Now()}
	if _, err := NewWriter(fakeObs{[]*domain.Observation{o}}, nil, nil, nil).Export(context.Background(), Options{Vault: dir}); err == nil {
		t.Fatal("expected unowned canonical alias collision")
	}
	after, err := os.ReadFile(alias)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("unowned alias was modified: bytes=%q err=%v", after, err)
	}
}

func TestExportRewritesExactOwnerPath(t *testing.T) {
	dir := t.TempDir()
	o := &domain.Observation{ID: 1, Title: "Note", Content: "before", Project: "p", UpdatedAt: time.Now()}
	e := NewWriter(fakeObs{[]*domain.Observation{o}}, nil, nil, nil)
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "cortex", "projects", "p", "observations", "note-"+idHash("1")+".md")
	o.Content = "after"
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(got), "after") {
		t.Fatalf("exact owner was not rewritten: bytes=%q err=%v", got, err)
	}
}

func TestSafeVaultRejectsExplicitSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "vault-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := safeVault(link); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("got %v, want explicit vault symlink rejection", err)
	}
}

func TestSafeVaultResolvesMacOSVarAlias(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS /var alias regression")
	}
	dir := t.TempDir()
	if !strings.HasPrefix(dir, "/private/") {
		t.Skipf("temporary directory is not under /private: %s", dir)
	}
	alias := "/var/" + strings.TrimPrefix(dir, "/private/")
	want, err := filepath.EvalSymlinks(alias)
	if err != nil {
		t.Fatal(err)
	}
	got, err := safeVault(alias)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("resolved vault=%q, want %q", got, want)
	}
}

func TestExportGoldenBytesAndOutsideVaultProtection(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	obs := &domain.Observation{ID: 1, Title: "Golden", Content: "Golden fixture for deterministic export and YAML escaping.", Project: "demo", Scope: "project", Type: "decision", CreatedAt: now, UpdatedAt: now}
	e := NewWriter(fakeObs{[]*domain.Observation{obs}}, nil, nil, nil)
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cortex", "projects", "demo", "observations", "golden-"+idHash("1")+".md")
	want, err := os.ReadFile(filepath.Join("testdata", "golden", "observation.md"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden bytes differ:\n%s", got)
	}
	outside := filepath.Join(dir, "outside.md")
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("unexpected outside-vault mutation: %v", err)
	}
}
