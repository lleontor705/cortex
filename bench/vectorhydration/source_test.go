package vectorhydration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type sourceFake struct {
	commands         []ProcessRequest
	heads, statuses  []string
	buildErr         error
	gitErr, artifact string
	hook             func(string)
}

func (f *sourceFake) Execute(_ context.Context, r ProcessRequest) Execution {
	f.commands = append(f.commands, r)
	if r.Executable == "git" {
		if f.gitErr == r.Args[0] {
			return Execution{Err: errors.New("git failure"), ExitCode: 1}
		}
		switch r.Args[0] {
		case "rev-parse":
			return Execution{Stdout: []byte(pop(&f.heads)), ExitCode: 0}
		default:
			return Execution{Stdout: []byte(pop(&f.statuses)), ExitCode: 0}
		}
	}
	if f.buildErr != nil {
		return Execution{Err: f.buildErr, ExitCode: 1}
	}
	if f.hook != nil {
		f.hook(r.Args[len(r.Args)-1])
	}
	switch f.artifact {
	case "missing":
		return Execution{}
	case "symlink":
		_ = os.Symlink("elsewhere", r.Args[len(r.Args)-1])
	case "dir":
		_ = os.Mkdir(r.Args[len(r.Args)-1], 0o700)
	default:
		return Execution{Err: os.WriteFile(r.Args[len(r.Args)-1], []byte("binary"), 0o755), ExitCode: 0}
	}
	return Execution{}
}
func pop(v *[]string) string {
	if len(*v) == 0 {
		return ""
	}
	x := (*v)[0]
	*v = (*v)[1:]
	return x
}
func assertPrivateGone(t *testing.T, f *sourceFake) {
	t.Helper()
	if len(f.commands) < 3 {
		return
	}
	private := f.commands[2].Args[5]
	for _, path := range []string{private, filepath.Dir(private)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("private path survived: %q: %v", path, err)
		}
	}
}
func TestPrepareBenchmarkBinarySequenceAndProvenance(t *testing.T) {
	m, root, out := testManifest(t), t.TempDir(), filepath.Join(t.TempDir(), "binary")
	f := &sourceFake{heads: []string{m.SourceCommit, m.SourceCommit}, statuses: []string{"", ""}}
	parent, _ := filepath.EvalSymlinks(filepath.Dir(out))
	f.hook = func(private string) {
		dir := filepath.Dir(private)
		if filepath.Dir(dir) != parent || dir == filepath.Dir(out) {
			t.Fatalf("private directory is not directly under destination parent: %q", dir)
		}
		if runtime.GOOS != "windows" {
			i, err := os.Stat(dir)
			if err != nil || i.Mode().Perm() != 0o700 {
				t.Fatalf("private directory mode: %v", err)
			}
		}
	}
	got, err := PrepareBenchmarkBinary(context.Background(), m, root, out, f)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, _ := filepath.EvalSymlinks(root)
	if got.SourceCommit != m.SourceCommit || got.ToolIdentity != "go" || got.BinaryPath != filepath.Join(parent, filepath.Base(out)) {
		t.Fatalf("identity: %+v path=%q want=%q", got, filepath.Clean(got.BinaryPath), filepath.Clean(out))
	}
	sum := sha256.Sum256([]byte("binary"))
	if got.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("bad hash")
	}
	want := [][]string{{"rev-parse", "HEAD"}, {"status", "--porcelain", "--untracked-files=all"}, {"test", "-c", "-trimpath", BenchmarkPackage, "-o"}, {"rev-parse", "HEAD"}, {"status", "--porcelain", "--untracked-files=all"}}
	for i, r := range f.commands {
		if r.Dir != canonicalRoot {
			t.Fatalf("cwd %d: %q", i, r.Dir)
		}
		if i != 2 && !reflect.DeepEqual(r.Args, want[i]) {
			t.Fatalf("argv %d: %v", i, r.Args)
		}
	}
	private := f.commands[2].Args[5]
	wantBuild := ProcessRequest{Executable: "go", Args: []string{"test", "-c", "-trimpath", BenchmarkPackage, "-o", private}, Dir: canonicalRoot}
	wantArgv := append([]string(nil), wantBuild.Args...)
	if !reflect.DeepEqual(got.Build, wantBuild) || !reflect.DeepEqual(got.BuildArgv, wantArgv) {
		t.Fatalf("build provenance: %+v", got)
	}
	if private == out || filepath.Dir(private) == filepath.Dir(out) {
		t.Fatalf("not private: %q", private)
	}
	assertPrivateGone(t, f)
}
func TestPrepareBenchmarkBinaryFailureMatrix(t *testing.T) {
	m := testManifest(t)
	for _, tc := range []struct {
		name     string
		f        func(*sourceFake)
		contains string
	}{
		{"build", func(f *sourceFake) { f.buildErr = errors.New("compiler") }, "build failed"},
		{"missing", func(f *sourceFake) { f.artifact = "missing" }, "hash benchmark"},
		{"symlink", func(f *sourceFake) { f.artifact = "symlink" }, "hash benchmark"},
		{"nonregular", func(f *sourceFake) { f.artifact = "dir" }, "hash benchmark"},
		{"head drift", func(f *sourceFake) { f.heads = []string{m.SourceCommit, "changed"} }, "post-build"},
		{"dirty", func(f *sourceFake) { f.statuses = []string{"", " M x"} }, "post-build"},
		{"git status failure", func(f *sourceFake) { f.gitErr = "status" }, "status failed"},
		{"git head failure", func(f *sourceFake) { f.gitErr = "rev-parse" }, "rev-parse failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &sourceFake{heads: []string{m.SourceCommit, m.SourceCommit}, statuses: []string{"", ""}}
			tc.f(f)
			_, err := PrepareBenchmarkBinary(context.Background(), m, t.TempDir(), filepath.Join(t.TempDir(), "out"), f)
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error: %v", err)
			}
			assertPrivateGone(t, f)
		})
	}
}
func TestPrepareBenchmarkBinaryRejectsOutputAndPublicationFailures(t *testing.T) {
	m, root, parent := testManifest(t), t.TempDir(), t.TempDir()
	if _, err := PrepareBenchmarkBinary(context.Background(), m, root, filepath.Join(root, "out"), &sourceFake{}); err == nil {
		t.Fatal("output inside source")
	}
	existing := filepath.Join(parent, "existing")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareBenchmarkBinary(context.Background(), m, root, existing, &sourceFake{heads: []string{m.SourceCommit}}); err == nil {
		t.Fatal("existing output")
	}
	deps := sourceDeps{hash: hashRegularFile, link: os.Link, remove: os.RemoveAll, stat: os.Lstat}
	calls := 0
	deps.hash = func(path string) (string, error) {
		calls++
		if calls == 2 {
			return "", errors.New("hash race")
		}
		return hashRegularFile(path)
	}
	out := filepath.Join(parent, "hash-fail")
	f := &sourceFake{heads: []string{m.SourceCommit, m.SourceCommit}, statuses: []string{"", ""}}
	if _, err := prepareBenchmarkBinary(context.Background(), m, root, out, f, deps); err == nil {
		t.Fatal("accepted hash failure")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("owned output survived: %v", err)
	}
	link := filepath.Join(parent, "race")
	f = &sourceFake{heads: []string{m.SourceCommit, m.SourceCommit}, statuses: []string{"", ""}, hook: func(_ string) { _ = os.WriteFile(link, []byte("raced"), 0o600) }}
	if _, err := PrepareBenchmarkBinary(context.Background(), m, root, link, f); err == nil || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("race result: %v", err)
	}
	b, _ := os.ReadFile(link)
	if string(b) != "raced" {
		t.Fatal("race destination removed")
	}
	for _, tc := range []struct {
		name, want string
		link       func(string, string) error
		hash       func(string) (string, error)
	}{
		{"link failure", "publish", func(string, string) error { return errors.New("link race") }, hashRegularFile},
		{"final hash failure", "verify published", os.Link, func(path string) (string, error) {
			if strings.HasSuffix(path, "final-hash") {
				return "", errors.New("final hash")
			}
			return hashRegularFile(path)
		}},
		{"final hash mismatch", "changed during", os.Link, func(path string) (string, error) {
			if strings.HasSuffix(path, "mismatch") {
				return "different", nil
			}
			return hashRegularFile(path)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := filepath.Join(parent, map[string]string{"link failure": "link-fail", "final hash failure": "final-hash", "final hash mismatch": "mismatch"}[tc.name])
			f := &sourceFake{heads: []string{m.SourceCommit, m.SourceCommit}, statuses: []string{"", ""}}
			d := sourceDeps{hash: tc.hash, link: tc.link, remove: os.RemoveAll, stat: os.Lstat}
			if _, err := prepareBenchmarkBinary(context.Background(), m, root, o, f, d); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error: %v", err)
			}
			if _, err := os.Stat(o); !os.IsNotExist(err) {
				t.Fatalf("owned output survived: %v", err)
			}
			assertPrivateGone(t, f)
		})
	}
}
