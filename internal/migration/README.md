# Migration Framework

Database migration framework used by tests and the legacy SQLite migrator. Local
startup uses the separate embedded forward-only v2 baseline in
`migrations/v2/001_init.sql`; the root `migrations/001-014` files do not drive
startup. PostgreSQL uses `migrations/v2/100_server.sql` and its own ledger.

## Features

- ✅ **Version Tracking**: Automatically tracks applied migrations in `_migrations` table
- ✅ **Transaction Safety**: Each migration runs in a transaction for atomicity
- ✅ **Rollback Support**: Available to registered/legacy migrations; the v2 baseline is forward-only
- ✅ **Status Reporting**: Query which migrations are applied/pending
- ✅ **Flexible Loading**: Load migrations from disk or register programmatically
- ✅ **Concurrent Safe**: Thread-safe operations with proper locking

## Installation

```go
import "github.com/lleontor705/cortex/internal/migration"
```

## Quick Start

### 1. Load Migrations from Disk

```go
package main

import (
    "context"
    "database/sql"
    "log"

    "github.com/lleontor705/cortex/internal/migration"
    _ "modernc.org/sqlite"
)

func main() {
    // Open database
    db, err := sql.Open("sqlite", "cortex.db")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    // Create migrator pointing to migrations directory
    migrator, err := migration.NewMigrator(db, "./migrations")
    if err != nil {
        log.Fatal(err)
    }

    // Apply all pending migrations
    ctx := context.Background()
    if err := migrator.Up(ctx); err != nil {
        log.Fatal(err)
    }

    log.Printf("Migrations applied. Current version: %d", migrator.Version())
}
```

### 2. Programmatic Migrations

```go
// Create migrator (no directory needed)
migrator, err := migration.NewMigrator(db, "")
if err != nil {
    log.Fatal(err)
}

// Register migrations programmatically
migrator.Register(migration.Migration{
    Version:     1,
    Name:        "create_users",
    Description: "Create users table",
    UpSQL: `CREATE TABLE users (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        username TEXT NOT NULL UNIQUE,
        email TEXT NOT NULL
    );`,
    DownSQL: "DROP TABLE IF EXISTS users;",
})

// Apply migrations
ctx := context.Background()
if err := migrator.Up(ctx); err != nil {
    log.Fatal(err)
}
```

### 3. Check Migration Status

```go
statuses, err := migrator.Status(ctx)
if err != nil {
    log.Fatal(err)
}

for _, status := range statuses {
    if status.Applied {
        fmt.Printf("✓ Version %d: %s (applied at %s)\n",
            status.Version, status.Name, status.AppliedAt)
    } else {
        fmt.Printf("⏳ Version %d: %s (pending)\n",
            status.Version, status.Name)
    }
}
```

### 4. Rollback Migrations

```go
// Rollback to version 1 (removes versions 2+)
if err := migrator.Down(ctx, 1); err != nil {
    log.Fatal(err)
}

// Rollback all migrations
if err := migrator.Down(ctx, 0); err != nil {
    log.Fatal(err)
}
```

## Migration File Format

Migration files are SQL files with a specific format:

```sql
-- +migrate Up
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL,
    email TEXT NOT NULL
);

CREATE INDEX idx_users_email ON users(email);

-- +migrate Down
DROP INDEX IF EXISTS idx_users_email;
DROP TABLE IF EXISTS users;
```

### File Naming Convention

Files must follow the pattern: `NNN_description.sql`

- `NNN`: Version number (zero-padded, e.g., `001`, `002`, `010`)
- `description`: Brief description using underscores
- `.sql`: File extension

Examples:
- `001_init.sql` - Initial schema
- `002_add_fts.sql` - Add full-text search
- `003_add_sync.sql` - Add sync support

### File Structure

1. **Up Section** (required): SQL to apply migration
   - Starts with `-- +migrate Up`
   - Contains CREATE TABLE, ALTER TABLE, etc.

2. **Down Section** (optional): SQL to rollback migration
   - Starts with `-- +migrate Down`
   - Should reverse the Up section
   - Required if you want rollback support

## API Reference

### Types

