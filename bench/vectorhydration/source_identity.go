package vectorhydration

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	maxArchiveBytes         = 64 << 20
	maxArchiveEntries       = 10000
	maxArchiveDepth         = 32
	maxArchiveFileBytes     = 16 << 20
	maxArchiveExpandedBytes = 256 << 20
)

var winVolumePattern = regexp.MustCompile(`^[A-Za-z]:/`)

type ArchivedSource struct {
	Commit, ArchiveSHA256 string
	Bytes                 []byte
}

type ExtractedSource struct {
	Path   string
	Remove func(string) error
}

func CanonicalArgv(r ProcessRequest) []string {
	if len(r.Args) != 6 || r.Args[0] != "test" || r.Args[1] != "-c" || r.Args[2] != "-trimpath" || r.Args[4] != "-o" || r.Executable == "" || r.Dir == "" || !isAbsolutePath(r.Dir) || r.Args[3] == "" || !isAbsolutePath(r.Args[5]) {
		return nil
	}
	if r.Args[3] != "./internal/store/sqlite" {
		return nil
	}
	return []string{"<tool>", "test", "-c", "-trimpath", r.Args[3], "-o", "<output>", "<source>"}
}

func HashArgv(r ProcessRequest) string {
	a := CanonicalArgv(r)
	if a == nil {
		return ""
	}
	var b bytes.Buffer
	for _, v := range a {
		fmt.Fprintf(&b, "%d:%s\n", len(v), v)
	}
	s := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(s[:])
}

func HashArchive(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }

func ToolIdentity(executable string) (path, digest string, err error) {
	path, err = exec.LookPath(executable)
	if err != nil {
		return "", "", fmt.Errorf("resolve tool %q: %w", executable, err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("tool executable must be a regular non-symlink file")
	}
	digest, err = hashRegularFile(path)
	return path, digest, err
}

func isAbsolutePath(s string) bool {
	s = strings.ReplaceAll(s, "\\", "/")
	return filepath.IsAbs(s) || winVolumePattern.MatchString(s) || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//")
}

func ToolVersion(ctx context.Context, executor Executor, tool string) (string, error) {
	x := executor.Execute(ctx, ProcessRequest{Executable: tool, Args: []string{"version"}})
	if x.ExitCode != 0 || x.Err != nil {
		return "", fmt.Errorf("tool version failed: %w", executionError(x))
	}
	f := strings.Fields(string(x.Stdout))
	if len(f) < 3 || f[0] != "go" || !strings.HasPrefix(f[2], "go") || strings.ContainsAny(f[2], "/\\@$ ") {
		return "", errors.New("tool version output is not canonical Go version output")
	}
	return f[2], nil
}

func VerifyToolIdentity(ctx context.Context, executor Executor, tool, digest, version string) error {
	path, got, err := ToolIdentity(tool)
	if err != nil || path != tool {
		return errors.New("resolved tool was replaced")
	}
	if got != digest {
		return errors.New("tool digest changed after use")
	}
	v, err := ToolVersion(ctx, executor, path)
	if err != nil || v != version {
		return errors.New("tool version changed after use")
	}
	return nil
}

type ArchiveOps struct {
	RemoveAll func(string) error
	Stat      func(string) (os.FileInfo, error)
}

func ExtractImmutableArchive(data []byte, destination string) error {
	return ExtractImmutableArchiveWithOps(data, destination, ArchiveOps{RemoveAll: os.RemoveAll, Stat: os.Stat})
}

func ExtractImmutableArchiveWithOps(data []byte, destination string, ops ArchiveOps) (err error) {
	if len(data) == 0 || len(data) > maxArchiveBytes {
		return errors.New("source archive exceeds byte limit")
	}
	if ops.RemoveAll == nil || ops.Stat == nil {
		return errors.New("archive operations are incomplete")
	}
	if _, e := ops.Stat(destination); e == nil {
		return errors.New("archive destination must be absent")
	} else if !errors.Is(e, os.ErrNotExist) {
		return fmt.Errorf("stat archive destination: %w", e)
	}
	if err = os.Mkdir(destination, 0o700); err != nil {
		return fmt.Errorf("create archive destination: %w", err)
	}
	defer func() {
		if err != nil {
			if e := ops.RemoveAll(destination); e != nil {
				err = fmt.Errorf("%w; cleanup archive destination: %v", err, e)
			}
		}
	}()
	tr := tar.NewReader(bytes.NewReader(data))
	seen, folds := map[string]bool{}, map[string]bool{}
	entries, expanded := 0, 0
	for {
		h, e := tr.Next()
		if errors.Is(e, io.EOF) {
			return nil
		}
		if e != nil {
			return fmt.Errorf("read source archive: %w", e)
		}
		entries++
		if entries > maxArchiveEntries {
			return errors.New("source archive has too many entries")
		}
		name := strings.ReplaceAll(h.Name, "\\", "/")
		// Reject raw dot segments before cleaning; otherwise safe/../file aliases away.
		for _, p := range strings.Split(name, "/") {
			if p == "." || p == ".." {
				return errors.New("source archive contains a traversal path")
			}
		}
		if name == "" || strings.HasPrefix(name, "/") || filepath.VolumeName(name) != "" || strings.HasPrefix(name, "//") || winVolumePattern.MatchString(name) {
			return errors.New("source archive contains an absolute path")
		}
		clean := pathCleanSlash(name)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return errors.New("source archive contains a traversal path")
		}
		if strings.Count(clean, "/")+1 > maxArchiveDepth {
			return errors.New("source archive path is too deep")
		}
		if seen[name] {
			return errors.New("source archive contains duplicate paths")
		}
		fold := strings.ToLower(clean)
		if folds[fold] {
			return errors.New("source archive contains case-fold aliases")
		}
		seen[name], folds[fold] = true, true
		dst := filepath.Join(destination, filepath.FromSlash(clean))
		switch h.Typeflag {
		case tar.TypeDir:
			if e := os.MkdirAll(dst, 0o700); e != nil {
				return e
			}
		case tar.TypeReg:
			if h.Size < 0 || h.Size > maxArchiveFileBytes || h.Size > int64(maxArchiveExpandedBytes-expanded) {
				return errors.New("source archive file exceeds size limit")
			}
			if e := os.MkdirAll(filepath.Dir(dst), 0o700); e != nil {
				return e
			}
			f, e := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if e != nil {
				return e
			}
			_, ce := io.CopyN(f, tr, h.Size)
			xe := f.Close()
			if ce != nil {
				return ce
			}
			if xe != nil {
				return xe
			}
			expanded += int(h.Size)
		default:
			return errors.New("source archive contains a link, device, or special entry")
		}
	}
}
func pathCleanSlash(name string) string {
	parts := strings.Split(name, "/")
	stack := make([]string, 0, len(parts))
	for _, p := range parts {
		switch p {
		case "", ".":
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			} else {
				return ".."
			}
		default:
			stack = append(stack, p)
		}
	}
	return strings.Join(stack, "/")
}

