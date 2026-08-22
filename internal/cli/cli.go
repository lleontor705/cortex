package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lleontor705/cortex/internal/app"
	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/domain"
	cortexhttp "github.com/lleontor705/cortex/internal/http"
	"github.com/lleontor705/cortex/internal/mcp"
	"github.com/lleontor705/cortex/internal/ollama"
	projectpkg "github.com/lleontor705/cortex/internal/project"
	"github.com/lleontor705/cortex/internal/projection/obsidian"
	"github.com/lleontor705/cortex/internal/setup"
	cortsync "github.com/lleontor705/cortex/internal/sync"
	"github.com/lleontor705/cortex/internal/tui"
	"github.com/lleontor705/cortex/internal/update"
	"github.com/mark3labs/mcp-go/server"
)

// Version is set by main at startup from the ldflags-injected value.
var Version = "dev"

// Run dispatches Cortex CLI commands.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		printUsage(stdout)
		return 1
	}

	// Launch update check in background (non-blocking).
	updateCh := make(chan *update.Result, 1)
	go func() { updateCh <- update.Check(Version) }()

	// printUpdateNotice drains the channel and prints if newer version found.
	printUpdateNotice := func() {
		select {
		case r := <-updateCh:
			if r != nil {
				writef(stderr, "\nA new version of cortex is available: %s (current: %s)\n%s\n", r.Latest, Version, r.UpdateURL)
			}
		default:
		}
	}

	var exitCode int
	switch args[1] {
	case "help", "--help", "-h":
		printUsage(stdout)
		return 0
	case "version", "--version", "-v":
		writef(stdout, "cortex %s\n", Version)
		printUpdateNotice()
		return 0
	case "mcp":
		return runMCP(args[2:], stdout, stderr)
	case "serve":
		return runServe(args[2:], stdout, stderr)
	case "tui":
		return runTUI(stdout, stderr)
	case "search":
		exitCode = runSearch(args[2:], stdout, stderr)
	case "save":
		exitCode = runSave(args[2:], stdout, stderr)
	case "context":
		exitCode = runContext(args[2:], stdout, stderr)
	case "stats":
		exitCode = runStats(args[2:], stdout, stderr)
	case "timeline":
		exitCode = runTimeline(args[2:], stdout, stderr)
	case "revisions":
		exitCode = runRevisions(args[2:], stdout, stderr)
	case "setup":
		exitCode = runSetup(args[2:], stdout, stderr)
	case "import":
		exitCode = runImport(args[2:], stdout, stderr)
	case "migrate":
		exitCode = runMigrate(args[2:], stdout, stderr)
	case "export":
		exitCode = runExport(args[2:], stdout, stderr)
	case "sync":
		exitCode = runSync(args[2:], stdout, stderr)
	case "merge-projects":
		exitCode = runMergeProjects(args[2:], stdout, stderr)
	case "reindex":
		exitCode = runReindex(args[2:], stdout, stderr)
	case "doctor":
		exitCode = runDoctor(stdout, stderr)
	case "gc":
		exitCode = runGC(args[2:], stdout, stderr)
	case "config":
		exitCode = runConfig(args[2:], stdout, stderr)
	case "auth":
		exitCode = runAuth(args[2:], stdout, stderr)
	default:
		writef(stderr, "unknown command: %s\n\n", args[1])
		printUsage(stderr)
		return 1
	}

	printUpdateNotice()
	return exitCode
}

func printUsage(w io.Writer) {
	writef(w, `cortex -- Persistent memory for AI coding assistants

Usage:
  cortex <command> [arguments]

Commands:
  mcp [--tools=PROFILE]  Start MCP server (stdio)
  search <query>         Search memories
  save <title> <content> Save a memory
  timeline <obs_id>      Show chronological context around an observation
  revisions <obs_id>     Show revision history for an observation
  context [project]      Show recent memory context
  stats                  Show memory statistics
  setup [agent]          Install agent integration
  import --from-json     Import observations from a JSON file
  export [--project P]   Export observations to JSON
  export --to-obsidian --vault PATH  Export a read-only Obsidian projection
  sync                   Sync via file chunks or --remote server replication
  merge-projects         Merge project name variants into one canonical name
  reindex [--project P]  Generate vector embeddings for all observations
  doctor                 Run health checks on the database
  gc [--days N]          Garbage collect archived observations (default: 90 days)
  config <subcommand>    Manage configuration without editing files (get, set, show, validate, init, wizard)
  migrate <up|down|status> Manage database migrations
  tui                    Launch terminal UI
  serve                  Start HTTP REST API server
  version                Print version
  help                   Show this help
`)
}

func openApp() (*app.App, error) {
	return app.Open(context.Background(), app.Options{})
}

func runSearch(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeln(stderr, "usage: cortex search <query> [--type TYPE] [--project PROJECT] [--scope SCOPE] [--limit N]")
		return 1
	}
	var queryParts []string
	opts := domain.SearchOptions{Limit: 10}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--type":
			if i+1 < len(args) {
				opts.Type = args[i+1]
				i++
			}
		case "--project":
			if i+1 < len(args) {
				opts.Project = args[i+1]
				i++
			}
		case "--scope":
			if i+1 < len(args) {
				opts.Scope = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err != nil {
					writef(stderr, "error: invalid --limit value %q\n", args[i+1])
					return 1
				}
				opts.Limit = n
				i++
			}
		default:
			queryParts = append(queryParts, args[i])
		}
	}
	query := strings.Join(queryParts, " ")
	if strings.TrimSpace(query) == "" {
		writeln(stderr, "error: search query is required")
		return 1
	}
	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()
	results, err := a.Stores.Search.Search(context.Background(), query, opts)
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	if len(results) == 0 {
		writef(stdout, "No memories found for: %q\n", query)
		return 0
	}
	writef(stdout, "Found %d memories:\n\n", len(results))
	for i, r := range results {
		project := ""
		if r.Project != "" {
			project = fmt.Sprintf(" | project: %s", r.Project)
		}
		writef(stdout, "[%d] #%d (%s) - %s\n    %s\n    %s%s | scope: %s\n",
			i+1, r.ID, r.Type, r.Title, truncate(r.Content, 300), r.CreatedAt.Format(time.RFC3339), project, r.Scope)
		if explanation := formatSearchBreakdown(r.ScoreBreakdown); explanation != "" {
			writef(stdout, "    explain: %s\n", explanation)
		}
		writeln(stdout, "")
	}
	return 0
}

