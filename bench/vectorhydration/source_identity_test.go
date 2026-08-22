package vectorhydration

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archiveFixture(t *testing.T, name string, kind byte) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: 1, Typeflag: kind}); err != nil {
		t.Fatal(err)
	}
	if kind == tar.TypeReg {
		_, _ = tw.Write([]byte("x"))
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestExtractImmutableArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"/absolute", "../escape", `..\escape`} {
		t.Run(name, func(t *testing.T) {
			if err := ExtractImmutableArchive(archiveFixture(t, name, tar.TypeReg), t.TempDir()); err == nil {
				t.Fatal("accepted unsafe archive path")
			}
		})
	}
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeChar} {
		if err := ExtractImmutableArchive(archiveFixture(t, "entry", kind), t.TempDir()); err == nil {
			t.Fatal("accepted non-regular archive entry")
		}
	}
}

func TestExtractImmutableArchiveCreatesOnlyRegularFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "extract")
	if err := ExtractImmutableArchive(archiveFixture(t, "pkg/file.go", tar.TypeReg), dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "pkg", "file.go"))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("extracted file: %v", err)
	}
}

func TestCanonicalArgvIsPathIndependent(t *testing.T) {
	a := HashArgv(ProcessRequest{Executable: `C:\go\bin\go.exe`, Dir: `C:\src`, Args: []string{"test", "-c", "-trimpath", "./internal/store/sqlite", "-o", `C:\tmp\a`}})
	b := HashArgv(ProcessRequest{Executable: `/usr/local/go/bin/go`, Dir: `/src`, Args: []string{"test", "-c", "-trimpath", "./internal/store/sqlite", "-o", `/tmp/b`}})
	if a != b {
		t.Fatalf("path leaked into argv digest: %s != %s", a, b)
	}
	if strings.Join(CanonicalArgv(ProcessRequest{Executable: "go", Dir: "/src", Args: []string{"test", "-c", "-trimpath", "./internal/store/sqlite", "-o", "/tmp/out"}}), " ") != "<tool> test -c -trimpath ./internal/store/sqlite -o <output> <source>" {
		t.Fatal("unexpected canonical argv")
	}
	if CanonicalArgv(ProcessRequest{Executable: "go", Dir: "relative", Args: []string{"test", "-c", "-trimpath", "./internal/store/sqlite", "-o", "/tmp/out"}}) != nil {
		t.Fatal("accepted relative dir in canonical argv")
	}
}

func TestStableNames(t *testing.T) {
	input := []string{"gamma", "alpha", "beta"}
	got := StableNames(input)
	want := []string{"alpha", "beta", "gamma"}
	if !slicesEqual(got, want) {
		t.Fatalf("StableNames=%v, want %v", got, want)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestExtractSourceLifecycle(t *testing.T) {
	data := archiveFixture(t, "pkg/file.go", tar.TypeReg)
	arch := ArchivedSource{Commit: "0123456789abcdef0123456789abcdef01234567", ArchiveSHA256: HashArchive(data), Bytes: data}
	extracted, err := ExtractSource(arch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(extracted.Path, "pkg", "file.go")); err != nil {
		t.Fatalf("extracted file missing: %v", err)
	}
	if err := extracted.Remove(extracted.Path); err != nil {
		t.Fatalf("remove extracted source: %v", err)
	}
}

type identityFakeExecutor struct {
	archiveOut []byte
	versionOut []byte
	buildOut   func(string)
}

func (f *identityFakeExecutor) Execute(_ context.Context, r ProcessRequest) Execution {
	if r.Executable == "git" && len(r.Args) > 0 && r.Args[0] == "archive" {
		return Execution{Stdout: f.archiveOut, ExitCode: 0}
	}
	if len(r.Args) > 0 && r.Args[0] == "version" {
		return Execution{Stdout: f.versionOut, ExitCode: 0}
	}
	if len(r.Args) >= 6 && r.Args[0] == "test" && r.Args[1] == "-c" {
		if f.buildOut != nil {
			f.buildOut(r.Args[5])
		} else {
			_ = os.WriteFile(r.Args[5], []byte("binary"), 0o755)
		}
		return Execution{ExitCode: 0}
	}
	return Execution{ExitCode: 1}
}

func TestArchiveSourceRejectsInvalidCommitAndEmpty(t *testing.T) {
	ctx := context.Background()
	f := &identityFakeExecutor{archiveOut: nil}
	if _, err := ArchiveSource(ctx, f, t.TempDir(), "short"); err == nil {
		t.Fatal("accepted short commit")
	}
	if _, err := ArchiveSource(ctx, f, t.TempDir(), strings.Repeat("0", 40)); err == nil {
		t.Fatal("accepted all-zero commit")
	}
	if _, err := ArchiveSource(ctx, f, t.TempDir(), strings.Repeat("a", 40)); err == nil {
		t.Fatal("accepted empty archive output")
	}
}

func TestToolVersionParsing(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		out     string
		wantVer string
		wantErr bool
	}{
		{"go version go1.26.5 windows/amd64\n", "go1.26.5", false},
		{"go version go1.25.0 linux/amd64\n", "go1.25.0", false},
		{"not-go version 1.0\n", "", true},
		{"go version /usr/bin/go\n", "", true},
	} {
		f := &identityFakeExecutor{versionOut: []byte(tc.out)}
		v, err := ToolVersion(ctx, f, "go")
		if tc.wantErr && err == nil {
			t.Fatalf("ToolVersion(%q) accepted invalid output", tc.out)
		}
		if !tc.wantErr && (err != nil || v != tc.wantVer) {
			t.Fatalf("ToolVersion(%q)=%q, err=%v, want %q", tc.out, v, err, tc.wantVer)
		}
	}
}

func TestPrepareIdentityPipelineSuccess(t *testing.T) {
	ctx := context.Background()
	toolFile := filepath.Join(t.TempDir(), "go.exe")
	if err := os.WriteFile(toolFile, []byte("go binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	tarBytes := archiveFixture(t, "file.go", tar.TypeReg)
	f := &identityFakeExecutor{
		archiveOut: tarBytes,
		versionOut: []byte("go version go1.26.5 windows/amd64\n"),
	}
	srcDir := t.TempDir()
	binDir := t.TempDir()
	binaryPath := filepath.Join(binDir, "test.bin")
	commit := strings.Repeat("a", 40)
	buildReq := ProcessRequest{
		Executable: toolFile,
		Dir:        srcDir,
		Args:       []string{"test", "-c", "-trimpath", "./internal/store/sqlite", "-o", binaryPath},
	}
	identity, err := PrepareIdentity(ctx, f, srcDir, commit, buildReq, binaryPath)
	if err != nil {
		t.Fatalf("PrepareIdentity failed: %v", err)
	}
	if identity.SourceCommit != commit || identity.ToolVersion != "go1.26.5" || identity.BuildIdentity != ApprovedBuildIdentity {
		t.Fatalf("unexpected identity result: %+v", identity)
	}
}
