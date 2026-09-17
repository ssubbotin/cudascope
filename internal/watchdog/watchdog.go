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
	run(ctx, src, stallAfter, onStall, time.Now, realTicker)
}

// ticker is how the loop is woken. Production passes realTicker; tests pass
// a channel they own, so no test has to sleep out an interval.
type ticker func(time.Duration) (<-chan time.Time, func())

func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

func run(ctx context.Context, src Source, stallAfter time.Duration, onStall func(age time.Duration),
	now func() time.Time, newTicker ticker) {

	if stallAfter <= 0 {
		return
	}

	interval := stallAfter / 4
	if interval < time.Second {
		interval = time.Second
	}
	tick, stop := newTicker(interval)
	defer stop()

	w := newWatcher(stallAfter)
	ts, err := src()
	w.observe(ts, err, now())

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
		}

		ts, err := src()
		if age, stalled := w.observe(ts, err, now()); stalled {
			onStall(age)
			return
		}
	}
}

// watcher is everything the loop remembers between readings. Holding the
// decision here rather than inside the loop is what lets it be judged
// without a clock: the tests feed it readings and a time, and no test waits
// out an interval to find out what it would have done.
type watcher struct {
	stallAfter time.Duration

	// Anything at or below this was written before we started. -1 means no
	// reading has succeeded yet, so there is no baseline.
	baseline int64

	// collected records that a row of ours has been seen fresh. Until then
	// an old row is somebody else's, and no age of it means anything.
	collected bool
}

func newWatcher(stallAfter time.Duration) *watcher {
	return &watcher{stallAfter: stallAfter, baseline: -1}
}

// observe folds one reading in. It returns the age of the newest row once
// collection has stopped, and false at every other moment, including every
// moment before this process has collected anything of its own.
func (w *watcher) observe(ts int64, err error, now time.Time) (time.Duration, bool) {
	if err != nil {
		log.Printf("watchdog: read latest metric: %v", err)
		return 0, false
	}

	if w.baseline < 0 {
		w.baseline = ts
		return 0, false
	}
	if ts <= w.baseline {
		// Nothing of ours in the store yet.
		return 0, false
	}

	age := now.Sub(time.Unix(ts, 0))
	if age <= w.stallAfter {
		w.collected = true
		return 0, false
	}
	if !w.collected {
		return 0, false
	}
	return age, true
}
