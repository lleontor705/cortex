package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/v2/internal/cli"
	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/platform"
	serverplatform "github.com/lleontor705/cortex/v2/internal/platform/server"
)

// version is set by GoReleaser via ldflags at build time.
// Falls back to the module version from go install.
var version = "dev"

type serverReindexJob interface {
	ReindexProject(context.Context, string) (*serverplatform.ReindexResult, error)
	Close() error
}

var openServerReindexJob = func(ctx context.Context, cfg config.Config) (serverReindexJob, error) {
	return serverplatform.OpenReindexJob(ctx, cfg)
}

type serverInvocation struct {
	configPath string
	reindex    bool
	projectID  string
}

func parseServerInvocation(args []string) (serverInvocation, error) {
	var inv serverInvocation
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--config":
			if i+1 >= len(args) || args[i+1] == "" {
				return inv, fmt.Errorf("--config requires a path")
			}
			i++
			inv.configPath = args[i]
		case len(arg) > len("--config=") && arg[:len("--config=")] == "--config=":
			inv.configPath = arg[len("--config="):]
		case arg == "reindex" && !inv.reindex:
			inv.reindex = true
		case arg == "--project-id" && inv.reindex:
			if i+1 >= len(args) || inv.projectID != "" {
				return inv, fmt.Errorf("reindex requires exactly one --project-id")
			}
			i++
			inv.projectID = args[i]
		case len(arg) > len("--project-id=") && arg[:len("--project-id=")] == "--project-id=" && inv.reindex:
			if inv.projectID != "" {
				return inv, fmt.Errorf("reindex requires exactly one --project-id")
			}
			inv.projectID = arg[len("--project-id="):]
		default:
			return inv, fmt.Errorf("unknown server argument %q", arg)
		}
	}
	if inv.reindex {
		project, err := uuid.Parse(inv.projectID)
		if err != nil || project == uuid.Nil {
			return inv, fmt.Errorf("reindex --project-id must be a public UUID")
		}
		inv.projectID = project.String()
	}
	return inv, nil
}

func main() {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	}
	cli.Version = version
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runContext(ctx, os.Args, os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runContext(context.Background(), args, stdout, stderr)
}

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	mode, cleanArgs := platform.ParseMode(args)
	switch mode {
	case platform.ModeLocal:
		// Byte-identical local path: cli.Run delegates to app.Open internally
		// via openApp(). No double-wiring — platform.Select is proven by tests;
		// the live execution path preserves the existing main→cli→app chain.
		return cli.Run(cleanArgs, stdout, stderr)
	case platform.ModeServer:
		invocation, err := parseServerInvocation(cleanArgs)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "cortex: server arguments: %v\n", err)
			return 2
		}
		cfg, err := config.Load(invocation.configPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "cortex: server config: %v\n", err)
			return 2
		}
		if invocation.reindex {
			job, err := openServerReindexJob(ctx, *cfg)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "cortex: server reindex bootstrap: %v\n", err)
				return 2
			}
			defer func() { _ = job.Close() }()
			result, err := job.ReindexProject(ctx, invocation.projectID)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "cortex: server reindex: %v\n", err)
				return 1
			}
			_, _ = fmt.Fprintf(stdout, "cortex: server reindex complete project=%s corpus=%d upserted=%d reembedded=%d skipped=%d batches=%d\n",
				invocation.projectID, result.Total, result.Upserted, result.ReEmbedded, result.Skipped, result.Batches)
			return 0
		}
		rt, err := serverplatform.Open(ctx, *cfg)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "cortex: server bootstrap: %v\n", err)
			return 2
		}
		defer func() { _ = rt.Close() }()
		baseURL := "http://" + rt.Address()
		_, _ = fmt.Fprintf(stdout, "cortex: server endpoint %s\ncortex: readiness %s/health\ncortex: API %s/api/\ncortex: MCP %s/mcp\n", baseURL, baseURL, baseURL, baseURL)
		if err := rt.Serve(ctx); err != nil {
			_, _ = fmt.Fprintf(stderr, "cortex: server: %v\n", err)
			return 1
		}
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "cortex: unknown mode %q (use --mode local or --mode server)\n", mode)
		return 2
	}
}