func runSave(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		writeln(stderr, "usage: cortex save <title> <content> [--type TYPE] [--project PROJECT] [--scope SCOPE] [--topic TOPIC_KEY]")
		return 1
	}
	title, content := args[0], args[1]
	typ, project, scope, topicKey := "manual", "", "project", ""
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--type":
			if i+1 < len(args) {
				typ = args[i+1]
				i++
			}
		case "--project":
			if i+1 < len(args) {
				project = args[i+1]
				i++
			}
		case "--scope":
			if i+1 < len(args) {
				scope = args[i+1]
				i++
			}
		case "--topic":
			if i+1 < len(args) {
				topicKey = args[i+1]
				i++
			}
		}
	}
	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	sessionID := defaultSessionID(project)
	_ = a.Stores.Sessions.Create(ctx, &domain.Session{ID: sessionID, Project: projectOrDefault(project), Directory: currentDir()})
	obs := &domain.Observation{SessionID: sessionID, Title: title, Content: content, Type: typ, Project: project, Scope: scope, TopicKey: topicKey}
	if err := a.Stores.Observations.Save(ctx, obs); err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	writef(stdout, "Memory saved: #%d %q (%s)\n", obs.ID, title, typ)
	return 0
}

func runContext(args []string, stdout, stderr io.Writer) int {
	project, scope := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--scope":
			if i+1 < len(args) {
				scope = args[i+1]
				i++
			}
		default:
			if project == "" {
				project = args[i]
			}
		}
	}
	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	sessions, err := a.Stores.Sessions.List(ctx, project)
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	observations, err := a.Stores.Observations.List(ctx, domain.ObservationFilter{Project: project, Scope: scope, Limit: 20})
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	if len(sessions) == 0 && len(observations) == 0 {
		writeln(stdout, "No previous session memories found.")
		return 0
	}
	if len(sessions) > 0 {
		writeln(stdout, "## Recent Sessions")
		writeln(stdout, "")
		for _, s := range takeSessions(sessions, 5) {
			status := "active"
			if s.EndedAt != nil {
				status = "ended"
			}
			writef(stdout, "- **%s** (%s, %s)\n", s.ID, s.Project, status)
		}
		writeln(stdout, "")
	}
	if len(observations) > 0 {
		writeln(stdout, "## Recent Observations")
		writeln(stdout, "")
		for _, o := range observations {
			writef(stdout, "- #%d [%s] %s\n", o.ID, o.Type, o.Title)
		}
	}
	return 0
}

func runStats(_ []string, stdout, stderr io.Writer) int {
	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	obsStats, err := a.Stores.Observations.Stats(ctx)
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	sessStats, err := a.Stores.Sessions.GetStats(ctx)
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	projects := "none yet"
	if len(sessStats.Projects) > 0 {
		projects = strings.Join(sessStats.Projects, ", ")
	} else if len(obsStats.Projects) > 0 {
		projects = strings.Join(obsStats.Projects, ", ")
	}
	writef(stdout, "Cortex Memory Stats\n  Sessions:     %d\n  Observations: %d\n  Projects:     %s\n", sessStats.TotalSessions, obsStats.TotalObservations, projects)
	return 0
}

func runTimeline(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeln(stderr, "usage: cortex timeline <observation_id> [--before N] [--after N]")
		return 1
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		writef(stderr, "error: invalid observation id %q\n", args[0])
		return 1
	}

	before := 3
	after := 3
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--before" && i+1 < len(args):
			n, convErr := strconv.Atoi(args[i+1])
			if convErr != nil {
				writef(stderr, "error: invalid --before value %q\n", args[i+1])
				return 1
			}
			before = n
			i++
		case args[i] == "--after" && i+1 < len(args):
			n, convErr := strconv.Atoi(args[i+1])
			if convErr != nil {
				writef(stderr, "error: invalid --after value %q\n", args[i+1])
				return 1
			}
			after = n
			i++
		}
	}

	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	ctx := context.Background()
	obs, err := a.Stores.Observations.GetByID(ctx, id)
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}

	// Get observations before (older)
	beforeObs, err := a.Stores.Observations.List(ctx, domain.ObservationFilter{
		CreatedBefore: &obs.CreatedAt,
		Limit:         before,
	})
	if err != nil {
		writef(stderr, "cortex: list before: %v\n", err)
		return 1
	}

	// Get observations after (newer)
	afterObs, err := a.Stores.Observations.List(ctx, domain.ObservationFilter{
		CreatedAfter: &obs.CreatedAt,
		Limit:        after,
		OrderAsc:     true,
	})
	if err != nil {
		writef(stderr, "cortex: list after: %v\n", err)
		return 1
	}

	// Print before (reverse to show oldest first)
	for i := len(beforeObs) - 1; i >= 0; i-- {
		o := beforeObs[i]
		writef(stdout, "  #%d [%s] %s\n", o.ID, o.Type, o.Title)
	}

	// Print target observation highlighted
	writef(stdout, ">>> #%d [%s] %s <<<\n    %s\n", obs.ID, obs.Type, obs.Title, truncate(obs.Content, 500))

	// Print after
	for _, o := range afterObs {
		writef(stdout, "  #%d [%s] %s\n", o.ID, o.Type, o.Title)
	}

	return 0
}

