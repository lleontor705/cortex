package vectorhydration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type BenchmarkBinary struct {
	SourceCommit, ToolIdentity, BinaryPath, SHA256 string
	Build                                          ProcessRequest
	BuildArgv                                      []string
}

func PrepareBenchmarkBinary(ctx context.Context, manifest Manifest, sourceRoot, outputPath string, executor Executor) (BenchmarkBinary, error) {
	return prepareBenchmarkBinary(ctx, manifest, sourceRoot, outputPath, executor, sourceDeps{
		hash: hashRegularFile, link: os.Link, remove: os.RemoveAll, stat: os.Lstat,
	})
}

type sourceDeps struct {
	hash   func(string) (string, error)
	link   func(string, string) error
	remove func(string) error
	stat   func(string) (os.FileInfo, error)
}

func prepareBenchmarkBinary(ctx context.Context, manifest Manifest, sourceRoot, outputPath string, executor Executor, deps sourceDeps) (BenchmarkBinary, error) {
	var result BenchmarkBinary
	if executor == nil {
		return result, errors.New("source executor is required")
	}
	if err := manifest.Validate(); err != nil {
		return result, fmt.Errorf("validate manifest: %w", err)
	}
	root, out, parent, err := canonicalPaths(sourceRoot, outputPath)
	if err != nil {
		return result, err
	}
	if err := outsideRoot(root, out); err != nil {
		return result, err
	}
	if err := absentNonSymlink(out, deps.stat); err != nil {
		return result, err
	}
	if err := verifySource(ctx, executor, root, manifest.SourceCommit); err != nil {
		return result, err
	}
	tmpDir, err := os.MkdirTemp(parent, ".cortex-benchmark-*")
	if err != nil {
		return result, fmt.Errorf("create private benchmark output: %w", err)
	}
	defer func() { _ = deps.remove(tmpDir) }()
	tmpPath := filepath.Join(tmpDir, "benchmark")
	build := ProcessRequest{Executable: "go", Args: []string{"test", "-c", "-trimpath", manifest.BenchmarkPackage, "-o", tmpPath}, Dir: root}
	if x := executor.Execute(ctx, build); x.ExitCode != 0 || x.Err != nil {
		return result, fmt.Errorf("benchmark build failed: %w", executionError(x))
	}
	if err := verifySource(ctx, executor, root, manifest.SourceCommit); err != nil {
		return result, fmt.Errorf("post-build source verification: %w", err)
	}
	sum, err := deps.hash(tmpPath)
	if err != nil {
		return result, fmt.Errorf("hash benchmark binary: %w", err)
	}
	if err := deps.link(tmpPath, out); err != nil {
		return result, fmt.Errorf("publish benchmark binary: %w", err)
	}
	owned := true
	defer func() {
		if owned {
			_ = deps.remove(out)
		}
	}()
	finalSum, err := deps.hash(out)
	if err != nil {
		return result, fmt.Errorf("verify published benchmark binary: %w", err)
	}
	if finalSum != sum {
		return result, errors.New("published benchmark binary changed during verification")
	}
	owned = false
	return BenchmarkBinary{SourceCommit: manifest.SourceCommit, Build: build, BuildArgv: append([]string(nil), build.Args...), ToolIdentity: build.Executable, BinaryPath: out, SHA256: sum}, nil
}
func canonicalPaths(sourceRoot, outputPath string) (string, string, string, error) {
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return "", "", "", fmt.Errorf("source root: %w", err)
	}
	if err := validatePathComponents(root, true); err != nil {
		return "", "", "", fmt.Errorf("source root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", "", fmt.Errorf("source root: %w", err)
	}
	out, err := filepath.Abs(outputPath)
	if err != nil {
		return "", "", "", fmt.Errorf("binary path: %w", err)
	}
	parent := filepath.Dir(out)
	if err := validatePathComponents(parent, true); err != nil {
		return "", "", "", fmt.Errorf("binary parent: %w", err)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return "", "", "", fmt.Errorf("binary parent: %w", err)
	}
	return root, filepath.Join(parent, filepath.Base(out)), parent, nil
}
func verifySource(ctx context.Context, executor Executor, root, want string) error {
	for _, args := range [][]string{{"rev-parse", "HEAD"}, {"status", "--porcelain", "--untracked-files=all"}} {
		x := executor.Execute(ctx, ProcessRequest{Executable: "git", Args: args, Dir: root})
		if x.ExitCode != 0 || x.Err != nil {
			return fmt.Errorf("git %s failed: %w", args[0], executionError(x))
		}
		if args[0] == "rev-parse" {
			if strings.TrimSpace(string(x.Stdout)) != want {
				return fmt.Errorf("HEAD does not match manifest source_commit")
			}
		} else if len(x.Stdout) != 0 || len(x.Stderr) != 0 {
			return errors.New("source tree is dirty or has untracked files")
		}
	}
	return nil
}
func outsideRoot(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("compare source and output paths: %w", err)
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return errors.New("benchmark output must be outside source root")
	}
	return nil
}
func absentNonSymlink(path string, stat func(string) (os.FileInfo, error)) error {
	info, err := stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect benchmark output: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("benchmark output must not be a symlink")
	}
	return errors.New("benchmark output already exists")
}
func hashRegularFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("benchmark output is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func executionError(x Execution) error {
	if x.Err == nil {
		return fmt.Errorf("exit code %d", x.ExitCode)
	}
	return x.Err
}
