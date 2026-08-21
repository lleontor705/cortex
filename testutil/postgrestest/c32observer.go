package postgrestest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Rows is the small part of pgx.Rows needed by the observer. It also makes the
// parser usable with in-memory rows in unit tests.
type Rows interface {
	Next() bool
	Values() ([]any, error)
	Err() error
	Close()
	FieldDescriptions() []pgconn.FieldDescription
}
type QueryConn interface {
	Query(context.Context, string, ...any) (Rows, error)
	Close(context.Context) error
}
type ConnFactory func(context.Context) (QueryConn, error)
type Clock interface{ Now() time.Time }
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type Config struct {
	RunPrefix, Source string
	Interval          time.Duration
	maxSamples        int
	Pooler, Postgres  ConnFactory
	Clock             Clock
}
type Sample struct {
	Offset            time.Duration
	Wall              time.Time
	Sequence          uint64
	Gap               time.Duration
	Stats             PgBouncerSnapshot
	Activity          ActivitySnapshot
	Config            ConfigSnapshot
	Truncated         bool `json:"truncated,omitempty"`
	ActivityTruncated bool `json:"activity_truncated,omitempty"`
}
type PgBouncerSnapshot struct {
	Stats     map[string]Counter
	Pools     map[string]Pool
	Servers   map[string]Server
	Truncated bool `json:"truncated,omitempty"`
}
type Counter struct{ Xacts, Queries, Received, Sent, Wait, QueryTime, XactTime, Assignments, ClientParse, ServerParse, ClientBind int64 }
type Pool struct{ ClientActive, ClientWaiting, ServerActive, ServerIdle, ServerUsed, ServerTested, ServerLogin, MaxWait int64 }
type Server struct {
	Type                                                                string
	Active, Idle, Used, Tested, Login, New, ActiveCancel, BeingCanceled int64
}
type ActivitySnapshot []ActivityRecord
type ActivityKey struct{ Application, State, WaitEvent string }
type ActivityRecord struct {
	Database    string     `json:"database,omitempty"`
	Application string     `json:"application,omitempty"`
	State       string     `json:"state,omitempty"`
	WaitEvent   string     `json:"wait,omitempty"`
	PID         int64      `json:"pid"`
	QuerySHA256 string     `json:"query_sha256,omitempty"`
	XactStart   *time.Time `json:"xact_start,omitempty"`
	QueryStart  *time.Time `json:"query_start,omitempty"`
}
type Activity struct {
	PID                   int64
	Query                 string
	XactStart, QueryStart *time.Time
}
type ConfigSnapshot struct {
	Source, Identity, Version string
	ActivityRequired          bool `json:"activity_required"`
}

const maxSnapshotEntries = 256

