package postgrestest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type lifecycleRows struct {
	observerRows
	closed *int
}

func (r *lifecycleRows) Close() { (*r.closed)++ }

type lifecycleConn struct {
	queries []string
	closes  int
	open    *int
}

func (c *lifecycleConn) Query(_ context.Context, sql string, _ ...any) (Rows, error) {
	if c.open != nil && *c.open == 0 {
		return nil, fmt.Errorf("previous rows not closed")
	}
	c.queries = append(c.queries, sql)
	closed := new(int)
	c.open = closed
	switch sql {
	case "SHOW STATS":
		return &lifecycleRows{observerRows: *row([]string{"database", "total_xact_count", "total_query_count", "total_received", "total_sent", "total_wait_time", "total_query_time", "total_xact_time", "total_server_assignment_count", "total_client_parse_count", "total_server_parse_count", "total_bind_count"}, "run_db", int64(1), int64(1), int64(1), int64(1), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0)), closed: closed}, nil
	case "SHOW POOLS":
		return &lifecycleRows{observerRows: *row([]string{"database", "cl_active", "cl_waiting", "sv_active", "sv_idle", "sv_used", "sv_tested", "sv_login", "maxwait"}, "run_db", int64(1), int64(0), int64(1), int64(1), int64(0), int64(0), int64(0), int64(0)), closed: closed}, nil
	case "SHOW SERVERS":
		return &lifecycleRows{observerRows: *row([]string{"database", "type", "state"}, "run_db", "S", "active"), closed: closed}, nil
	default:
		return &lifecycleRows{observerRows: *row([]string{"pid", "datname", "application_name", "state"}, int64(1), "run_db", "app", "active"), closed: closed}, nil
	}
}
func (c *lifecycleConn) Close(context.Context) error { c.closes++; return nil }

type observerRows struct {
	fields []pgconn.FieldDescription
	rows   [][]any
	i      int
}

func (r *observerRows) Next() bool                                   { r.i++; return r.i <= len(r.rows) }
func (r *observerRows) Values() ([]any, error)                       { return r.rows[r.i-1], nil }
func (r *observerRows) Err() error                                   { return nil }
func (r *observerRows) Close()                                       {}
func (r *observerRows) FieldDescriptions() []pgconn.FieldDescription { return r.fields }
func row(names []string, values ...any) *observerRows {
	f := make([]pgconn.FieldDescription, len(names))
	for i, n := range names {
		f[i].Name = n
	}
	return &observerRows{fields: f, rows: [][]any{values}}
}

func TestParsePGBouncerNamedFieldsAndPrefixAggregation(t *testing.T) {
	n := []string{"total_query_count", "database", "total_xact_count", "total_received", "total_sent", "total_wait_time", "total_query_time", "total_xact_time", "total_server_assignment_count", "total_client_parse_count", "total_server_parse_count", "total_bind_count"}
	vals := func(db string, q, x int64) []any {
		return []any{q, db, x, "4", "5", "6", "7", "8", "9", "10", "11", "12"}
	}
	stats := &observerRows{fields: func() []pgconn.FieldDescription {
		f := make([]pgconn.FieldDescription, len(n))
		for i, x := range n {
			f[i].Name = x
		}
		return f
	}(), rows: [][]any{vals("run_1", 2, 3), vals("run_2", 4, 5), vals("other", 99, 99)}}
	pfields := []string{"sv_idle", "database", "cl_active", "cl_waiting", "sv_active", "sv_used", "sv_tested", "sv_login", "maxwait"}
	pools := row(pfields, "1", "run_1", "2", "3", "4", "5", "6", "7", "8")
	sfields := []string{"database", "type", "state"}
	servers := row(sfields, "run_1", "S", "active")
	s, err := ParsePGBouncer(stats, pools, servers, "run_")
	if err != nil {
		t.Fatal(err)
	}
	if s.Stats[hashMetadata("run_1")].Queries != 2 || len(s.Stats) != 2 {
		t.Fatalf("stats=%+v", s.Stats)
	}
}

func TestValidateGapsResetAndPrefix(t *testing.T) {
	base := Sample{Sequence: 1, Wall: time.Unix(1, 0), Offset: time.Second, Config: ConfigSnapshot{Identity: "run_"}, Stats: PgBouncerSnapshot{Stats: map[string]Counter{"run_1": {Xacts: 10}}, Pools: map[string]Pool{"run_1": {}}, Servers: map[string]Server{"run_1/S": {}}}, Activity: ActivitySnapshot{{Database: "run_1", PID: 1}}}
	next := base
	next.Sequence = 2
	next.Wall = base.Wall.Add(time.Second)
	next.Offset = 2 * time.Second
	next.Gap = 2 * time.Second
	next.Stats.Stats = map[string]Counter{"run_1": {Xacts: 9}}
	if Validate([]Sample{base, next}, time.Second, "run_") == nil {
		t.Fatal("counter reset accepted")
	}
	next.Stats.Stats = map[string]Counter{"run_1": {Xacts: 11}}
	next.Gap = 3 * time.Second
	if Validate([]Sample{base, next}, time.Second, "run_") == nil {
		t.Fatal("large gap accepted")
	}
}

