//go:build postgres_integration

package postgres

import (
	"context"
	"errors"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const rwC32Unlimited = ^uint64(0)

type rwC32CPUStat struct{ total, idle uint64 }

func rwReadCPUStat(t *testing.T) rwC32CPUStat {
	t.Helper()
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		t.Fatalf("c32 environment read /proc/stat: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		if len(fields) < 4 {
			t.Fatal("c32 environment malformed /proc/stat")
		}
		var stat rwC32CPUStat
		for i, field := range fields {
			n, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				t.Fatalf("c32 environment parse /proc/stat: %v", err)
			}
			stat.total += n
			if i == 3 || i == 4 {
				stat.idle += n
			}
		}
		return stat
	}
	t.Fatal("c32 environment missing aggregate CPU line")
	return rwC32CPUStat{}
}

func rwC32IntervalUtilization(t *testing.T) []float64 {
	t.Helper()
	const buckets = 10
	values := make([]float64, 0, buckets)
	previous := rwReadCPUStat(t)
	for i := 0; i < buckets; i++ {
		time.Sleep(time.Second)
		current := rwReadCPUStat(t)
		total, idle := current.total-previous.total, current.idle-previous.idle
		if total == 0 || idle > total {
			t.Fatal("c32 environment invalid CPU interval delta")
		}
		values = append(values, 100*float64(total-idle)/float64(total))
		previous = current
	}
	return values
}

func rwReadCgroupUint(t *testing.T, paths ...string) (uint64, bool) {
	t.Helper()
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value := strings.TrimSpace(string(raw))
		if value == "max" {
			return rwC32Unlimited, true
		}
		n, err := strconv.ParseUint(value, 10, 64)
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

func rwC32CPUSetCount(value string) int {
	count := 0
	for _, item := range strings.Split(strings.TrimSpace(value), ",") {
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, "-", 2)
		start, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0
		}
		end := start
		if len(parts) == 2 {
			end, err = strconv.Atoi(parts[1])
			if err != nil || end < start {
				return 0
			}
		}
		count += end - start + 1
	}
	return count
}

func rwC32EffectiveCPUs(t *testing.T) int {
	t.Helper()
	host := runtime.NumCPU()
	cpuset := host
	for _, path := range []string{"/sys/fs/cgroup/cpuset.cpus.effective", "/sys/fs/cgroup/cpuset/cpuset.cpus"} {
		if raw, err := os.ReadFile(path); err == nil {
			if n := rwC32CPUSetCount(string(raw)); n > 0 {
				cpuset = n
				break
			}
		}
	}
	quotaCPU := float64(host)
	if raw, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		fields := strings.Fields(string(raw))
		if len(fields) == 2 && fields[0] != "max" {
			quota, qerr := strconv.ParseFloat(fields[0], 64)
			period, perr := strconv.ParseFloat(fields[1], 64)
			if qerr != nil || perr != nil || period <= 0 {
				t.Fatal("c32 environment malformed cpu.max")
			}
			quotaCPU = quota / period
		}
	} else if quota, ok := rwReadCgroupUint(t, "/sys/fs/cgroup/cpu/cpu.cfs_quota_us"); ok && quota != rwC32Unlimited {
		period, present := rwReadCgroupUint(t, "/sys/fs/cgroup/cpu/cpu.cfs_period_us")
		if !present || period == 0 {
			t.Fatal("c32 environment missing cgroup CPU period")
		}
		quotaCPU = float64(quota) / float64(period)
	}
	return int(math.Floor(math.Min(float64(host), math.Min(float64(cpuset), quotaCPU))))
}

func rwC32EffectiveMemory(t *testing.T) uint64 {
	t.Helper()
	if value, ok := rwReadCgroupUint(t, "/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"); ok && value != rwC32Unlimited {
		return value
	}
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		t.Fatalf("c32 environment read memory: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			n, _ := strconv.ParseUint(fields[1], 10, 64)
			return n * 1024
		}
	}
	t.Fatal("c32 environment missing memory limit")
	return 0
}