// ParsePGBouncer parses SHOW STATS, SHOW POOLS, and SHOW SERVERS using column
// names (never positional columns), aggregating only databases in RunPrefix.
func ParsePGBouncer(stats, pools, servers Rows, prefix string) (PgBouncerSnapshot, error) {
	out := PgBouncerSnapshot{Stats: map[string]Counter{}, Pools: map[string]Pool{}, Servers: map[string]Server{}}
	if err := parseRows(stats, func(f []pgconn.FieldDescription, v []any) error {
		db, err := textField(f, v, "database")
		if err != nil {
			return err
		}
		if !strings.HasPrefix(db, prefix) {
			return nil
		}
		c := Counter{}
		for n, p := range map[string]*int64{"total_xact_count": &c.Xacts, "total_query_count": &c.Queries, "total_received": &c.Received, "total_sent": &c.Sent, "total_wait_time": &c.Wait, "total_query_time": &c.QueryTime, "total_xact_time": &c.XactTime, "total_server_assignment_count": &c.Assignments, "total_client_parse_count": &c.ClientParse, "total_server_parse_count": &c.ServerParse, "total_bind_count": &c.ClientBind} {
			x, e := numberField(f, v, n)
			if e != nil {
				return e
			}
			*p = x
		}
		if len(out.Stats) >= maxSnapshotEntries && out.Stats[hashMetadata(db)] == (Counter{}) {
			out.Truncated = true
			return nil
		}
		out.Stats[hashMetadata(db)] = addCounter(out.Stats[hashMetadata(db)], c)
		return nil
	}); err != nil {
		return out, fmt.Errorf("SHOW STATS: %w", err)
	}
	if err := parseRows(pools, func(f []pgconn.FieldDescription, v []any) error {
		db, err := textField(f, v, "database")
		if err != nil {
			return err
		}
		if !strings.HasPrefix(db, prefix) {
			return nil
		}
		p := Pool{}
		fields := map[string]*int64{"cl_active": &p.ClientActive, "cl_waiting": &p.ClientWaiting, "sv_active": &p.ServerActive, "sv_idle": &p.ServerIdle, "sv_used": &p.ServerUsed, "sv_tested": &p.ServerTested, "sv_login": &p.ServerLogin, "maxwait": &p.MaxWait}
		for n, d := range fields {
			x, e := numberField(f, v, n)
			if e != nil {
				return e
			}
			*d = x
		}
		if len(out.Pools) >= maxSnapshotEntries && out.Pools[hashMetadata(db)] == (Pool{}) {
			out.Truncated = true
			return nil
		}
		out.Pools[hashMetadata(db)] = addPool(out.Pools[hashMetadata(db)], p)
		return nil
	}); err != nil {
		return out, fmt.Errorf("SHOW POOLS: %w", err)
	}
	if err := parseRows(servers, func(f []pgconn.FieldDescription, v []any) error {
		db, err := textField(f, v, "database")
		if err != nil {
			return err
		}
		if !strings.HasPrefix(db, prefix) {
			return nil
		}
		s := Server{}
		s.Type, _ = optionalText(f, v, "type")
		state, err := textField(f, v, "state")
		if err != nil {
			return err
		}
		if state != "" {
			switch strings.ToLower(state) {
			case "active":
				s.Active++
			case "idle":
				s.Idle++
			case "used":
				s.Used++
			case "tested":
				s.Tested++
			case "new":
				s.New++
			case "active_cancel":
				s.ActiveCancel++
			case "being_canceled":
				s.BeingCanceled++
			default:
				return fmt.Errorf("unknown server state %q", state)
			}
		}
		if s.Type == "" {
			return fmt.Errorf("empty required field %q", "type")
		}
		key := hashMetadata(db) + "/" + hashMetadata(s.Type)
		if len(out.Servers) >= maxSnapshotEntries && out.Servers[key] == (Server{}) {
			out.Truncated = true
			return nil
		}
		out.Servers[key] = addServer(out.Servers[key], s)
		return nil
	}); err != nil {
		return out, fmt.Errorf("SHOW SERVERS: %w", err)
	}
	return out, nil
}

func ParseActivity(rows Rows) (ActivitySnapshot, error) {
	out, _, err := parseActivityBounded(rows)
	return out, err
}

