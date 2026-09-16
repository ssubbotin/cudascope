// Package watchdog notices when metric collection has stopped.
package watchdog

import (
	"context"
	"log"
	"time"
)

// Source reports the timestamp of the newest metric row, or 0 when there is
// none.
type Source func() (int64, error)

// Run watches collection and calls onStall once it has stopped, then
// returns. A zero stallAfter disables the watchdog.
//
// It only fires after this process has written a row of its own. The store
// keeps what earlier runs wrote, so freshness on its own proves nothing: a
// container restarting a minute after its last successful scrape would
// otherwise count that row as evidence that collection works, and then kill
// itself once it aged out — in a loop, for a failure a restart cannot fix,
// such as a driver mismatch after an upgrade.
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

	// Anything at or below this was written before we started. -1 means the
	// reading failed and no baseline has been established yet.
	baseline, err := src()
	if err != nil {
		log.Printf("watchdog: read latest metric: %v", err)
		baseline = -1
	}

	collected := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		ts, err := src()
		if err != nil {
			log.Printf("watchdog: read latest metric: %v", err)
			continue
		}

		if baseline < 0 {
			baseline = ts
			continue
		}
		if ts <= baseline {
			// Nothing of ours in the store yet.
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
