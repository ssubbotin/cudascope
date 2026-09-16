package watchdog

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSource stands in for the store. ts is whole seconds, as the real one
// is, which is why the intervals below stay well above a second; the
// production default is five minutes.
type fakeSource struct {
	ts     atomic.Int64
	failed atomic.Bool
	calls  atomic.Int64
}

func (f *fakeSource) read() (int64, error) {
	f.calls.Add(1)
	if f.failed.Load() {
		return 0, errors.New("store unavailable")
	}
	return f.ts.Load(), nil
}

func waitFor(cond func() bool, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func runWatchdog(t *testing.T, src *fakeSource, stallAfter time.Duration) *atomic.Bool {
	t.Helper()
	var fired atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go Run(ctx, src.read, stallAfter, func(time.Duration) { fired.Store(true) })
	return &fired
}

// Collection that worked and then stopped is the case the watchdog exists
// for: on 2026-09-16 the collector wedged and the process went on serving
// stale data for an hour.
func TestFiresOnceCollectionStops(t *testing.T) {
	src := &fakeSource{}
	src.ts.Store(time.Now().Add(-10 * time.Second).Unix())

	fired := runWatchdog(t, src, 2*time.Second)

	// Collect for a while: the watchdog must stay quiet.
	for i := 0; i < 13; i++ {
		src.ts.Store(time.Now().Unix())
		time.Sleep(100 * time.Millisecond)
	}
	if fired.Load() {
		t.Fatal("fired while collection was healthy")
	}

	// Stop collecting; the newest row ages out.
	if !waitFor(fired.Load, 8*time.Second) {
		t.Error("did not fire after collection stopped")
	}
}

// Rows an earlier run left behind are not evidence that this process can
// collect. A container that restarts shortly after its last good scrape
// would otherwise adopt that row, fire once it aged out, and restart into
// the same failure again.
func TestDoesNotFireOnRowsFromAnEarlierRun(t *testing.T) {
	src := &fakeSource{}
	src.ts.Store(time.Now().Unix()) // fresh, but written before we started

	fired := runWatchdog(t, src, 2*time.Second)

	time.Sleep(6 * time.Second)
	if fired.Load() {
		t.Error("fired on a row this process never wrote")
	}
}

// The same holds for an old row: this process has still collected nothing.
func TestDoesNotFireWhenCollectionNeverWorked(t *testing.T) {
	src := &fakeSource{}
	src.ts.Store(time.Now().Add(-time.Hour).Unix())

	fired := runWatchdog(t, src, 2*time.Second)

	time.Sleep(4 * time.Second)
	if fired.Load() {
		t.Error("fired although this process never collected anything")
	}
}

// An empty store is the same case: nothing has been collected yet.
func TestDoesNotFireOnEmptyStore(t *testing.T) {
	src := &fakeSource{}

	fired := runWatchdog(t, src, 2*time.Second)

	time.Sleep(4 * time.Second)
	if fired.Load() {
		t.Error("fired on an empty store")
	}
}

// A store that cannot be read at startup must not leave the watchdog
// disarmed: the first reading that works becomes the baseline.
func TestRecoversWhenTheBaselineReadingFails(t *testing.T) {
	src := &fakeSource{}
	src.failed.Store(true)
	src.ts.Store(time.Now().Unix())

	fired := runWatchdog(t, src, 2*time.Second)

	time.Sleep(1500 * time.Millisecond)
	src.failed.Store(false)

	for i := 0; i < 13; i++ {
		src.ts.Store(time.Now().Unix())
		time.Sleep(100 * time.Millisecond)
	}
	if fired.Load() {
		t.Fatal("fired while collection was healthy")
	}

	if !waitFor(fired.Load, 8*time.Second) {
		t.Error("did not fire after collection stopped")
	}
}

func TestDisabledByZero(t *testing.T) {
	src := &fakeSource{}
	src.ts.Store(time.Now().Add(-time.Hour).Unix())

	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(context.Background(), src.read, 0, func(time.Duration) {
			t.Error("fired although the watchdog is disabled")
		})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("Run did not return immediately when disabled")
	}
	if src.calls.Load() != 0 {
		t.Errorf("read the store %d time(s) while disabled", src.calls.Load())
	}
}
