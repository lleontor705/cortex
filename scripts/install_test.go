package scripts

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallScriptDefinesWarnBeforeUse(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test location")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "install.sh"))
	if err != nil {
		t.Fatalf("read install script: %v", err)
	}
	script := strings.ReplaceAll(string(data), "\r\n", "\n")
	definition := strings.Index(script, "warn()")
	use := strings.Index(script, "  warn \"")
	if definition < 0 {
		t.Fatal("install script does not define warn()")
	}
	if use < 0 {
		t.Fatal("install script does not exercise warn()")
	}
	if definition > use {
		t.Fatal("install script uses warn before defining it")
	}
}
