package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Language represents a programming language supported by the sandbox.
type Language string

const (
	LanguagePython     Language = "python"
	LanguageNode       Language = "node"
	LanguageBun        Language = "bun"
	LanguageGo         Language = "go"
	LanguagePowerShell Language = "powershell"
	LanguageBash       Language = "bash"
)

// NormalizeLanguage normalizes input language aliases (e.g. "py", "js", "ts", "pwsh", "sh").
func NormalizeLanguage(lang string) (Language, error) {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "python", "python3", "py":
		return LanguagePython, nil
	case "node", "nodejs", "javascript", "js", "typescript", "ts":
		return LanguageNode, nil
	case "bun":
		return LanguageBun, nil
	case "go", "golang":
		return LanguageGo, nil
	case "powershell", "pwsh", "ps1":
		return LanguagePowerShell, nil
	case "bash", "sh", "shell":
		return LanguageBash, nil
	default:
		return "", fmt.Errorf("unsupported language: %q (supported: python, node, bun, go, powershell, bash)", lang)
	}
}

// ExecutionRequest specifies code to execute, optional file context, and execution constraints.
type ExecutionRequest struct {
	Language       Language
	Code           string
	FilePath       string
	Timeout        time.Duration
	Args           []string
	MaxOutputBytes int
}

// ExecutionResult contains the outcome of the sandbox execution.
type ExecutionResult struct {
	Stdout    string        `json:"stdout"`
	Stderr    string        `json:"stderr"`
	ExitCode  int           `json:"exit_code"`
	Duration  time.Duration `json:"duration"`
	Truncated bool          `json:"truncated"`
	RawBytes  int           `json:"raw_bytes"`
}

// Runner defines the interface for executing scripts in isolated subprocesses.
type Runner interface {
	Execute(ctx context.Context, req ExecutionRequest) (*ExecutionResult, error)
	AvailableRuntimes() map[Language]string
}

// DetectRuntimes returns a map of all supported languages available in the host PATH.
func DetectRuntimes() map[Language]string {
	available := make(map[Language]string)

	checkBinary := func(lang Language, candidates ...string) {
		for _, bin := range candidates {
			if path, err := exec.LookPath(bin); err == nil {
				available[lang] = path
				return
			}
		}
	}

	checkBinary(LanguagePython, "python3", "python")
	checkBinary(LanguageBun, "bun")
	checkBinary(LanguageNode, "node", "nodejs")
	checkBinary(LanguageGo, "go")
	checkBinary(LanguagePowerShell, "pwsh", "powershell")
	checkBinary(LanguageBash, "bash", "sh")

	return available
}