type cliRevisionSnapshot struct {
	Reason   string `json:"reason"`
	Previous struct {
		Title         string `json:"title"`
		Content       string `json:"content"`
		RevisionCount int    `json:"revision_count"`
	} `json:"previous"`
}

func runRevisions(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeln(stderr, "usage: cortex revisions <observation_id> [--limit N]")
		return 1
	}

	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		writef(stderr, "error: invalid observation id %q\n", args[0])
		return 1
	}

	limit := 20
	for i := 1; i < len(args); i++ {
		if args[i] == "--limit" && i+1 < len(args) {
			n, convErr := strconv.Atoi(args[i+1])
			if convErr != nil {
				writef(stderr, "error: invalid --limit value %q\n", args[i+1])
				return 1
			}
			limit = n
			i++
		}
	}

	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	if _, err := a.Stores.Observations.GetByID(context.Background(), id); err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}

	snapshots, err := a.Stores.TemporalSnapshots.GetByRootObservation(context.Background(), id)
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	if len(snapshots) == 0 {
		writef(stdout, "No revision history found for observation #%d\n", id)
		return 0
	}

	if limit <= 0 {
		limit = 20
	}

	writef(stdout, "Revision history for observation #%d\n\n", id)
	count := 0
	for _, snapshot := range snapshots {
		if count >= limit {
			break
		}
		var parsed cliRevisionSnapshot
		if err := json.Unmarshal([]byte(snapshot.Description), &parsed); err != nil {
			continue
		}
		reason := parsed.Reason
		if reason == "" {
			reason = "revision"
		}
		writef(stdout, "[%d] %s [%s] rev=%d %s\n", count+1, snapshot.Timestamp.Format(time.RFC3339), reason, parsed.Previous.RevisionCount, parsed.Previous.Title)
		if parsed.Previous.Content != "" {
			writef(stdout, "    %s\n", truncate(parsed.Previous.Content, 150))
		}
		writeln(stdout, "")
		count++
	}
	if count == 0 {
		writef(stdout, "No revision history found for observation #%d\n", id)
	}
	return 0
}

func runMCP(args []string, stdout, stderr io.Writer) int {
	cfg, err := config.Load("")
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	if cfg.MCP.Remote.Enabled {
		proxy, err := mcp.OpenRemoteProxy(context.Background(), mcp.RemoteProxyConfig{URL: cfg.MCP.Remote.URL, TokenEnv: cfg.MCP.Remote.TokenEnv, Timeout: cfg.MCP.Remote.Timeout})
		if err != nil {
			writef(stderr, "cortex: %v\n", err)
			return 1
		}
		defer func() { _ = proxy.Close() }()
		if err := server.ServeStdio(proxy.Server); err != nil {
			writef(stderr, "cortex: %v\n", err)
			return 1
		}
		_ = stdout
		return 0
	}

	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()
	toolsFilter := ""
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--tools=") {
			toolsFilter = strings.TrimPrefix(args[i], "--tools=")
		} else if args[i] == "--tools" && i+1 < len(args) {
			toolsFilter = args[i+1]
			i++
		}
	}
	var srv *server.MCPServer
	if toolsFilter != "" {
		srv = mcp.NewServerWithTools(a.Stores, mcp.ResolveTools(toolsFilter))
	} else {
		srv = mcp.NewServer(a.Stores)
	}
	if err := server.ServeStdio(srv); err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	_ = stdout
	return 0
}

func runTUI(stdout, stderr io.Writer) int {
	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	deps := &tui.Deps{
		Observations: a.Stores.Observations,
		Sessions:     a.Stores.Sessions,
		Search:       a.Stores.Search,
		Graph:        a.Stores.Graph,
		Scoring:      a.Stores.Scoring,
		Entities:     a.Stores.Entities,
		App:          a,
		Config:       a.Config,
		Version:      Version,
	}

	model := tui.New(deps)
	p := tea.NewProgram(model, tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	_ = stdout
	return 0
}

func runServe(args []string, stdout, stderr io.Writer) int {
	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	addr := fmt.Sprintf("%s:%d", a.Config.HTTP.Host, a.Config.HTTP.Port)
	if !isLoopbackHost(a.Config.HTTP.Host) && strings.TrimSpace(a.Config.HTTP.Token) == "" {
		writef(stderr, "cortex: refusing to expose HTTP API on non-local host %q without http.token configured\n", a.Config.HTTP.Host)
		return 1
	}

	deps := &cortexhttp.Deps{
		Observations:      a.Stores.Observations,
		Sessions:          a.Stores.Sessions,
		Search:            a.Stores.Search,
		Prompts:           a.Stores.Prompts,
		Graph:             a.Stores.Graph,
		Scoring:           a.Stores.Scoring,
		TemporalSnapshots: a.Stores.TemporalSnapshots,
	}

	srv := cortexhttp.NewServer(addr, deps, cortexhttp.Options{
		AuthToken:      a.Config.HTTP.Token,
		AllowedOrigins: a.Config.HTTP.AllowedOrigins,
	})
	writef(stdout, "Cortex HTTP server listening on %s\n", addr)

	if err := srv.ListenAndServe(); err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	return 0
}

func runSetup(args []string, stdout, stderr io.Writer) int {
	agent := ""
	if len(args) > 0 {
		agent = args[0]
	}
	if agent == "" {
		writeln(stdout, "Supported agents: opencode, claude-code, gemini-cli, codex")
		return 0
	}
	result, err := setup.Install(agent)
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	writef(stdout, "Installed Cortex integration for %s (%d files)\n  -> %s\n", result.Agent, result.Files, result.Destination)
	return 0
}

func runImport(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeln(stderr, "usage: cortex import --from-json --path FILE")
		return 1
	}

	switch args[0] {
	case "--from-json":
		return importFromJSON(args[1:], stdout, stderr)
	default:
		writef(stderr, "unknown import source: %s (use --from-json)\n", args[0])
		return 1
	}
}

