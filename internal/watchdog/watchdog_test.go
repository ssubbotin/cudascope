package watchdog

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// No test here sleeps. The decision the watchdog makes lives in
// watcher.observe, which takes a reading and the time it was taken at, so an
// afternoon of collection is a handful of calls; the loop around it is
// driven through a tick channel the test owns. Waiting out real intervals
// cost twenty three seconds a run and left
// TestRecoversWhenTheBaselineReadingFails failing about one run in three:
// timestamps are whole seconds, and whether a row counted as ours came down
// to where the second boundary happened to fall.

// at is a fixed moment to measure from, so every timestamp in a test reads
// as an offset from one place.
var at = time.Unix(1_700_000_000, 0)

// secondsAgo is a row written that many seconds before at.
func secondsAgo(n int64) int64 { return at.Add(-time.Duration(n) * time.Second).Unix() }

const stallAfter = 2 * time.Second

var unreachable = errors.New("store unavailable")

// fakeSource stands in for the store. The loop tests change ts between
// passes, which is safe because the loop reads it only when handed a tick.
type fakeSource struct {
	ts    atomic.Int64
	calls atomic.Int64
}

func newFakeSource(ts int64) *fakeSource {
	f := &fakeSource{}
	f.ts.Store(ts)
	return f
}

func (f *fakeSource) read() (int64, error) {
	f.calls.Add(1)
	return f.ts.Load(), nil
}

// Collection that worked and then stopped is the case the watchdog exists
// for: on 2026-09-16 the collector wedged and the process went on serving
// stale data for an hour.
func TestFiresOnceCollectionStops(t *testing.T) {
	w := newWatcher(stallAfter)

	// A row from an earlier run becomes the baseline.
	mustStayQuiet(t, w, secondsAgo(10), at)

	// This process collects: rows newer than the baseline, read fresh.
	for i := int64(5); i > 0; i-- {
		mustStayQuiet(t, w, secondsAgo(i), at)
	}

	// Collection stops, and the newest row ages past the threshold.
	age, stalled := w.observe(secondsAgo(1), nil, at.Add(4*time.Second))
	if !stalled {
		t.Fatal("did not fire after collection stopped")
	}
	if age != 5*time.Second {
		t.Fatalf("age = %s, want the 5s since the newest row", age)
	}
}

// Rows an earlier run left behind are not evidence that this process can
// collect. A container that restarts shortly after its last good scrape
// would otherwise adopt that row, fire once it aged out, and restart into
// the same failure again.
func TestDoesNotFireOnRowsFromAnEarlierRun(t *testing.T) {
	w := newWatcher(stallAfter)

	// Fresh, but written before we started: a baseline, not ours.
	mustStayQuiet(t, w, secondsAgo(0), at)

	// However long it ages, it stays somebody else's row.
	for _, after := range []time.Duration{time.Second, 10 * time.Second, time.Hour} {
		mustStayQuiet(t, w, secondsAgo(0), at.Add(after))
	}
}

// The same holds for an old row: this process has still collected nothing.
func TestDoesNotFireWhenCollectionNeverWorked(t *testing.T) {
	w := newWatcher(stallAfter)

	mustStayQuiet(t, w, secondsAgo(3600), at)
	mustStayQuiet(t, w, secondsAgo(3600), at.Add(time.Hour))
}

// An empty store is the same case: nothing has been collected yet.
func TestDoesNotFireOnEmptyStore(t *testing.T) {
	w := newWatcher(stallAfter)

	for _, after := range []time.Duration{0, time.Second, time.Hour} {
		mustStayQuiet(t, w, 0, at.Add(after))
	}
}

