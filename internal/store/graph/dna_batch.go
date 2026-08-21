// dna_batch.go implements the optional DNA batch edge-count capability on the
// local SQLite graph store. It is deliberately separate from store.go so the
// traversal work owned by other tasks stays untouched; the method is an
// additive, read-only hydration helper with per-ID semantics identical to
// CountEdgesByObservation.
package graph

import (
	"context"
	"fmt"
	"strings"
)

// maxDNABatchIDs bounds the IDs per statement so batch lookups stay within
// SQLite's conservative variable limit; the DNA N=500 path fits one chunk.
const maxDNABatchIDs = 500

// CountEdgesByObservationIDs counts edges connected to each requested
// observation in one statement per maxDNABatchIDs chunk. The statement unions
// two index-driven GROUP BY scans — one over edge sources using
// idx_edges_from, one over non-self-loop edge targets using idx_edges_to —
// and the two row sets are merged in Go. Counts match
// CountEdgesByObservation exactly: an edge contributes once per listed
// endpoint, and a self-loop counts once because only the source scan sees it
// (the target scan excludes from_obs_id = to_obs_id rows). IDs with no edges
// (and an empty input) yield no entry — callers treat absence as zero.
func (s *Store) CountEdgesByObservationIDs(ctx context.Context, obsIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(obsIDs))
	if len(obsIDs) == 0 {
		return out, nil
	}

	for start := 0; start < len(obsIDs); start += maxDNABatchIDs {
		end := start + maxDNABatchIDs
		if end > len(obsIDs) {
			end = len(obsIDs)
		}
		chunk := obsIDs[start:end]

		// Each chunk ID is bound twice: once for the source scan and once
		// for the target scan. (A VALUES CTE is avoided because the
		// modernc.org/sqlite parser rejects placeholders there.)
		args := make([]any, 0, 2*len(chunk))
		for _, id := range chunk {
			args = append(args, id)
		}
		for _, id := range chunk {
			args = append(args, id)
		}

		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		query := `SELECT from_obs_id AS id, COUNT(*) AS cnt, 0 AS side
			FROM edges
			WHERE from_obs_id IN (` + placeholders + `)
			GROUP BY from_obs_id
			UNION ALL
			SELECT to_obs_id, COUNT(*), 1
			FROM edges
			WHERE to_obs_id IN (` + placeholders + `) AND from_obs_id <> to_obs_id
			GROUP BY to_obs_id`

		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("graph: count edges by observation ids: %w", err)
		}
		for rows.Next() {
			var id, cnt int64
			var side int
			if err := rows.Scan(&id, &cnt, &side); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("graph: scan batch edge count: %w", err)
			}
			// Both sides add; the target scan's exclusion predicate already
			// deduplicated self-loops inside SQL.
			out[id] += int(cnt)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("graph: batch edge count rows: %w", err)
		}
		_ = rows.Close()
	}

	return out, nil
}