func rwC32Throttle(t *testing.T) uint64 {
	t.Helper()
	for _, path := range []string{"/sys/fs/cgroup/cpu.stat", "/sys/fs/cgroup/cpu/cpu.stat"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "nr_throttled" {
				n, err := strconv.ParseUint(fields[1], 10, 64)
				if err != nil {
					t.Fatal("c32 environment malformed nr_throttled")
				}
				return n
			}
		}
	}
	t.Fatal("c32 environment missing nr_throttled")
	return 0
}

type rwC32PostgresEnvironment struct {
	version                                      string
	maxConnections, baselineActive, peakSessions int
}

func rwC32PostgresFacts(t *testing.T, ctx context.Context, admin *pgxpool.Pool) rwC32PostgresEnvironment {
	t.Helper()
	var facts rwC32PostgresEnvironment
	if err := admin.QueryRow(ctx, "SHOW server_version").Scan(&facts.version); err != nil {
		t.Fatalf("c32 environment PostgreSQL version: %v", err)
	}
	if !strings.HasPrefix(facts.version, "16.") {
		t.Fatalf("c32 environment PostgreSQL 16 required (got %s)", facts.version)
	}
	var rawMaxConnections string
	if err := admin.QueryRow(ctx, "SHOW max_connections").Scan(&rawMaxConnections); err != nil {
		t.Fatalf("c32 environment max_connections: %v", err)
	}
	maxConnections, err := rwParseMaxConnections(rawMaxConnections)
	if err != nil {
		t.Fatalf("c32 environment max_connections: %v", err)
	}
	facts.maxConnections = maxConnections
	query := `SELECT count(*) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND state <> 'idle' AND application_name NOT LIKE 'cortex-c32-harness%'`
	if err := admin.QueryRow(ctx, query).Scan(&facts.baselineActive); err != nil {
		t.Fatalf("c32 environment active sessions: %v", err)
	}
	facts.peakSessions = facts.baselineActive
	return facts
}

func rwParseMaxConnections(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errMaxConnectionsEmpty
	}
	connections, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if connections <= 0 {
		return 0, errMaxConnectionsNonPositive
	}
	return connections, nil
}

var errMaxConnectionsEmpty = errors.New("SHOW max_connections empty")
var errMaxConnectionsNonPositive = errors.New("SHOW max_connections must be positive")

func TestRwParseMaxConnections(t *testing.T) {
	t.Run("accepts valid integer text", func(t *testing.T) {
		n, err := rwParseMaxConnections("200")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 200 {
			t.Fatalf("want 200, got %d", n)
		}
	})
	t.Run("rejects empty text", func(t *testing.T) {
		if _, err := rwParseMaxConnections(""); !errors.Is(err, errMaxConnectionsEmpty) {
			t.Fatalf("want empty error, got %v", err)
		}
	})
	t.Run("rejects nonnumeric text", func(t *testing.T) {
		if _, err := rwParseMaxConnections("ten"); err == nil {
			t.Fatal("want parse error")
		}
	})
	t.Run("rejects zero", func(t *testing.T) {
		if _, err := rwParseMaxConnections("0"); !errors.Is(err, errMaxConnectionsNonPositive) {
			t.Fatalf("want non-positive error, got %v", err)
		}
	})
	t.Run("rejects negative", func(t *testing.T) {
		if _, err := rwParseMaxConnections("-5"); !errors.Is(err, errMaxConnectionsNonPositive) {
			t.Fatalf("want non-positive error, got %v", err)
		}
	})
}

