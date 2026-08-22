package vectorhydration

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("VECTOR_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("helper stdout\n")
	_, _ = os.Stderr.WriteString("helper stderr\n")
	os.Exit(7)
}

func TestOSExecutorCapturesBothStreamsAndIdentity(t *testing.T) {
	x := (OSExecutor{}).Execute(context.Background(), ProcessRequest{Executable: os.Args[0], Args: []string{"-test.run=TestHelperProcess"}, Env: []string{"VECTOR_HELPER=1"}, Identity: "run/run1/block/1"})
	if x.ExitCode != 7 || !strings.Contains(string(x.Stdout), "helper stdout") || !strings.Contains(string(x.Stderr), "helper stderr") {
		t.Fatalf("execution lost streams: %#v", x)
	}
	if x.PID <= 0 || !strings.Contains(x.ProcessIdentity, "start:") || !strings.Contains(x.ProcessIdentity, "run/run1/block/1") {
		t.Fatalf("incomplete identity: %q", x.ProcessIdentity)
	}
}

func TestEffectiveEnvIsStrictCaseInsensitiveAndSorted(t *testing.T) {
	if _, err := effectiveEnv([]string{"gomaxprocs=1", "GOMAXPROCS=1"}, 1); err == nil {
		t.Fatal("case variant duplicate accepted")
	}
	if _, err := effectiveEnv([]string{"AWS_SECRET_ACCESS_KEY=x"}, 1); err == nil {
		t.Fatal("secret environment accepted")
	}
	e, err := effectiveEnv([]string{"GOOS=windows", "GOMAXPROCS=1"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(e); i++ {
		if strings.SplitN(e[i-1], "=", 2)[0] >= strings.SplitN(e[i], "=", 2)[0] {
			t.Fatalf("environment not sorted: %v", e)
		}
	}
}