func importFromJSON(args []string, stdout, stderr io.Writer) int {
	path := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--path" && i+1 < len(args) {
			path = args[i+1]
			i++
		}
	}
	if path == "" {
		writeln(stderr, "cortex: --path is required for --from-json")
		return 1
	}

	f, err := os.Open(path)
	if err != nil {
		writef(stderr, "cortex: failed to open file: %v\n", err)
		return 1
	}
	defer func() { _ = f.Close() }()

	// Limit to 50 MB to prevent excessive memory usage
	const maxImportSize = 50 << 20
	var observations []*domain.Observation
	if err := json.NewDecoder(io.LimitReader(f, maxImportSize)).Decode(&observations); err != nil {
		writef(stderr, "cortex: invalid JSON: %v\n", err)
		return 1
	}

	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	ctx := context.Background()
	saved := 0
	for _, obs := range observations {
		// Ensure session exists
		if obs.SessionID != "" {
			_ = a.Stores.Sessions.Create(ctx, &domain.Session{
				ID: obs.SessionID, Project: obs.Project, Directory: ".",
			})
		}
		if err := a.Stores.Observations.Save(ctx, obs); err != nil {
			writef(stderr, "warning: skipped %q: %v\n", obs.Title, err)
			continue
		}
		saved++
	}

	writef(stdout, "Imported %d of %d observations from JSON\n", saved, len(observations))
	return 0
}

func runMigrate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeln(stderr, "usage: cortex migrate <up|down|status> [--target VERSION]")
		return 1
	}

	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	ctx := context.Background()

	switch args[0] {
	case "up":
		if err := a.Migrator.Up(ctx); err != nil {
			writef(stderr, "cortex: migration up failed: %v\n", err)
			return 1
		}
		writeln(stdout, "Migrations applied successfully")
		return 0

	case "down":
		target := 0
		for i := 1; i < len(args); i++ {
			if args[i] == "--target" && i+1 < len(args) {
				target, err = strconv.Atoi(args[i+1])
				if err != nil {
					writef(stderr, "cortex: invalid target version: %s\n", args[i+1])
					return 1
				}
				i++
			}
		}
		if err := a.Migrator.Down(ctx, target); err != nil {
			writef(stderr, "cortex: migration down failed: %v\n", err)
			return 1
		}
		writef(stdout, "Migrations rolled back to version %d\n", target)
		return 0

	case "status":
		statuses, err := a.Migrator.Status(ctx)
		if err != nil {
			writef(stderr, "cortex: %v\n", err)
			return 1
		}
		writeln(stdout, "Migration Status:")
		for _, s := range statuses {
			applied := "pending"
			if s.Applied {
				applied = fmt.Sprintf("applied at %s", s.AppliedAt)
			}
			writef(stdout, "  %03d %-20s %s\n", s.Version, s.Name, applied)
		}
		return 0

	default:
		writef(stderr, "unknown migrate subcommand: %s (use up, down, or status)\n", args[0])
		return 1
	}
}

func runExport(args []string, stdout, stderr io.Writer) int {
	for _, arg := range args {
		if arg == "--to-obsidian" {
			return runObsidianExport(args, stdout, stderr)
		}
	}
	project := ""
	output := ""

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--project" && i+1 < len(args):
			project = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--project="):
			project = strings.TrimPrefix(args[i], "--project=")
		case args[i] == "--output" && i+1 < len(args):
			output = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--output="):
			output = strings.TrimPrefix(args[i], "--output=")
		}
	}

	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	filter := domain.ObservationFilter{
		Project: project,
		Limit:   10000,
	}

	observations, err := a.Stores.Observations.List(context.Background(), filter)
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}

	data, err := json.MarshalIndent(observations, "", "  ")
	if err != nil {
		writef(stderr, "cortex: failed to marshal JSON: %v\n", err)
		return 1
	}

	if output != "" {
		if err := os.WriteFile(output, data, 0600); err != nil {
			writef(stderr, "cortex: failed to write file: %v\n", err)
			return 1
		}
		writef(stdout, "Exported %d observations to %s\n", len(observations), output)
	} else {
		writeln(stdout, string(data))
	}

	return 0
}

func runObsidianExport(args []string, stdout, stderr io.Writer) int {
	vault, project := "", ""
	includePersonal := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--vault":
			if i+1 < len(args) {
				vault = args[i+1]
				i++
			}
		case "--project":
			if i+1 < len(args) {
				project = args[i+1]
				i++
			}
		case "--include-personal":
			includePersonal = true
		}
	}
	if vault == "" {
		writeln(stderr, "usage: cortex export --to-obsidian --vault PATH [--project PROJECT] [--include-personal]")
		return 1
	}
	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()
	r, err := obsidian.NewWriter(a.Stores.Observations, a.Stores.Graph, a.Stores.Entities, a.Stores.Scoring).Export(context.Background(), obsidian.Options{Vault: vault, Project: project, IncludePersonal: includePersonal})
	if err != nil {
		writef(stderr, "cortex: obsidian export: %v\n", err)
		return 1
	}
	for _, warning := range r.Warnings {
		writef(stderr, "warning: %s\n", warning)
	}
	writef(stdout, "Exported %d observations to Obsidian (%d skipped, %d stale)\n", r.Written, r.Skipped, r.Stale)
	return 0
}