```go
type Migration struct {
    Version     int    // Migration version number
    Name        string // Migration name (from filename)
    Description string // Human-readable description
    UpSQL       string // SQL to apply migration
    DownSQL     string // SQL to rollback migration
}

type MigrationStatus struct {
    Version   int    // Migration version
    Name      string // Migration name
    Applied   bool   // Whether migration was applied
    AppliedAt string // When migration was applied (empty if not applied)
}

type Migrator struct {
    // ... internal fields
}
```

### Functions

```go
// Create a new migrator
func NewMigrator(db *sql.DB, dir string) (*Migrator, error)

// Register a migration programmatically
func (m *Migrator) Register(migration Migration)

// Apply all pending migrations
func (m *Migrator) Up(ctx context.Context) error

// Rollback migrations to specified version (0 = all)
func (m *Migrator) Down(ctx context.Context, version int) error

// Get status of all migrations
func (m *Migrator) Status(ctx context.Context) ([]MigrationStatus, error)

// Get current migration version (highest applied)
func (m *Migrator) Version() int
```

## Database Schema

The migrator automatically creates a `_migrations` table:

```sql
CREATE TABLE IF NOT EXISTS _migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

## Best Practices

### 1. Always Include Down SQL

```go
// Good
migrator.Register(migration.Migration{
    Version: 1,
    Name:    "create_users",
    UpSQL:   "CREATE TABLE users (id INTEGER PRIMARY KEY);",
    DownSQL: "DROP TABLE IF EXISTS users;", // ✓ Has rollback
})

// Bad
migrator.Register(migration.Migration{
    Version: 1,
    Name:    "create_users",
    UpSQL:   "CREATE TABLE users (id INTEGER PRIMARY KEY);",
    DownSQL: "", // ✗ No rollback
})
```

### 2. Use Transactions Wisely

Each migration runs in a transaction automatically. Don't add explicit transactions:

```sql
-- Bad: Don't add explicit transactions
-- +migrate Up
BEGIN;
CREATE TABLE users (id INTEGER);
COMMIT;

-- Good: Let the framework handle it
-- +migrate Up
CREATE TABLE users (id INTEGER);
```

### 3. Make Migrations Idempotent

Use `IF NOT EXISTS` and `IF EXISTS`:

```sql
-- Good
CREATE TABLE IF NOT EXISTS users (id INTEGER);
DROP TABLE IF EXISTS users;

-- Bad: Will fail on re-run
CREATE TABLE users (id INTEGER);
DROP TABLE users;
```

### 4. Version Numbering

- Use sequential numbers starting from 1
- Zero-pad to 3 digits (001, 002, ..., 010)
- Never modify existing migration files
- Always add new migrations with higher version numbers

### 5. Test Rollbacks

Always test that your Down SQL correctly reverses the Up SQL:

```go
func TestMigration(t *testing.T) {
    db := testDB(t)
    m, _ := migration.NewMigrator(db, "")

    m.Register(migration.Migration{
        Version: 1,
        Name:    "test",
        UpSQL:   "CREATE TABLE test (id INTEGER);",
        DownSQL: "DROP TABLE IF EXISTS test;",
    })

    ctx := context.Background()

    // Apply
    m.Up(ctx)

    // Rollback
    m.Down(ctx, 0)

    // Verify table doesn't exist
    // ...
}
```

## Error Handling

The migrator returns errors for:
- Invalid migration files
- SQL syntax errors
- Missing Down SQL when rolling back
- Database connection issues

All errors wrap the underlying cause:

```go
if err := migrator.Up(ctx); err != nil {
    // err contains: "migration: apply version N: <cause>"
    log.Printf("Migration failed: %v", err)
}
```

## Performance

- Migrations are cached in memory after first load
- Status checks are O(n) where n = number of migrations
- Concurrent status checks are safe (thread-safe reads)
- Migration application is sequential (cannot run Up concurrently)

## Testing

The package includes comprehensive tests:

```bash
# Run all tests
go test ./internal/migration/... -v

# Run with coverage
go test ./internal/migration/... -cover

# Run benchmarks
go test ./internal/migration/... -bench=.
```

## Examples

See the `migrations/` directory for example migration files:
- `001_init.sql` - Initial schema setup
- `002_add_fts.sql` - Add full-text search support

## License

MIT License - See LICENSE file for details.
