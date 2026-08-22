package vectorhydration

import (
	"fmt"
	"math/rand"
)

type Order string

const (
	OrderAB Order = "AB"
	OrderBA Order = "BA"
)

type ScheduleEntry struct {
	Cell    int    `json:"gomaxprocs"`
	Block   int    `json:"block"`
	Order   Order  `json:"order"`
	BlockID string `json:"block_id"`
	RunID   string `json:"run_id"`
}

// Schedule creates a deterministic, round-robin cell schedule. Each cell's
// twenty paired blocks are independently shuffled from the recorded seed and
// contain ten observations in each order.
func Schedule(seed uint64, campaign, phase, run string) ([]ScheduleEntry, error) {
	if seed == 0 || campaign == "" || phase == "" || run == "" {
		return nil, fmt.Errorf("seed, campaign, phase, and run are required")
	}
	orders := make([][]Order, len(RequiredCells))
	r := rand.New(rand.NewSource(int64(seed)))
	for i := range orders {
		orders[i] = make([]Order, PairedBlocksPerCell)
		for j := range orders[i] {
			if j < 10 {
				orders[i][j] = OrderAB
			} else {
				orders[i][j] = OrderBA
			}
		}
		r.Shuffle(len(orders[i]), func(a, b int) { orders[i][a], orders[i][b] = orders[i][b], orders[i][a] })
	}
	out := make([]ScheduleEntry, 0, len(RequiredCells)*PairedBlocksPerCell)
	for block := 1; block <= PairedBlocksPerCell; block++ {
		for cellIndex, cell := range RequiredCells {
			order := orders[cellIndex][block-1]
			blockID := fmt.Sprintf("%s.%s.%s.c%d.b%02d", campaign, phase, run, cell, block)
			out = append(out, ScheduleEntry{Cell: cell, Block: block, Order: order, BlockID: blockID, RunID: blockID + ".run"})
		}
	}
	return out, nil
}