func parseActivityBounded(rows Rows) (ActivitySnapshot, bool, error) {
	out := ActivitySnapshot{}
	truncated := false
	err := parseRows(rows, func(f []pgconn.FieldDescription, v []any) error {
		a := Activity{}
		var e error
		a.PID, e = numberField(f, v, "pid")
		if e != nil {
			return e
		}
		a.Query, _ = optionalText(f, v, "query")
		db, _ := optionalText(f, v, "datname")
		app, _ := optionalText(f, v, "application_name")
		state, _ := optionalText(f, v, "state")
		wait, _ := optionalText(f, v, "wait_event")
		a.XactStart, _ = timeField(f, v, "xact_start")
		a.QueryStart, _ = timeField(f, v, "query_start")
		qhash := ""
		if a.Query != "" {
			qhash = fmt.Sprintf("%x", sha256.Sum256([]byte(a.Query)))
		}
		if len(out) < maxSnapshotEntries {
			out = append(out, ActivityRecord{Database: db, Application: app, State: state, WaitEvent: wait, PID: a.PID, QuerySHA256: qhash, XactStart: a.XactStart, QueryStart: a.QueryStart})
		} else {
			truncated = true
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Database != out[j].Database {
			return out[i].Database < out[j].Database
		}
		return out[i].PID < out[j].PID
	})
	return out, truncated, err
}

func parseRows(r Rows, fn func([]pgconn.FieldDescription, []any) error) error {
	if r == nil {
		return fmt.Errorf("missing source")
	}
	defer r.Close()
	f := r.FieldDescriptions()
	for r.Next() {
		v, e := r.Values()
		if e != nil {
			return e
		}
		if e = fn(f, v); e != nil {
			return e
		}
	}
	return r.Err()
}
func index(f []pgconn.FieldDescription, n string) int {
	for i, x := range f {
		if strings.EqualFold(x.Name, n) {
			return i
		}
	}
	return -1
}
func value(f []pgconn.FieldDescription, v []any, n string) (any, error) {
	i := index(f, n)
	if i < 0 {
		return nil, fmt.Errorf("missing required field %q", n)
	}
	if i >= len(v) {
		return nil, fmt.Errorf("missing value for field %q", n)
	}
	return v[i], nil
}
func number(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int32:
		return int64(x), nil
	case int:
		return int64(x), nil
	case uint64:
		return int64(x), nil
	case string:
		n, e := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n, e
	case []byte:
		n, e := strconv.ParseInt(strings.TrimSpace(string(x)), 10, 64)
		return n, e
	}
	return 0, fmt.Errorf("not numeric: %T", v)
}
func numberField(f []pgconn.FieldDescription, v []any, n string) (int64, error) {
	x, e := value(f, v, n)
	if e != nil {
		return 0, e
	}
	num, e := number(x)
	if e != nil {
		return 0, fmt.Errorf("field %s: %w", n, e)
	}
	return num, nil
}
func text(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []byte:
		return strings.TrimSpace(string(x))
	default:
		return fmt.Sprint(x)
	}
}
func textField(f []pgconn.FieldDescription, v []any, n string) (string, error) {
	x, e := value(f, v, n)
	if e != nil {
		return "", e
	}
	s := text(x)
	if s == "" {
		return "", fmt.Errorf("empty required field %q", n)
	}
	return s, nil
}
func optionalText(f []pgconn.FieldDescription, v []any, n string) (string, error) {
	i := index(f, n)
	if i < 0 || i >= len(v) {
		return "", nil
	}
	return text(v[i]), nil
}
func timeField(f []pgconn.FieldDescription, v []any, n string) (*time.Time, error) {
	i := index(f, n)
	if i < 0 || i >= len(v) || v[i] == nil {
		return nil, nil
	}
	switch x := v[i].(type) {
	case time.Time:
		return &x, nil
	case string:
		t, e := time.Parse(time.RFC3339Nano, x)
		return &t, e
	case []byte:
		t, e := time.Parse(time.RFC3339Nano, string(x))
		return &t, e
	}
	return nil, fmt.Errorf("field %s: not timestamp", n)
}
func addCounter(a, b Counter) Counter {
	a.Xacts += b.Xacts
	a.Queries += b.Queries
	a.Received += b.Received
	a.Sent += b.Sent
	a.Wait += b.Wait
	a.QueryTime += b.QueryTime
	a.XactTime += b.XactTime
	a.Assignments += b.Assignments
	a.ClientParse += b.ClientParse
	a.ServerParse += b.ServerParse
	a.ClientBind += b.ClientBind
	return a
}
func addPool(a, b Pool) Pool {
	a.ClientActive += b.ClientActive
	a.ClientWaiting += b.ClientWaiting
	a.ServerActive += b.ServerActive
	a.ServerIdle += b.ServerIdle
	a.ServerUsed += b.ServerUsed
	a.ServerTested += b.ServerTested
	a.ServerLogin += b.ServerLogin
	a.MaxWait += b.MaxWait
	return a
}
func addServer(a, b Server) Server {
	a.Active += b.Active
	a.Idle += b.Idle
	a.Used += b.Used
	a.Tested += b.Tested
	a.Login += b.Login
	a.New += b.New
	a.ActiveCancel += b.ActiveCancel
	a.BeingCanceled += b.BeingCanceled
	return a
}

