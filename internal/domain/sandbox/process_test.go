package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProcessExecution(t *testing.T) {
	runner := NewDefaultRunner()
	runtimes := runner.AvailableRuntimes()
	if len(runtimes) == 0 {
		t.Skip("Skipping execution test: no supported runtimes in PATH")
	}

	ctx := context.Background()

	// 1. Try Go execution if available
	if _, ok := runtimes[LanguageGo]; ok {
		t.Run("GoExecution", func(t *testing.T) {
			code := `package main
import "fmt"
func main() {
	fmt.Println("cortex-sandbox-ok")
}`
			res, err := runner.Execute(ctx, ExecutionRequest{
				Language: LanguageGo,
				Code:     code,
				Timeout:  10 * time.Second,
			})
			if err != nil {
				t.Fatalf("Go execution failed: %v", err)
			}
			if !strings.Contains(res.Stdout, "cortex-sandbox-ok") {
				t.Fatalf("Expected 'cortex-sandbox-ok' in stdout, got: %q", res.Stdout)
			}
			if res.ExitCode != 0 {
				t.Fatalf("Expected exit code 0, got %d", res.ExitCode)
			}
		})
	}

	// 2. Try PowerShell execution if available
	if _, ok := runtimes[LanguagePowerShell]; ok {
		t.Run("PowerShellExecution", func(t *testing.T) {
			code := `Write-Output "cortex-powershell-ok"`
			res, err := runner.Execute(ctx, ExecutionRequest{
				Language: LanguagePowerShell,
				Code:     code,
				Timeout:  10 * time.Second,
			})
			if err != nil {
				t.Fatalf("PowerShell execution failed: %v", err)
			}
			if !strings.Contains(res.Stdout, "cortex-powershell-ok") {
				t.Fatalf("Expected 'cortex-powershell-ok' in stdout, got: %q", res.Stdout)
			}
		})
	}

	// 3. Test Timeout Handling
	t.Run("TimeoutHandling", func(t *testing.T) {
		var code string
		var lang Language
		if _, ok := runtimes[LanguageGo]; ok {
			lang = LanguageGo
			code = `package main
import "time"
func main() {
	time.Sleep(5 * time.Second)
}`
		} else if _, ok := runtimes[LanguagePowerShell]; ok {
			lang = LanguagePowerShell
			code = `Start-Sleep -Seconds 5`
		} else {
			t.Skip("Neither go nor powershell available for timeout test")
		}

		_, err := runner.Execute(ctx, ExecutionRequest{
			Language: lang,
			Code:     code,
			Timeout:  500 * time.Millisecond,
		})
		if err == nil {
			t.Fatal("Expected timeout error, got nil")
		}
		if !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("Expected 'timed out' in error, got: %v", err)
		}
	})
}