func runSync(args []string, stdout, stderr io.Writer) int {
	var doImport, doStatus, syncAll, remoteSync bool
	var project string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--import":
			doImport = true
		case "--status":
			doStatus = true
		case "--all":
			syncAll = true
		case "--remote":
			remoteSync = true
		case "--project":
			if i+1 < len(args) {
				project = args[i+1]
				i++
			}
		}
	}

	a, err := app.Open(context.Background(), app.Options{DisableRemoteSync: true})
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	syncDir := filepath.Dir(a.Config.Database.Path)
	transport := cortsync.NewFileTransport(syncDir)
	syncer := cortsync.NewSyncer(a.Stores.Observations, transport)
	ctx := context.Background()
	if remoteSync {
		if strings.TrimSpace(a.Config.Sync.URL) == "" {
			writef(stderr, "sync remote: sync.url is not configured\n")
			return 1
		}
		token := os.Getenv(a.Config.Sync.TokenEnv)
		remote, err := cortsync.NewRemoteSyncer(a.DB.DB(), a.Config.Sync.URL, token, a.Config.Sync.Timeout)
		if err != nil {
			writef(stderr, "sync remote: %v\n", err)
			return 1
		}
		result, err := remote.Sync(ctx)
		if err != nil {
			writef(stderr, "sync remote: %v\n", err)
			return 1
		}
		writef(stdout, "Remote sync complete: %d pushed, %d pulled, cursor %d\n", result.Pushed, result.Pulled, result.Cursor)
		return 0
	}

	if doStatus {
		local, remote, pending, err := syncer.Status(ctx)
		if err != nil {
			writef(stderr, "sync status: %v\n", err)
			return 1
		}
		writef(stdout, "Local chunks:  %d\nRemote chunks: %d\nPending import: %d\n", local, remote, pending)
		return 0
	}

	if doImport {
		result, err := syncer.Import(ctx)
		if err != nil {
			writef(stderr, "sync import: %v\n", err)
			return 1
		}
		writef(stdout, "Imported %d chunks (%d skipped): %d sessions, %d observations, %d prompts\n",
			result.ChunksImported, result.ChunksSkipped,
			result.SessionsImported, result.ObservationsImported, result.PromptsImported)
		return 0
	}

	// Default: export
	if !syncAll && project == "" {
		project = projectpkg.DetectProject(currentDir())
	}

	username := cortsync.GetUsername()
	result, err := syncer.Export(ctx, username, project)
	if err != nil {
		writef(stderr, "sync export: %v\n", err)
		return 1
	}
	if result.IsEmpty {
		writeln(stdout, "Nothing new to sync.")
		return 0
	}
	writef(stdout, "Exported chunk %s: %d sessions, %d observations, %d prompts\n",
		result.ChunkID, result.SessionsExported, result.ObservationsExported, result.PromptsExported)
	writeln(stdout, "To share: git add ~/.cortex/ && git commit -m 'sync cortex memories'")
	return 0
}

func runMergeProjects(args []string, stdout, stderr io.Writer) int {
	var from, to string
	var dryRun bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from":
			if i+1 < len(args) {
				from = args[i+1]
				i++
			}
		case "--to":
			if i+1 < len(args) {
				to = args[i+1]
				i++
			}
		case "--dry-run":
			dryRun = true
		}
	}

	if from == "" || to == "" {
		writeln(stderr, "usage: cortex merge-projects --from 'Name1,Name2' --to canonical-name [--dry-run]")
		return 1
	}

	var sources []string
	for _, s := range strings.Split(from, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			sources = append(sources, s)
		}
	}

	if dryRun {
		writef(stdout, "Dry run: would merge %v -> %q\n", sources, to)
		return 0
	}

	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	result, err := a.Stores.Observations.MergeProjects(context.Background(), sources, to)
	if err != nil {
		writef(stderr, "merge-projects: %v\n", err)
		return 1
	}

	writef(stdout, "Merged into %q: %d observations, %d sessions updated.\n",
		result.Canonical, result.ObservationsUpdated, result.SessionsUpdated)
	if len(result.SourcesMerged) > 0 {
		writef(stdout, "Sources merged: %v\n", result.SourcesMerged)
	}
	return 0
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

func formatSearchBreakdown(b domain.SearchScoreBreakdown) string {
	parts := make([]string, 0, 4)
	if b.Strategy != "" {
		parts = append(parts, "strategy="+b.Strategy)
	}
	if b.TopicKeyExact {
		parts = append(parts, "topic_key_exact=true")
	}
	if b.KeywordBM25 != 0 {
		parts = append(parts, fmt.Sprintf("bm25=%.4f", b.KeywordBM25))
	}
	if b.FusionScore != 0 {
		parts = append(parts, fmt.Sprintf("fusion=%.4f", b.FusionScore))
	}
	return strings.Join(parts, " | ")
}

func defaultSessionID(project string) string {
	if project == "" {
		return "manual-save"
	}
	return "manual-save-" + project
}

func projectOrDefault(project string) string {
	if project == "" {
		return "default"
	}
	return project
}

func currentDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func takeSessions(sessions []*domain.Session, n int) []*domain.Session {
	if len(sessions) <= n {
		return sessions
	}
	return sessions[:n]
}

// writef writes formatted output, discarding any write error (unactionable for CLI output).
func writef(w io.Writer, format string, args ...interface{}) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// writeln writes a line, discarding any write error.
func writeln(w io.Writer, s string) {
	_, _ = fmt.Fprintln(w, s)
}

// --- reindex ----------------------------------------------------------------