type Observer struct {
	cfg       Config
	started   time.Time
	seq       uint64
	last      *Sample
	terminal  bool
	pool, pg  QueryConn
	closeOnce sync.Once
	onSample  func()
}

func NewObserver(c Config) *Observer {
	if c.Clock == nil {
		c.Clock = realClock{}
	}
	return &Observer{cfg: c}
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (o *Observer) Sample(ctx context.Context) (Sample, error) {
	if o.started.IsZero() {
		o.started = o.cfg.Clock.Now()
	}
	now := o.cfg.Clock.Now()
	o.seq++
	offset := now.Sub(o.started)
	s := Sample{Offset: offset, Wall: o.started.Add(offset), Sequence: o.seq}
	if o.last != nil {
		s.Gap = s.Offset - o.last.Offset
	}
	pc, e := o.connections(ctx, o.cfg.Pooler, &o.pool)
	if e != nil {
		return s, e
	}
	stats, e := pc.Query(ctx, "SHOW STATS")
	if e != nil {
		return s, e
	}
	stats, e = materializeRows(stats)
	if e != nil {
		return s, fmt.Errorf("SHOW STATS: %w", e)
	}
	pools, e := pc.Query(ctx, "SHOW POOLS")
	if e != nil {
		return s, e
	}
	pools, e = materializeRows(pools)
	if e != nil {
		return s, fmt.Errorf("SHOW POOLS: %w", e)
	}
	servers, e := pc.Query(ctx, "SHOW SERVERS")
	if e != nil {
		return s, e
	}
	servers, e = materializeRows(servers)
	if e != nil {
		return s, fmt.Errorf("SHOW SERVERS: %w", e)
	}
	s.Stats, e = ParsePGBouncer(stats, pools, servers, o.cfg.RunPrefix)
	if e != nil {
		return s, e
	}
	pg, e := o.connections(ctx, o.cfg.Postgres, &o.pg)
	if e != nil {
		return s, e
	}
	likePrefix := escapeLike(o.cfg.RunPrefix)
	ar, e := pg.Query(ctx, `SELECT pid, datname, application_name, state, wait_event, query, xact_start, query_start FROM pg_stat_activity WHERE datname LIKE $1 ESCAPE '\'`, likePrefix+"%")
	if e != nil {
		return s, e
	}
	s.Activity, s.ActivityTruncated, e = parseActivityBounded(ar)
	if e != nil {
		return s, e
	}
	filtered := s.Activity[:0]
	for _, a := range s.Activity {
		if strings.HasPrefix(a.Database, o.cfg.RunPrefix) {
			filtered = append(filtered, a)
		}
	}
	s.Activity = filtered
	for i := range s.Activity {
		s.Activity[i].Database = hashMetadata(s.Activity[i].Database)
		s.Activity[i].Application = hashMetadata(s.Activity[i].Application)
	}
	s.Config = ConfigSnapshot{Source: hashMetadata(o.cfg.Source), Identity: hashMetadata(o.cfg.RunPrefix), Version: hashMetadata("c32"), ActivityRequired: o.cfg.Postgres != nil}
	s.Truncated = s.Stats.Truncated || s.ActivityTruncated
	o.last = &s
	if o.onSample != nil {
		o.onSample()
	}
	return s, nil
}

func materializeRows(r Rows) (Rows, error) {
	if r == nil {
		return nil, fmt.Errorf("missing source")
	}
	out := &bufferRows{fields: r.FieldDescriptions()}
	for r.Next() {
		v, err := r.Values()
		if err != nil {
			r.Close()
			return nil, err
		}
		out.rows = append(out.rows, append([]any(nil), v...))
	}
	err := r.Err()
	r.Close()
	return out, err
}

type bufferRows struct {
	fields []pgconn.FieldDescription
	rows   [][]any
	i      int
}

func (r *bufferRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}
func (r *bufferRows) Values() ([]any, error)                       { return r.rows[r.i-1], nil }
func (r *bufferRows) Err() error                                   { return nil }
func (r *bufferRows) Close()                                       {}
func (r *bufferRows) FieldDescriptions() []pgconn.FieldDescription { return r.fields }
func (o *Observer) connections(ctx context.Context, f ConnFactory, slot *QueryConn) (QueryConn, error) {
	if *slot != nil {
		return *slot, nil
	}
	if f == nil {
		return nil, fmt.Errorf("missing source")
	}
	c, e := f(ctx)
	if e == nil {
		*slot = c
	}
	return c, e
}
func (o *Observer) Run(ctx context.Context) ([]Sample, error) {
	if o.terminal {
		return nil, fmt.Errorf("observer already terminated")
	}
	defer func() { o.terminal = true }()
	if o.cfg.Interval <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}
	var out []Sample
	for {
		select {
		case <-ctx.Done():
			o.close()
			return out, nil
		default:
		}
		s, e := o.Sample(ctx)
		if e != nil {
			o.close()
			return out, e
		}
		out = append(out, s)
		if o.cfg.maxSamples > 0 && len(out) >= o.cfg.maxSamples {
			o.close()
			return out, errDiagnosticSampleLimit
		}
		t := time.NewTimer(o.cfg.Interval)
		select {
		case <-ctx.Done():
			t.Stop()
			o.close()
			return out, nil
		case <-t.C:
		}
	}
}
func (o *Observer) close() {
	o.closeOnce.Do(func() {
		if o.pool != nil {
			_ = o.pool.Close(context.Background())
			o.pool = nil
		}
		if o.pg != nil {
			_ = o.pg.Close(context.Background())
			o.pg = nil
		}
	})
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}
func hashMetadata(s string) string { return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(s))) }