// A store that cannot be read at startup must not leave the watchdog
// disarmed: the first reading that works becomes the baseline.
func TestRecoversWhenTheBaselineReadingFails(t *testing.T) {
	w := newWatcher(stallAfter)

	// A failed reading leaves no baseline behind.
	mustStayQuietOnError(t, w, at)
	mustStayQuietOnError(t, w, at.Add(time.Second))
	if w.baseline >= 0 {
		t.Fatalf("a failed reading set a baseline of %d", w.baseline)
	}

	// The first reading that works becomes the baseline.
	mustStayQuiet(t, w, secondsAgo(10), at.Add(2*time.Second))

	// A row this process wrote, read while it is still fresh: the watchdog
	// is armed from here on.
	ours := at.Add(2 * time.Second).Unix()
	mustStayQuiet(t, w, ours, at.Add(2*time.Second))

	// Collection stops and that row ages past the threshold.
	if _, stalled := w.observe(ours, nil, at.Add(10*time.Second)); !stalled {
		t.Error("did not fire after collection stopped")
	}
}

// A reading that fails while the watchdog is armed is not a stall: the store
// is unreachable, which says nothing about whether the collector is running.
func TestAFailedReadingIsNotAStall(t *testing.T) {
	w := newWatcher(stallAfter)

	mustStayQuiet(t, w, secondsAgo(10), at)
	mustStayQuiet(t, w, secondsAgo(0), at)
	mustStayQuietOnError(t, w, at.Add(time.Hour))
}

// The loop wiring: a tick that finds collection stalled calls onStall, and
// Run returns.
//
// The clock is the synchronisation point. The loop asks for the time once
// per pass, right after reading the store, so handing it a time both starts
// that pass and proves the previous one finished. Nothing here waits on a
// timer, and nothing depends on how two goroutines happen to interleave.
func TestTheLoopFiresAndReturns(t *testing.T) {
	src := newFakeSource(secondsAgo(10)) // from an earlier run: the baseline
	times := make(chan time.Time)
	tick := make(chan time.Time)
	fired := make(chan time.Duration, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		run(context.Background(), src.read, stallAfter,
			func(age time.Duration) { fired <- age },
			func() time.Time { return <-times },
			func(time.Duration) (<-chan time.Time, func()) { return tick, func() {} })
	}()

	// The pass before the loop: the row already in the store is the baseline.
	times <- at

	// A row of ours, read while it is fresh: the watchdog arms.
	src.ts.Store(secondsAgo(0))
	tick <- at
	times <- at

	// The same row, four seconds on: past the threshold, so it fires.
	tick <- at
	times <- at.Add(4 * time.Second)

	if age := <-fired; age != 4*time.Second {
		t.Errorf("age = %s, want 4s", age)
	}
	<-done
}

// Cancelling the context stops the loop.
func TestTheLoopStopsWhenCancelled(t *testing.T) {
	src := newFakeSource(secondsAgo(10))
	times := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		run(ctx, src.read, stallAfter,
			func(time.Duration) { t.Error("fired although nothing had been collected") },
			func() time.Time { return <-times },
			func(time.Duration) (<-chan time.Time, func()) { return make(chan time.Time), func() {} })
	}()

	times <- at // the baseline pass is done, so the loop is at its select
	cancel()
	<-done
}

func TestDisabledByZero(t *testing.T) {
	src := newFakeSource(secondsAgo(3600))

	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(context.Background(), src.read, 0, func(time.Duration) {
			t.Error("fired although the watchdog is disabled")
		})
	}()

	<-done
	if src.calls.Load() != 0 {
		t.Errorf("read the store %d time(s) while disabled", src.calls.Load())
	}
}

// mustStayQuiet folds in a reading that succeeded and fails the test if the
// watchdog fired on it.
func mustStayQuiet(t *testing.T, w *watcher, ts int64, now time.Time) {
	t.Helper()
	if age, stalled := w.observe(ts, nil, now); stalled {
		t.Fatalf("fired on the row at %d, read at %s: age %s", ts, now.UTC(), age)
	}
}

// mustStayQuietOnError does the same for a reading that failed.
func mustStayQuietOnError(t *testing.T, w *watcher, now time.Time) {
	t.Helper()
	if _, stalled := w.observe(0, unreachable, now); stalled {
		t.Fatalf("fired on a failed reading at %s", now.UTC())
	}
}