func runReindex(args []string, stdout, stderr io.Writer) int {
	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	if a.Stores.Embeddings == nil {
		writef(stderr, "cortex: no embedding provider configured.\nSet search.embedding_provider in cortex.yaml (e.g., 'ollama' or 'openai').\n")
		return 1
	}

	if a.Stores.Vectors == nil || !domain.IsVectorIndexHealthy(context.Background(), a.Stores.Vectors) {
		writef(stderr, "cortex: vector store not available.\nBuild with: go build -tags cortex_vectors ./cmd/cortex\n")
		return 1
	}

	project := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--project" && i+1 < len(args) {
			project = args[i+1]
			i++
		}
	}

	ctx := context.Background()
	filter := domain.ObservationFilter{Limit: 5000}
	if project != "" {
		filter.Project = project
	}

	obs, err := a.Stores.Observations.List(ctx, filter)
	if err != nil {
		writef(stderr, "cortex: list observations: %v\n", err)
		return 1
	}

	writef(stdout, "Reindexing %d observations with %s...\n", len(obs), a.Stores.Embeddings.Model())
	if len(obs) == 0 {
		writeln(stdout, "Done. Indexed: 0, Skipped: 0, Errors: 0")
		return 0
	}

	// Manage the local Ollama process and model only when auto-start is enabled.
	if a.Config.Search.EmbeddingProvider == "ollama" && a.Config.Search.OllamaAutoStart {
		mgr := ollama.NewManager(a.Config.Search.EmbeddingBaseURL)
		if !mgr.IsRunning(ctx) {
			writeln(stdout, "Starting Ollama...")
			if err := mgr.EnsureRunning(ctx); err != nil {
				writef(stderr, "cortex: failed to start ollama: %v\n", err)
				return 1
			}
			writeln(stdout, "Ollama is ready.")
		}
		model := a.Config.Search.EmbeddingModel
		if model == "" {
			model = "nomic-embed-text"
		}
		has, _ := mgr.HasModel(ctx, model)
		if !has {
			writef(stdout, "Pulling model %s...\n", model)
			if err := mgr.PullModel(ctx, model, func(p string) {
				writef(stdout, "  %s\n", p)
			}); err != nil {
				writef(stderr, "cortex: failed to pull model: %v\n", err)
				return 1
			}
			writef(stdout, "Model %s pulled successfully.\n", model)
		}
	}

	indexed, skipped, errors := 0, 0, 0
	for i, o := range obs {
		text := o.Title + "\n" + o.Content
		vec, embErr := a.Stores.Embeddings.Embed(ctx, text)
		if embErr != nil {
			errors++
			continue
		}
		if storeErr := a.Stores.Vectors.Upsert(ctx, []domain.VectorPoint{{
			ID:     o.ID,
			Vector: vec,
			ModelInfo: domain.ModelInfo{
				Name:      a.Stores.Embeddings.Model(),
				Dimension: a.Stores.Embeddings.Dimensions(),
			},
		}}); storeErr != nil {
			errors++
			continue
		}
		indexed++

		if (i+1)%50 == 0 {
			writef(stdout, "  %d/%d indexed...\n", i+1, len(obs))
		}
	}

	writef(stdout, "Done. Indexed: %d, Skipped: %d, Errors: %d\n", indexed, skipped, errors)
	return 0
}

// --- doctor -----------------------------------------------------------------

func runDoctor(stdout, stderr io.Writer) int {
	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	ctx := context.Background()
	issues := 0

	writeln(stdout, "Cortex Doctor — Health Check\n")

	// 1. Database
	stats, err := a.Stores.Observations.Stats(ctx)
	if err != nil {
		writef(stdout, "  [FAIL] Database: %v\n", err)
		issues++
	} else {
		writef(stdout, "  [OK]   Database: %d observations, %d projects\n", stats.TotalObservations, len(stats.Projects))
	}

	// 2. FTS5 sync
	results, searchErr := a.Stores.Search.Search(ctx, "test", domain.SearchOptions{Limit: 1})
	if searchErr != nil {
		writef(stdout, "  [FAIL] FTS5 search: %v\n", searchErr)
		issues++
	} else {
		writef(stdout, "  [OK]   FTS5 search: operational (%d results for test query)\n", len(results))
	}

	// 3. Graph
	edgeCount, graphErr := a.Stores.Graph.CountAllEdges(ctx)
	if graphErr != nil {
		writef(stdout, "  [FAIL] Knowledge graph: %v\n", graphErr)
		issues++
	} else {
		writef(stdout, "  [OK]   Knowledge graph: %d edges\n", edgeCount)
	}

	// 4. Vector store
	if domain.IsVectorIndexHealthy(context.Background(), a.Stores.Vectors) {
		writef(stdout, "  [OK]   Vector store: enabled\n")
	} else {
		writef(stdout, "  [WARN] Vector store: disabled (build with -tags cortex_vectors)\n")
	}

	// 5. Embedding service
	if a.Stores.Embeddings != nil {
		writef(stdout, "  [OK]   Embeddings: %s (%d dims)\n", a.Stores.Embeddings.Model(), a.Stores.Embeddings.Dimensions())
	} else {
		writef(stdout, "  [WARN] Embeddings: not configured (set search.embedding_provider)\n")
	}

	// 6. Orphan count
	if stats != nil && len(stats.Projects) > 0 {
		orphans, orphErr := a.Stores.Observations.OrphanObservations(ctx, stats.Projects[0], 1000)
		if orphErr == nil {
			pct := 0.0
			if stats.TotalObservations > 0 {
				pct = float64(len(orphans)) / float64(stats.TotalObservations) * 100
			}
			if pct > 50 {
				writef(stdout, "  [WARN] Orphans: %d (%.0f%%) — use cortex_relate to connect observations\n", len(orphans), pct)
			} else {
				writef(stdout, "  [OK]   Orphans: %d (%.0f%%)\n", len(orphans), pct)
			}
		}
	}

	writeln(stdout, "")
	if issues > 0 {
		writef(stdout, "%d issue(s) found.\n", issues)
		return 1
	}
	writeln(stdout, "All checks passed.")
	return 0
}

// --- gc (garbage collect) ---------------------------------------------------

