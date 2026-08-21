package vectorhydration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ProcessRequest struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	Dir        string   `json:"dir"`
	Env        []string `json:"env"`
	Identity   string   `json:"identity,omitempty"`
}
type Execution struct {
	Stdout, Stderr        []byte
	ExitCode              int
	StartedAt, FinishedAt time.Time
	Err                   error
	PID                   int
	ProcessIdentity       string
}

// OSExecutor captures both streams and makes process identity unambiguous for a run.
type OSExecutor struct{}

func (OSExecutor) Execute(ctx context.Context, r ProcessRequest) Execution {
	started := time.Now().UTC()
	c := exec.CommandContext(ctx, r.Executable, r.Args...)
	c.Dir = r.Dir
	c.Env = r.Env
	var out, stderr bytes.Buffer
	c.Stdout, c.Stderr = &out, &stderr
	err := c.Run()
	x := Execution{Stdout: out.Bytes(), Stderr: stderr.Bytes(), StartedAt: started, FinishedAt: time.Now().UTC(), Err: err, ExitCode: -1}
	if c.Process != nil {
		x.PID = c.Process.Pid
		x.ProcessIdentity = fmt.Sprintf("pid:%d:start:%d:identity:%s", x.PID, started.UnixNano(), r.Identity)
	}
	if err == nil {
		x.ExitCode = 0
	} else {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			x.ExitCode = ee.ExitCode()
		}
	}
	return x
}

type Executor interface {
	Execute(context.Context, ProcessRequest) Execution
}

var safeEnvNames = map[string]bool{
	"GOMAXPROCS": true, "GOOS": true, "GOARCH": true, "CGO_ENABLED": true,
	"GOTOOLCHAIN": true, "GOTRACEBACK": true, "GOEXPERIMENT": true, "GODEBUG": true,
	"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "TEMP": true, "TMP": true,
}

func effectiveEnv(extra []string, cell int) ([]string, error) {
	values := map[string]string{}
	for _, e := range os.Environ() {
		addSafeEnv(values, e)
	}
	seen := map[string]bool{}
	for _, e := range extra {
		i := strings.IndexByte(e, '=')
		if i <= 0 {
			return nil, errors.New("environment entry must be NAME=value")
		}
		key := strings.ToUpper(e[:i])
		if !safeEnvNames[key] {
			return nil, fmt.Errorf("unsafe environment variable %q", e[:i])
		}
		if seen[key] {
			return nil, fmt.Errorf("duplicate environment variable %q", key)
		}
		seen[key] = true
		values[key] = e[i+1:]
	}
	want := strconv.Itoa(cell)
	if v, ok := values["GOMAXPROCS"]; ok && v != want {
		return nil, errors.New("GOMAXPROCS does not match schedule")
	}
	values["GOMAXPROCS"] = want
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+values[k])
	}
	return out, nil
}
func addSafeEnv(values map[string]string, e string) {
	i := strings.IndexByte(e, '=')
	if i <= 0 {
		return
	}
	key := strings.ToUpper(e[:i])
	if safeEnvNames[key] {
		values[key] = e[i+1:]
	}
}
