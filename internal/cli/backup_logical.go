package cli

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/app"
	"github.com/lleontor705/cortex/v2/internal/backup"
)

// --- backup --out (portable logical snapshot) --------------------------------
//
// `cortex backup --out <file.tar.gz> [--tenant <id>]` produces the portable
// logical snapshot (JSONL parts in a tar.gz, sha256-verified manifest).
// `cortex backup <path>` (positional destination) keeps its existing
// physical-SQLite VACUUM INTO semantics — the two coexist because they answer
// different needs (fast same-engine snapshot vs portable logical layer).
//
// Scope note (documented in the PR): logical backup/restore targets the local
// SQLite v2 store. Server (PostgreSQL) full-database backup remains the
// domain of pg_dump; restore refuses databases without a cortex-v2 schema
// identity, which includes server-composed PostgreSQL databases.

// collectBackupSecrets assembles the fail-closed secret guard list: known
// credential environment variables plus the configured HTTP token. Values are
// never logged; they are only used for containment scanning.
func collectBackupSecrets(httpToken string) []backup.Secret {
	var extra []backup.Secret
	if strings.TrimSpace(httpToken) != "" {
		extra = append(extra, backup.Secret{Name: "http.token", Value: httpToken})
	}
	return backup.CollectEnvSecrets(extra...)
}

// runBackupLogical implements `cortex backup --out <file.tar.gz>`.
func runBackupLogical(args []string, a *app.App, stdout, stderr io.Writer) int {
	out, tenant := "", ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--out" && i+1 < len(args):
			out = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--out="):
			out = strings.TrimPrefix(args[i], "--out=")
		case args[i] == "--tenant" && i+1 < len(args):
			tenant = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--tenant="):
			tenant = strings.TrimPrefix(args[i], "--tenant=")
		}
	}
	if out == "" {
		writeln(stderr, "usage: cortex backup --out <file.tar.gz> [--tenant ID]   (portable logical snapshot)")
		writeln(stderr, "       cortex backup <path>                              (physical SQLite snapshot)")
		return 1
	}

	res, err := (&backup.Exporter{DB: a.DB.DB()}).Export(context.Background(), backup.ExportOptions{
		Out:         out,
		Tenant:      tenant,
		ToolVersion: Version,
		Secrets:     collectBackupSecrets(a.Config.HTTP.Token),
	})
	if err != nil {
		writef(stderr, "cortex: backup: %v\n", err)
		return 1
	}
	writef(stdout, "Portable backup created: %s (%.2f MB)\n", res.Path, float64(res.SizeBytes)/(1024*1024))
	writef(stdout, "  Schema:        cortex-v2 %s\n", res.SchemaVer)
	writef(stdout, "  Observations:  %d\n", res.Observations)
	writef(stdout, "  Edges:         %d\n", res.Edges)
	writef(stdout, "  Sessions:      %d\n", res.Sessions)
	writeln(stdout, "Embedding vectors are not included; run 'cortex reindex' after a restore.")
	return 0
}

// runRestore implements `cortex restore --from <file.tar.gz>`.
func runRestore(args []string, a *app.App, stdout, stderr io.Writer) int {
	from := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--from" && i+1 < len(args):
			from = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--from="):
			from = strings.TrimPrefix(args[i], "--from=")
		}
	}
	if from == "" {
		writeln(stderr, "usage: cortex restore --from <file.tar.gz>")
		return 1
	}
	if _, err := os.Stat(from); err != nil {
		writef(stderr, "cortex: restore: archive not readable: %v\n", err)
		return 1
	}

	res, err := (&backup.Restorer{DB: a.DB.DB()}).Restore(context.Background(), backup.RestoreOptions{
		From:    from,
		Secrets: collectBackupSecrets(a.Config.HTTP.Token),
	})
	if err != nil {
		writef(stderr, "cortex: restore: %v\n", err)
		return 1
	}
	writef(stdout, "Restore complete (schema %s):\n", res.SchemaVer)
	writef(stdout, "  Sessions:      %d restored, %d skipped (existing)\n", res.SessionsRestored, res.SessionsSkipped)
	writef(stdout, "  Observations:  %d restored, %d skipped (existing)\n", res.ObservationsRestored, res.ObservationsSkipped)
	writef(stdout, "  Edges:         %d restored, %d skipped (existing)\n", res.EdgesRestored, res.EdgesSkipped)
	writeln(stdout, "Embedding vectors were not restored; run 'cortex reindex' (or wait for the embedding worker) to regenerate them.")
	return 0
}