func runGC(args []string, stdout, stderr io.Writer) int {
	a, err := openApp()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	days := 90
	for i := 0; i < len(args); i++ {
		if args[i] == "--days" && i+1 < len(args) {
			if d, dErr := strconv.Atoi(args[i+1]); dErr == nil && d > 0 {
				days = d
			}
			i++
		}
	}

	ctx := context.Background()
	cutoff := time.Now().AddDate(0, 0, -days)

	// Find soft-deleted observations older than cutoff
	archived, err := a.Stores.Observations.ListArchivable(ctx, cutoff, 0, 1000)
	if err != nil {
		writef(stderr, "cortex: list archivable: %v\n", err)
		return 1
	}

	if len(archived) == 0 {
		writeln(stdout, "Nothing to collect.")
		return 0
	}

	deleted := 0
	for _, obs := range archived {
		if hardErr := a.Stores.Observations.HardDelete(ctx, obs.ID); hardErr == nil {
			deleted++
		}
	}

	writef(stdout, "Garbage collected %d observations older than %d days.\n", deleted, days)
	return 0
}

func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writef(stderr, "Usage: cortex config <show|validate|path|init> [options]\n")
		return 1
	}

	switch args[0] {
	case "path":
		cfg, err := config.Load("")
		if err != nil {
			writef(stderr, "error: %v\n", err)
			return 1
		}
		if cfg.LoadedFrom != "" {
			writef(stdout, "%s\n", cfg.LoadedFrom)
		} else {
			writef(stdout, "%s (default in-memory)\n", filepath.Join(config.CortexDir(), "cortex.yaml"))
		}
		return 0

	case "validate":
		configPath := ""
		for i := 1; i < len(args); i++ {
			if (args[i] == "--file" || args[i] == "-f" || args[i] == "--config") && i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else if strings.HasPrefix(args[i], "--file=") {
				configPath = strings.TrimPrefix(args[i], "--file=")
			} else if strings.HasPrefix(args[i], "--config=") {
				configPath = strings.TrimPrefix(args[i], "--config=")
			}
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			writef(stderr, "Configuration invalid: %v\n", err)
			return 1
		}
		writef(stdout, "Configuration valid (source: %s)\n", cfg.LoadedFrom)
		return 0

	case "show", "print":
		format := "yaml"
		configPath := ""
		for i := 1; i < len(args); i++ {
			if args[i] == "--format" && i+1 < len(args) {
				format = strings.ToLower(args[i+1])
				i++
			} else if strings.HasPrefix(args[i], "--format=") {
				format = strings.ToLower(strings.TrimPrefix(args[i], "--format="))
			} else if (args[i] == "--file" || args[i] == "-f" || args[i] == "--config") && i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else if strings.HasPrefix(args[i], "--file=") {
				configPath = strings.TrimPrefix(args[i], "--file=")
			} else if strings.HasPrefix(args[i], "--config=") {
				configPath = strings.TrimPrefix(args[i], "--config=")
			}
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			writef(stderr, "error loading config: %v\n", err)
			return 1
		}
		// Redact secrets for safe output
		cfgCopy := *cfg
		cfgCopy.HTTP.Token = maskSecret(cfgCopy.HTTP.Token)
		cfgCopy.Vector.Qdrant.APIKey = maskSecret(cfgCopy.Vector.Qdrant.APIKey)
		cfgCopy.Server.Secrets.SigningKey = maskSecret(cfgCopy.Server.Secrets.SigningKey)
		cfgCopy.Server.Secrets.OIDCClientSecret = maskSecret(cfgCopy.Server.Secrets.OIDCClientSecret)

		if format == "json" {
			data, err := json.MarshalIndent(cfgCopy, "", "  ")
			if err != nil {
				writef(stderr, "error formatting json: %v\n", err)
				return 1
			}
			writef(stdout, "%s\n", string(data))
		} else {
			writef(stdout, "# Active config (source: %s)\n", cfg.LoadedFrom)
			writef(stdout, "%s\n", cfg.String())
		}
		return 0

	case "init":
		format := "yaml"
		force := false
		targetPath := ""
		for i := 1; i < len(args); i++ {
			if args[i] == "--format" && i+1 < len(args) {
				format = strings.ToLower(args[i+1])
				i++
			} else if strings.HasPrefix(args[i], "--format=") {
				format = strings.ToLower(strings.TrimPrefix(args[i], "--format="))
			} else if args[i] == "--force" || args[i] == "-f" {
				force = true
			} else if (args[i] == "--file" || args[i] == "-o" || args[i] == "--output") && i+1 < len(args) {
				targetPath = args[i+1]
				i++
			} else if strings.HasPrefix(args[i], "--file=") {
				targetPath = strings.TrimPrefix(args[i], "--file=")
			} else if strings.HasPrefix(args[i], "--output=") {
				targetPath = strings.TrimPrefix(args[i], "--output=")
			}
		}

		outPath, err := config.InitConfig(targetPath, format, force)
		if err != nil {
			writef(stderr, "error: %v\n", err)
			return 1
		}
		writef(stdout, "Initialized minimal %s configuration at: %s\n", strings.ToUpper(format), outPath)
		return 0

	case "get":
		if len(args) < 2 {
			writef(stderr, "Usage: cortex config get <key> [--file=path]\nExample: cortex config get http.port\n")
			return 1
		}
		key := args[1]
		configPath := ""
		for i := 2; i < len(args); i++ {
			if (args[i] == "--file" || args[i] == "-f" || args[i] == "--config") && i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else if strings.HasPrefix(args[i], "--file=") {
				configPath = strings.TrimPrefix(args[i], "--file=")
			} else if strings.HasPrefix(args[i], "--config=") {
				configPath = strings.TrimPrefix(args[i], "--config=")
			}
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			writef(stderr, "error loading config: %v\n", err)
			return 1
		}
		val, err := cfg.GetProperty(key)
		if err != nil {
			writef(stderr, "error: %v\n", err)
			return 1
		}
		writef(stdout, "%s\n", val)
		return 0

	case "set":
		if len(args) < 3 {
			writef(stderr, "Usage: cortex config set <key> <value> [--file=path] [--format=yaml|json|toml]\nExample: cortex config set http.port 8080\n")
			return 1
		}
		key := args[1]
		val := args[2]
		configPath := ""
		format := ""
		for i := 3; i < len(args); i++ {
			if (args[i] == "--file" || args[i] == "-f" || args[i] == "--config") && i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else if strings.HasPrefix(args[i], "--file=") {
				configPath = strings.TrimPrefix(args[i], "--file=")
			} else if strings.HasPrefix(args[i], "--config=") {
				configPath = strings.TrimPrefix(args[i], "--config=")
			} else if (args[i] == "--format") && i+1 < len(args) {
				format = strings.ToLower(args[i+1])
				i++
			} else if strings.HasPrefix(args[i], "--format=") {
				format = strings.ToLower(strings.TrimPrefix(args[i], "--format="))
			}
		}

		cfg, err := config.Load(configPath)
		if err != nil {
			writef(stderr, "error loading config: %v\n", err)
			return 1
		}
		if err := cfg.SetProperty(key, val); err != nil {
			writef(stderr, "error setting %s: %v\n", key, err)
			return 1
		}

		savePath := cfg.LoadedFrom
		if savePath == "" {
			ext := "yaml"
			if format != "" {
				ext = format
			}
			savePath = filepath.Join(config.CortexDir(), "cortex."+ext)
		} else if format != "" {
			// Change format extension if explicitly requested
			dir := filepath.Dir(savePath)
			savePath = filepath.Join(dir, "cortex."+format)
		}

		if err := config.Save(cfg, savePath); err != nil {
			writef(stderr, "error saving config: %v\n", err)
			return 1
		}
		writef(stdout, "✔ Successfully set %s = %s\n  Saved to: %s\n", key, val, savePath)
		return 0

	case "wizard", "interactive":
		return runConfigWizard(stdout, stderr)

	default:
		writef(stderr, "unknown config subcommand: %s (valid: get, set, show, validate, path, init, wizard)\n", args[0])
		return 1
	}
}

