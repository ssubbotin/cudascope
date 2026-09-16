// Package watchdog notices when metric collection has stopped.
package watchdog

import (
	"context"
	"log"
	"time"
)

// Source reports when the newest metric row was written, or 0 when there is
// none.
type Source interface {
	LatestGPUMetricTs() (int64, error)
}

// Run watches collection and calls onStall once it has stopped, then
// returns. A zero stallAfter disables the watchdog.
//
// It only ever fires after collection has worked at least once in this
// process. A process that never managed to collect is failing for a reason
// restarting will not fix — no GPU, a driver mismatch after an upgrade, a
// database it cannot write — and the store still holds rows from previous
// runs, which would otherwise read as a stall the moment it starts.
func Run(ctx context.Context, src Source, stallAfter time.Duration, onStall func(age time.Duration)) {
	if stallAfter <= 0 {
		return
	}

	interval := stallAfter / 4
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	collected := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		ts, err := src.LatestGPUMetricTs()
		if err != nil {
			log.Printf("watchdog: read latest metric: %v", err)
			continue
		}
		if ts == 0 {
			continue
		}

		age := time.Since(time.Unix(ts, 0))
		if age <= stallAfter {
			collected = true
			continue
		}
		if !collected {
			continue
		}

		onStall(age)
		return
	}
}