func TestRwPgbouncerConfigShapeFromFourColumns(t *testing.T) {
	fields := []pgconn.FieldDescription{
		{Name: "changeable"},
		{Name: "default"},
		{Name: "name"},
		{Name: "value"},
	}
	values := []any{"yes", "session", "pool_mode", "transaction"}
	name, setting, ok := rwPgbouncerConfigSettingFromRow(fields, values)
	if !ok {
		t.Fatal("failed to parse config row")
	}
	if name != "pool_mode" {
		t.Fatalf("name = %q, want pool_mode", name)
	}
	if setting.Value != "transaction" {
		t.Fatalf("value = %q, want transaction", setting.Value)
	}
	if setting.Default != "session" {
		t.Fatalf("default = %q, want session", setting.Default)
	}
	if setting.Changeable != "yes" {
		t.Fatalf("changeable = %q, want yes", setting.Changeable)
	}

	keyFields := []pgconn.FieldDescription{
		{Name: "key"},
		{Name: "value"},
		{Name: "default"},
		{Name: "changeable"},
	}
	keyValues := []any{"default_pool_size", "128", "128", "no"}
	keyName, keySetting, ok := rwPgbouncerConfigSettingFromRow(keyFields, keyValues)
	if !ok {
		t.Fatal("failed to parse key-based config row")
	}
	if keyName != "default_pool_size" {
		t.Fatalf("name from key fixture = %q, want default_pool_size", keyName)
	}
	if keySetting.Value != "128" {
		t.Fatalf("key setting value = %q, want 128", keySetting.Value)
	}

	capacityFields := []pgconn.FieldDescription{
		{Name: "name"},
		{Name: "value"},
		{Name: "default"},
		{Name: "changeable"},
	}
	capacityValues := []any{"default_pool_size", "32", "32", "no"}
	capacityName, capacity, ok := rwPgbouncerConfigSettingFromRow(capacityFields, capacityValues)
	if !ok {
		t.Fatal("failed to parse default_pool_size row")
	}
	if capacityName != "default_pool_size" {
		t.Fatalf("capacity name = %q, want default_pool_size", capacityName)
	}
	if capacity.Value != "32" {
		t.Fatalf("capacity value = %q, want 32", capacity.Value)
	}
	mode, poolCapacity, err := rwPgbouncerConfigRequiredFacts(map[string]rwPgbouncerShowConfigSetting{
		name:         setting,
		capacityName: capacity,
	})
	if err != nil {
		t.Fatalf("extract required facts: %v", err)
	}
	if mode != "transaction" {
		t.Fatalf("mode = %q, want transaction", mode)
	}
	if poolCapacity != 32 {
		t.Fatalf("pool capacity = %d, want 32", poolCapacity)
	}
}

func rwC32PoolerFacts(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	settings := map[string]rwPgbouncerShowConfigSetting{}
	rows, err := conn.Query(ctx, "SHOW CONFIG")
	if err != nil {
		t.Fatalf("c32 environment PgBouncer SHOW CONFIG: %v", err)
	}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			rows.Close()
			t.Fatalf("read PgBouncer SHOW CONFIG row: %v", err)
		}
		name, setting, ok := rwPgbouncerConfigSettingFromRow(rows.FieldDescriptions(), values)
		if !ok {
			continue
		}
		settings[name] = setting
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("PgBouncer SHOW CONFIG rows: %v", err)
	}
	rows.Close()
	mode, slots, err := rwPgbouncerConfigRequiredFacts(settings)
	if err != nil {
		t.Fatalf("c32 environment PgBouncer preflight: %v", err)
	}
	if strings.ToLower(mode) != "transaction" {
		t.Fatalf("c32 environment PgBouncer transaction mode required (got %s)", mode)
	}
	if slots < rwC32Workers {
		t.Fatalf("c32 environment PgBouncer slots=%d, want >=%d", slots, rwC32Workers)
	}
	rows, err = conn.Query(ctx, "SHOW POOLS")
	if err != nil {
		t.Fatalf("c32 environment PgBouncer SHOW POOLS: %v", err)
	}
	var clWaiting, maxWait int64
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			rows.Close()
			t.Fatalf("read PgBouncer SHOW POOLS row: %v", err)
		}
		state := rwPoolerStateFromRow(rows.FieldDescriptions(), values)
		clWaiting += state.clWaiting
		maxWait += state.maxwait
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("PgBouncer SHOW POOLS rows: %v", err)
	}
	rows.Close()
	if clWaiting != 0 {
		t.Fatalf("c32 environment PgBouncer cl_waiting=%d", clWaiting)
	}
	if maxWait != 0 {
		t.Fatalf("c32 environment PgBouncer maxwait=%d", maxWait)
	}
}