func runConfigWizard(stdout, stderr io.Writer) int {
	cfg, err := config.Load("")
	if err != nil {
		writef(stderr, "error loading existing config: %v\n", err)
		return 1
	}

	writef(stdout, "\n=== 🧠 Cortex Configuration Wizard ===\n\n")
	writef(stdout, "Current active config: %s\n", cfg.LoadedFrom)
	writef(stdout, "Storage: %s\n", cfg.Database.Path)
	writef(stdout, "HTTP Port: %d (enabled: %t)\n", cfg.HTTP.Port, cfg.HTTP.Enabled)
	writef(stdout, "Embedding Provider: %s\n", cfg.Search.EmbeddingProvider)
	writef(stdout, "\nTip: Use 'cortex config set <key> <value>' for instant CLI updates, or 'cortex tui' for full visual navigation.\n\n")
	return 0
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 6 {
		return "******"
	}
	return s[:3] + "..." + s[len(s)-3:]
}

func runAuth(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writef(stderr, "Usage: cortex auth <login|status|logout> [options]\n")
		return 1
	}
	switch args[0] {
	case "login":
		token := ""
		for i := 1; i < len(args); i++ {
			if (args[i] == "--token" || args[i] == "-t") && i+1 < len(args) {
				token = args[i+1]
				i++
			} else if strings.HasPrefix(args[i], "--token=") {
				token = strings.TrimPrefix(args[i], "--token=")
			}
		}
		if token == "" {
			writef(stderr, "error: token required (use cortex auth login --token=<token>)\n")
			return 1
		}
		cfg, err := config.Load("")
		if err != nil {
			writef(stderr, "error: %v\n", err)
			return 1
		}
		cfg.HTTP.Token = token
		if err := config.Save(cfg, cfg.LoadedFrom); err != nil {
			writef(stderr, "error saving token: %v\n", err)
			return 1
		}
		writef(stdout, "✔ Successfully authenticated. Token saved to %s\n", cfg.LoadedFrom)
		return 0

	case "status", "whoami":
		cfg, err := config.Load("")
		if err != nil {
			writef(stderr, "error: %v\n", err)
			return 1
		}
		writef(stdout, "\n=== 👤 Cortex Authentication Status ===\n")
		if cfg.HTTP.Token != "" {
			writef(stdout, "Token: %s\n", maskSecret(cfg.HTTP.Token))
			subject := cfg.Server.PrincipalSubject
			if subject == "" {
				subject = os.Getenv("USER")
				if subject == "" {
					subject = os.Getenv("USERNAME")
				}
				if subject == "" {
					subject = "local-user"
				}
			}
			writef(stdout, "Principal: %s\n", subject)
			if len(cfg.Server.Roles) > 0 {
				writef(stdout, "Role: %s\n", strings.Join(cfg.Server.Roles, ", "))
			} else {
				writef(stdout, "Role: admin\n")
			}
			writef(stdout, "Status: Authenticated\n\n")
		} else {
			writef(stdout, "Status: Anonymous / Unauthenticated (local zero-auth mode)\n\n")
		}
		return 0

	case "logout":
		cfg, err := config.Load("")
		if err != nil {
			writef(stderr, "error: %v\n", err)
			return 1
		}
		cfg.HTTP.Token = ""
		_ = config.Save(cfg, cfg.LoadedFrom)
		writef(stdout, "✔ Successfully logged out.\n")
		return 0

	default:
		writef(stderr, "unknown auth subcommand: %s (valid: login, status, logout)\n", args[0])
		return 1
	}
}