func Validate(samples []Sample, interval time.Duration, prefix string) error {
	if len(samples) == 0 {
		return fmt.Errorf("no samples")
	}
	var prev Counter
	first := true
	for i, s := range samples {
		if (i == 0 && (s.Sequence != 1 || s.Offset < 0 || s.Wall.IsZero() || s.Gap != 0)) || (i > 0 && (s.Sequence != samples[i-1].Sequence+1 || !s.Wall.After(samples[i-1].Wall) || s.Offset <= samples[i-1].Offset || s.Gap != s.Offset-samples[i-1].Offset || s.Gap != s.Wall.Sub(samples[i-1].Wall) || s.Gap < 0 || s.Gap > 2*interval)) {
			return fmt.Errorf("invalid sequence or gap at %d", i)
		}
		if s.Config.Identity != hashMetadata(prefix) || s.Config.Version == "" || !strings.HasPrefix(s.Config.Source, "sha256:") {
			return fmt.Errorf("prefix mismatch")
		}
		if len(s.Stats.Stats) == 0 || len(s.Stats.Pools) == 0 || len(s.Stats.Servers) == 0 || (s.Config.ActivityRequired && len(s.Activity) == 0) {
			return fmt.Errorf("missing source")
		}
		var total Counter
		for db, c := range s.Stats.Stats {
			if !strings.HasPrefix(db, "sha256:") {
				return fmt.Errorf("unredacted database %q", db)
			}
			total = addCounter(total, c)
		}
		for db := range s.Stats.Pools {
			if !strings.HasPrefix(db, "sha256:") {
				return fmt.Errorf("unredacted pool database %q", db)
			}
		}
		for db, p := range s.Stats.Pools {
			if p.ClientActive < 0 || p.ClientWaiting < 0 || p.ServerActive < 0 || p.ServerIdle < 0 || p.ServerUsed < 0 || p.ServerTested < 0 || p.ServerLogin < 0 || p.MaxWait < 0 {
				return fmt.Errorf("negative pool count for %q", db)
			}
		}
		for key := range s.Stats.Servers {
			parts := strings.SplitN(key, "/", 2)
			if len(parts) != 2 || !strings.HasPrefix(parts[0], "sha256:") || !strings.HasPrefix(parts[1], "sha256:") {
				return fmt.Errorf("unredacted server identity %q", key)
			}
		}
		for key, srv := range s.Stats.Servers {
			if srv.Active < 0 || srv.Idle < 0 || srv.Used < 0 || srv.Tested < 0 || srv.Login < 0 || srv.New < 0 || srv.ActiveCancel < 0 || srv.BeingCanceled < 0 {
				return fmt.Errorf("negative server count for %q", key)
			}
		}
		if total.Xacts < 0 || total.Queries < 0 || total.Received < 0 || total.Sent < 0 || total.Wait < 0 || total.QueryTime < 0 || total.XactTime < 0 || total.Assignments < 0 || total.ClientParse < 0 || total.ServerParse < 0 || total.ClientBind < 0 {
			return fmt.Errorf("negative cumulative counter")
		}
		if !first && (total.Xacts < prev.Xacts || total.Queries < prev.Queries || total.Received < prev.Received || total.Sent < prev.Sent || total.Wait < prev.Wait || total.QueryTime < prev.QueryTime || total.XactTime < prev.XactTime || total.Assignments < prev.Assignments || total.ClientParse < prev.ClientParse || total.ServerParse < prev.ServerParse || total.ClientBind < prev.ClientBind) {
			return fmt.Errorf("counter regression")
		}
		if s.Truncated || s.Stats.Truncated {
			return fmt.Errorf("bounded observer evidence truncated")
		}
		for _, a := range s.Activity {
			if !strings.HasPrefix(a.Database, "sha256:") || !strings.HasPrefix(a.Application, "sha256:") || a.PID < 0 {
				return fmt.Errorf("invalid activity evidence")
			}
		}
		prev = total
		first = false
	}
	return nil
}