func ArchiveSource(ctx context.Context, executor Executor, root, commit string) (ArchivedSource, error) {
	if !commitPattern.MatchString(commit) || strings.Trim(commit, "0") == "" {
		return ArchivedSource{}, errors.New("git archive requires an exact non-zero 40-hex commit")
	}
	x := executor.Execute(ctx, ProcessRequest{Executable: "git", Args: []string{"archive", "--format=tar", commit}, Dir: root})
	if x.ExitCode != 0 || x.Err != nil {
		return ArchivedSource{}, fmt.Errorf("git archive failed: %w", executionError(x))
	}
	if len(x.Stdout) == 0 {
		return ArchivedSource{}, errors.New("git archive was empty")
	}
	b := append([]byte(nil), x.Stdout...)
	return ArchivedSource{Commit: commit, ArchiveSHA256: HashArchive(b), Bytes: b}, nil
}

func ExtractSource(source ArchivedSource) (ExtractedSource, error) {
	dir, err := os.MkdirTemp("", ".cortex-source-")
	if err != nil {
		return ExtractedSource{}, err
	}
	if err = os.Remove(dir); err != nil {
		return ExtractedSource{}, err
	}
	if err = ExtractImmutableArchive(source.Bytes, dir); err != nil {
		return ExtractedSource{}, err
	}
	return ExtractedSource{Path: dir, Remove: os.RemoveAll}, nil
}

// PrepareIdentity is the sole trust pipeline: tool precheck, exact archive, owned extraction, build, tool postcheck, binary hash.
func PrepareIdentity(ctx context.Context, executor Executor, sourceRoot, commit string, build ProcessRequest, binary string) (identity BinaryIdentity, err error) {
	if executor == nil || CanonicalArgv(build) == nil {
		return identity, errors.New("approved build request is required")
	}
	tool, toolSum, err := ToolIdentity(build.Executable)
	if err != nil {
		return identity, err
	}
	version, err := ToolVersion(ctx, executor, tool)
	if err != nil {
		return identity, err
	}
	source, err := ArchiveSource(ctx, executor, sourceRoot, commit)
	if err != nil {
		return identity, err
	}
	extracted, err := ExtractSource(source)
	if err != nil {
		return identity, err
	}
	defer func() {
		if e := extracted.Remove(extracted.Path); err == nil && e != nil {
			err = fmt.Errorf("cleanup extracted source: %w", e)
		}
	}()
	request := build
	request.Executable, request.Dir = tool, extracted.Path
	request.Args = append([]string(nil), build.Args...)
	if request.Args[5] != binary {
		return identity, errors.New("build output does not match binary path")
	}
	x := executor.Execute(ctx, request)
	if x.ExitCode != 0 || x.Err != nil {
		return identity, fmt.Errorf("approved build failed: %w", executionError(x))
	}
	if err = VerifyToolIdentity(ctx, executor, tool, toolSum, version); err != nil {
		return identity, err
	}
	bs, err := hashRegularFile(binary)
	if err != nil {
		return identity, err
	}
	identity = BinaryIdentity{SchemaVersion: BinaryIdentitySchemaVersion, BinarySHA256: bs, SourceCommit: source.Commit, SourceTreeSHA256: source.ArchiveSHA256, ToolSHA256: toolSum, ArgvSHA256: HashArgv(request), ToolVersion: version, BuildIdentity: ApprovedBuildIdentity}
	return identity, identity.Validate()
}

func StableNames(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}