func TestValidateRejectsTimingAndNegativePoolServerMutations(t *testing.T) {
	base := Sample{Sequence: 1, Wall: time.Unix(1, 0), Offset: time.Second, Config: ConfigSnapshot{Identity: hashMetadata("run_"), Source: "sha256:source", Version: "sha256:c32"}, Stats: PgBouncerSnapshot{Stats: map[string]Counter{"sha256:db": {Xacts: 10}}, Pools: map[string]Pool{"sha256:db": {ClientActive: 1}}, Servers: map[string]Server{"sha256:db/sha256:S": {Active: 1}}}}
	next := base
	next.Sequence = 2
	next.Offset = 2 * time.Second
	next.Wall = base.Wall.Add(2 * time.Second)
	next.Gap = time.Second
	if Validate([]Sample{base, next}, time.Second, "run_") == nil {
		t.Fatal("inconsistent wall/gap timing accepted")
	}
	for name, mutate := range map[string]func(*Sample){
		"pool":   func(s *Sample) { s.Stats.Pools["sha256:db"] = Pool{ClientActive: -1} },
		"server": func(s *Sample) { s.Stats.Servers["sha256:db/sha256:S"] = Server{Active: -1} },
	} {
		s := base
		s.Stats = PgBouncerSnapshot{Stats: map[string]Counter{"sha256:db": {Xacts: 10}}, Pools: map[string]Pool{"sha256:db": {ClientActive: 1}}, Servers: map[string]Server{"sha256:db/sha256:S": {Active: 1}}}
		mutate(&s)
		if Validate([]Sample{s}, time.Second, "run_") == nil {
			t.Fatalf("negative %s count accepted", name)
		}
	}
}

func TestObserverDerivesWallFromOffset(t *testing.T) {
	now := time.Unix(10, 0)
	o := NewObserver(Config{RunPrefix: "run_", Source: "test", Clock: ClockFunc(func() time.Time { old := now; now = now.Add(time.Second); return old }), Pooler: func(context.Context) (QueryConn, error) { return &lifecycleConn{}, nil }, Postgres: func(context.Context) (QueryConn, error) { return &lifecycleConn{}, nil }})
	first, err := o.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := o.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Wall.Sub(first.Wall) != second.Gap || second.Gap != second.Offset-first.Offset {
		t.Fatalf("timing relation wall=%v gap=%v offset=%v", second.Wall.Sub(first.Wall), second.Gap, second.Offset-first.Offset)
	}
}

func TestOverheadBoundaryUsesMedianAndP95(t *testing.T) {
	o := Overhead{Off: []time.Duration{100, 100, 100, 100, 100}, On: []time.Duration{105, 105, 105, 105, 105}}
	if !o.Pass() {
		t.Fatal("exact five percent overhead rejected")
	}
	o.On[3] = 106
	o.On[4] = 106
	if o.Pass() {
		t.Fatal("overhead above five percent accepted")
	}
}

func TestObserverCachesConnectionsAndClosesOnlyAtTerminalShutdown(t *testing.T) {
	pool, pg := &lifecycleConn{}, &lifecycleConn{}
	poolOpens, pgOpens := 0, 0
	o := NewObserver(Config{RunPrefix: "run_", Source: "test", Pooler: func(context.Context) (QueryConn, error) { poolOpens++; return pool, nil }, Postgres: func(context.Context) (QueryConn, error) { pgOpens++; return pg, nil }})
	for i := 0; i < 3; i++ {
		if _, err := o.Sample(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if poolOpens != 1 || pgOpens != 1 {
		t.Fatalf("opens pool=%d pg=%d", poolOpens, pgOpens)
	}
	if len(pool.queries) != 9 {
		t.Fatalf("query count=%d", len(pool.queries))
	}
	for i, want := range []string{"SHOW STATS", "SHOW POOLS", "SHOW SERVERS"} {
		for sample := 0; sample < 3; sample++ {
			if pool.queries[sample*3+i] != want {
				t.Fatalf("query order=%v", pool.queries)
			}
		}
	}
	o.close()
	o.close()
	if pool.closes != 1 || pg.closes != 1 {
		t.Fatalf("close count pool=%d pg=%d", pool.closes, pg.closes)
	}
}

func TestPGXPoolerConfigTargetsAdminSimpleProtocol(t *testing.T) {
	cfg, err := PGXPoolerConfig("postgres://user:pass@localhost:6432/run_db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database != "pgbouncer" || cfg.DefaultQueryExecMode != pgx.QueryExecModeSimpleProtocol {
		t.Fatalf("pooler config database=%q protocol=%v", cfg.Database, cfg.DefaultQueryExecMode)
	}
}