type Overhead struct{ Off, On []time.Duration }

func (o Overhead) Pass() bool {
	return overheadRatio(o.On, 0.50) <= 1.05*overheadRatio(o.Off, 0.50) && overheadRatio(o.On, 0.95) <= 1.05*overheadRatio(o.Off, 0.95)
}
func overheadRatio(v []time.Duration, q float64) float64 {
	if len(v) == 0 {
		return 0
	}
	x := append([]time.Duration(nil), v...)
	sort.Slice(x, func(i, j int) bool { return x[i] < x[j] })
	i := int(float64(len(x)-1) * q)
	return float64(x[i])
}

// Diagnostic is an opt-in, fail-closed telemetry controller for C32.  It is
// deliberately separate from the protocol runner: its verdict is never an
// input to the runner's PASS/FAIL decision.
type DiagnosticStatus string

const (
	diagnosticSampleCushion                  = 100
	maxDiagnosticSamples                     = 18_000 + diagnosticSampleCushion
	maxDiagnosticEvents                      = 256
	DiagnosticPass          DiagnosticStatus = "PASS"
	DiagnosticBlocked       DiagnosticStatus = "BLOCKED"
	DiagnosticInconclusive  DiagnosticStatus = "INCONCLUSIVE"
	DiagnosticInterval                       = 100 * time.Millisecond
)

var errDiagnosticSampleLimit = errors.New("diagnostic sample limit reached")

type Phase string

const (
	PhasePath       Phase = "path"
	PhaseRepetition Phase = "repetition"
	PhaseBlock      Phase = "block"
	PhasePopulation Phase = "population"
	PhaseCold       Phase = "cold"
	PhaseWarm       Phase = "warm"
	PhaseMeasured   Phase = "measured"
)

type PhaseEvent struct {
	Phase Phase     `json:"phase"`
	At    time.Time `json:"at"`
}

type DiagnosticReport struct {
	Identity    string           `json:"identity"`
	Status      DiagnosticStatus `json:"status"`
	Overhead    OverheadReport   `json:"overhead"`
	Complete    bool             `json:"complete"`
	Gaps        []string         `json:"gaps,omitempty"`
	Samples     []Sample         `json:"samples,omitempty"`
	PhaseEvents []PhaseEvent     `json:"phase_events,omitempty"`
	Truncated   bool             `json:"truncated,omitempty"`
}

