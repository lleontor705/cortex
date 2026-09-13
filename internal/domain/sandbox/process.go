package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultTimeout   = 15 * time.Second
	MaxTimeout       = 60 * time.Second
	DefaultMaxOutput = 5 * 1024 * 1024  // 5 MB
	MaxAllowedOutput = 20 * 1024 * 1024 // 20 MB
)

// DefaultRunner implements Runner using host subprocesses.
type DefaultRunner struct {
	runtimes map[Language]string
}

// NewDefaultRunner creates a DefaultRunner detecting available runtimes.
func NewDefaultRunner() *DefaultRunner {
	return &DefaultRunner{
		runtimes: DetectRuntimes(),
	}
}

// AvailableRuntimes returns detected language runtimes.
func (r *DefaultRunner) AvailableRuntimes() map[Language]string {
	res := make(map[Language]string, len(r.runtimes))
	for k, v := range r.runtimes {
		res[k] = v
	}
	return res
}

// Execute runs code in a temporary subprocess sandbox with bounded limits.
func (r *DefaultRunner) Execute(ctx context.Context, req ExecutionRequest) (*ExecutionResult, error) {
	lang, err := NormalizeLanguage(string(req.Language))
	if err != nil {
		return nil, err
	}

	binPath, ok := r.runtimes[lang]
	if !ok || binPath == "" {
		// Try dynamic detection in case PATH changed
		r.runtimes = DetectRuntimes()
		binPath, ok = r.runtimes[lang]
		if !ok || binPath == "" {
			var available []string
			for k := range r.runtimes {
				available = append(available, string(k))
			}
			return nil, fmt.Errorf("runtime for %q is not installed or not in PATH (available runtimes: %s)",
				lang, strings.Join(available, ", "))
		}
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	} else if timeout > MaxTimeout {
		timeout = MaxTimeout
	}

	maxOutput := req.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutput
	} else if maxOutput > MaxAllowedOutput {
		maxOutput = MaxAllowedOutput
	}

	// Prepare temp script file
	ext := scriptExtension(lang)
	tmpDir, err := os.MkdirTemp("", "cortex-sandbox-*")
	if err != nil {
		return nil, fmt.Errorf("create temp sandbox dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	scriptPath := filepath.Join(tmpDir, "script"+ext)
	if err := os.WriteFile(scriptPath, []byte(req.Code), 0600); err != nil {
		return nil, fmt.Errorf("write script file: %w", err)
	}

	// Build command invocation
	cmdName, cmdArgs := buildCommandArgs(lang, binPath, scriptPath, req.Args)

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, cmdName, cmdArgs...)
	cmd.Dir = tmpDir

	// Setup bounded environment
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "CORTEX_SANDBOX=1")

	// If a target file path was supplied, check and inject
	if req.FilePath != "" {
		absPath, err := filepath.Abs(req.FilePath)
		if err == nil {
			cmd.Env = append(cmd.Env, "FILE_PATH="+absPath)
			if fileBytes, readErr := os.ReadFile(absPath); readErr == nil {
				// Inject file content as env var if under 500KB, otherwise script reads FILE_PATH
				if len(fileBytes) <= 500*1024 {
					cmd.Env = append(cmd.Env, "FILE_CONTENT="+string(fileBytes))
				}
			}
		}
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdoutBuf, limit: maxOutput}
	cmd.Stderr = &limitedWriter{w: &stderrBuf, limit: maxOutput}

	startTime := time.Now()
	runErr := cmd.Run()
	duration := time.Since(startTime)

	result := &ExecutionResult{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		Duration: duration,
		RawBytes: stdoutBuf.Len() + stderrBuf.Len(),
	}

	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("execution timed out after %v", timeout)
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("run subprocess: %w", runErr)
		}
	} else {
		result.ExitCode = 0
	}

	if stdoutBuf.Len() >= maxOutput || stderrBuf.Len() >= maxOutput {
		result.Truncated = true
	}

	return result, nil
}

func scriptExtension(lang Language) string {
	switch lang {
	case LanguagePython:
		return ".py"
	case LanguageNode, LanguageBun:
		return ".js"
	case LanguageGo:
		return ".go"
	case LanguagePowerShell:
		return ".ps1"
	case LanguageBash:
		return ".sh"
	default:
		return ".txt"
	}
}

func buildCommandArgs(lang Language, binPath, scriptPath string, extraArgs []string) (string, []string) {
	switch lang {
	case LanguagePython:
		args := []string{"-u", scriptPath}
		return binPath, append(args, extraArgs...)
	case LanguageNode:
		args := []string{scriptPath}
		return binPath, append(args, extraArgs...)
	case LanguageBun:
		args := []string{"run", scriptPath}
		return binPath, append(args, extraArgs...)
	case LanguageGo:
		args := []string{"run", scriptPath}
		return binPath, append(args, extraArgs...)
	case LanguagePowerShell:
		args := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath}
		return binPath, append(args, extraArgs...)
	case LanguageBash:
		args := []string{scriptPath}
		return binPath, append(args, extraArgs...)
	default:
		return binPath, append([]string{scriptPath}, extraArgs...)
	}
}

// limitedWriter wraps an io.Writer and silences writes exceeding limit.
type limitedWriter struct {
	w       io.Writer
	written int
	limit   int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.written >= l.limit {
		return len(p), nil
	}
	remaining := l.limit - l.written
	if len(p) > remaining {
		n, err := l.w.Write(p[:remaining])
		l.written += n
		if err != nil {
			return n, err
		}
		return len(p), nil
	}
	n, err := l.w.Write(p)
	l.written += n
	return n, err
}