type OverheadReport struct {
	OffP50 time.Duration `json:"off_p50_ns"`
	OnP50  time.Duration `json:"on_p50_ns"`
	OffP95 time.Duration `json:"off_p95_ns"`
	OnP95  time.Duration `json:"on_p95_ns"`
	Pass   bool          `json:"pass"`
}

// DiagnosticEnabled intentionally checks only the exact opt-in value.
func DiagnosticEnabled() bool { return os.Getenv("CORTEX_C32_OBSERVER") == "1" }

type Diagnostic struct {
	observer  *Observer
	identity  string
	clock     Clock
	mu        sync.Mutex
	samples   []Sample
	events    []PhaseEvent
	gaps      []string
	overhead  OverheadReport
	status    DiagnosticStatus
	truncated bool
	started   bool
	stop      sync.Once
	report    sync.Once
	reportErr error
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	first     chan struct{}
	firstOnce sync.Once
}

// DiagnosticFirstSampleTimeout bounds startup even when a caller supplies a
// context without a deadline.
const DiagnosticFirstSampleTimeout = 30 * time.Second

func NewDiagnostic(c Config) *Diagnostic {
	if c.Interval <= 0 {
		c.Interval = DiagnosticInterval
	}
	return &Diagnostic{observer: NewObserver(c), identity: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(c.RunPrefix))), clock: c.Clock, status: DiagnosticBlocked}
}

func (d *Diagnostic) RecordPhase(p Phase) {
	if d == nil || !DiagnosticEnabled() {
		return
	}
	d.mu.Lock()
	clock := d.clock
	if clock == nil {
		clock = realClock{}
	}
	d.events = append(d.events, PhaseEvent{Phase: p, At: clock.Now().UTC()})
	if len(d.events) > maxDiagnosticEvents {
		d.events = d.events[:maxDiagnosticEvents]
		d.truncated = true
	}
	d.mu.Unlock()
}

// Start performs the overhead gate before the sampler is started. Empty or
// mismatched baselines are incomplete telemetry, not a pass.
func (d *Diagnostic) Start(ctx context.Context, off, on []time.Duration) error {
	if d == nil || !DiagnosticEnabled() {
		return nil
	}
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return fmt.Errorf("diagnostic already started")
	}
	if len(on) > 0 {
		d.overhead = overheadReport(off, on)
	} else {
		d.overhead = OverheadReport{OffP50: percentile(off, .50), OffP95: percentile(off, .95)}
	}
	d.started = true
	if len(off) == 0 || (len(on) > 0 && len(off) != len(on)) {
		d.gaps = append(d.gaps, "overhead samples incomplete")
		d.status = DiagnosticInconclusive
		d.mu.Unlock()
		return nil
	}
	if len(on) > 0 && !d.overhead.Pass {
		d.gaps = append(d.gaps, "observer overhead exceeds five percent")
		d.status = DiagnosticBlocked
		d.mu.Unlock()
		return nil
	}
	d.status = DiagnosticInconclusive
	d.observer.cfg.maxSamples = maxDiagnosticSamples
	d.ctx, d.cancel = context.WithCancel(ctx)
	d.done = make(chan struct{})
	d.first = make(chan struct{})
	d.observer.onSample = func() { d.firstOnce.Do(func() { close(d.first) }) }
	d.mu.Unlock()
	go func() {
		samples, err := d.observer.Run(d.ctx)
		d.mu.Lock()
		d.samples = append(d.samples, samples...)
		for _, sample := range samples {
			if sample.Truncated {
				d.gaps = append(d.gaps, "activity snapshot truncated")
				d.truncated = true
			}
		}
		if err != nil {
			if errors.Is(err, errDiagnosticSampleLimit) {
				d.gaps = append(d.gaps, "sample bound exceeded")
				d.truncated = true
			} else {
				d.gaps = append(d.gaps, "observer sampling failed")
			}
			d.status = DiagnosticInconclusive
		}
		d.mu.Unlock()
		close(d.done)
	}()
	return nil
}

// WaitFirstSample waits until the sampler has successfully populated one
// complete sample. A timeout is a diagnostic gap, never a pass.
func (d *Diagnostic) WaitFirstSample(ctx context.Context) bool {
	if d == nil || !DiagnosticEnabled() {
		return false
	}
	d.mu.Lock()
	first := d.first
	done := d.done
	d.mu.Unlock()
	if first == nil || done == nil {
		return false
	}
	select {
	case <-first:
		return true
	case <-ctx.Done():
		return false
	case <-done:
		return false
	}
}

// SetMeasuredOverhead replaces the provisional gate measurements after the
// sampler has been started.  This is used by wired tests whose ON arm must be
// measured while the observer is live.
func (d *Diagnostic) SetMeasuredOverhead(off, on []time.Duration) {
	if d == nil || !DiagnosticEnabled() {
		return
	}
	d.mu.Lock()
	d.overhead = overheadReport(off, on)
	if len(off) == 0 || len(off) != len(on) || !d.overhead.Pass {
		d.gaps = append(d.gaps, "overhead samples incomplete or above five percent")
		d.status = DiagnosticBlocked
		cancel := d.cancel
		d.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return
	}
	d.status = DiagnosticPass
	d.mu.Unlock()
}

func overheadReport(off, on []time.Duration) OverheadReport {
	if len(off) == 0 || len(off) != len(on) {
		return OverheadReport{}
	}
	return OverheadReport{OffP50: percentile(off, .50), OnP50: percentile(on, .50), OffP95: percentile(off, .95), OnP95: percentile(on, .95), Pass: (Overhead{Off: off, On: on}).Pass()}
}

func percentile(v []time.Duration, q float64) time.Duration {
	if len(v) == 0 {
		return 0
	}
	x := append([]time.Duration(nil), v...)
	sort.Slice(x, func(i, j int) bool { return x[i] < x[j] })
	return x[int(float64(len(x)-1)*q)]
}

// Stop is cancellation-safe and idempotent. A started sampler is always
// stopped before the snapshot is returned.
func (d *Diagnostic) Stop() DiagnosticReport {
	if d == nil || !DiagnosticEnabled() {
		return DiagnosticReport{}
	}
	d.stop.Do(func() {
		d.mu.Lock()
		cancel := d.cancel
		done := d.done
		d.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	complete := d.status == DiagnosticPass && len(d.samples) > 0 && len(d.gaps) == 0 && !d.truncated
	if complete {
		if err := Validate(d.samples, d.observer.cfg.Interval, d.observer.cfg.RunPrefix); err != nil {
			d.gaps = append(d.gaps, "strict telemetry validation failed")
			complete = false
		}
	}
	if !complete {
		d.status = DiagnosticBlocked
	}
	return DiagnosticReport{Identity: d.identity, Status: d.status, Overhead: d.overhead, Complete: complete, Gaps: append([]string(nil), d.gaps...), Samples: append([]Sample(nil), d.samples...), PhaseEvents: append([]PhaseEvent(nil), d.events...), Truncated: d.truncated}
}

// EmitReport emits the one diagnostic line. The caller supplies the logger so
// this package never couples telemetry to testing.T or a production logger.
// JSON contains only bounded observer data and the sanitized run identity.
func (d *Diagnostic) EmitReport(emit any) error {
	if d == nil || emit == nil || !DiagnosticEnabled() {
		return nil
	}
	d.report.Do(func() {
		raw, err := json.Marshal(d.Stop())
		if err != nil {
			d.reportErr = fmt.Errorf("serialize C32 observer report: %w", err)
			return
		}
		line := "C32_OBSERVER_REPORT " + string(raw)
		switch fn := emit.(type) {
		case func(string) error:
			d.reportErr = fn(line)
		case func(string):
			fn(line)
		default:
			d.reportErr = fmt.Errorf("unsupported C32 observer report sink %T", emit)
		}
		if d.reportErr != nil {
			d.reportErr = fmt.Errorf("emit C32 observer report: %w", d.reportErr)
			d.mu.Lock()
			d.status = DiagnosticBlocked
			d.gaps = append(d.gaps, "report emission failed")
			d.mu.Unlock()
		}
	})
	return d.reportErr
}
