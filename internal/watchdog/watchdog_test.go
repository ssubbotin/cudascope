package watchdog

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSource struct {
	ts  atomic.Int64
	err atomic.Value // error
}

func (f *fakeSource) LatestGPUMetricTs() (int64, error) {
	if e, ok := f.err.Load().(error); ok && e != nil {
		return 0, e
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

// Collection that worked and then stopped is the case the watchdog exists
// for: on 2026-09-16 the collector wedged and the process went on serving
// stale data for an hour.
// Timestamps carry whole-second precision, so the intervals here are kept
// well above a second; the production default is five minutes.
func TestFiresOnceCollectionStops(t *testing.T) {
	src := &fakeSource{}
	src.ts.Store(time.Now().Unix())

	var fired atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, src, 2*time.Second, func(time.Duration) { fired.Store(true) })

	// Keep the source fresh for a while: the watchdog must stay quiet.
	for i := 0; i < 13; i++ {
		src.ts.Store(time.Now().Unix())
		time.Sleep(100 * time.Millisecond)
	}
	if fired.Load() {
		t.Fatal("fired while collection was healthy")
	}

	// Stop updating; the row ages out.
	if !waitFor(fired.Load, 8*time.Second) {
		t.Error("did not fire after collection stopped")
	}
}

// A process that has never collected must not be restarted: the store still
// holds rows from earlier runs, and the reason it cannot collect is not one
// a restart fixes.
func TestDoesNotFireWhenCollectionNeverWorked(t *testing.T) {
	src := &fakeSource{}
	src.ts.Store(time.Now().Add(-time.Hour).Unix())

	var fired atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, src, 2*time.Second, func(time.Duration) { fired.Store(true) })

	time.Sleep(4 * time.Second)
	if fired.Load() {
		t.Error("fired although this process never collected anything")
	}
}

// An empty store is the same case: nothing has been collected yet.
func TestDoesNotFireOnEmptyStore(t *testing.T) {
	src := &fakeSource{}

	var fired atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, src, 2*time.Second, func(time.Duration) { fired.Store(true) })

	time.Sleep(4 * time.Second)
	if fired.Load() {
		t.Error("fired on an empty store")
	}
}

func TestDisabledByZero(t *testing.T) {
	src := &fakeSource{}
	src.ts.Store(time.Now().Add(-time.Hour).Unix())

	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(context.Background(), src, 0, func(time.Duration) {
			t.Error("fired although the watchdog is disabled")
		})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("Run did not return immediately when disabled")
	}
}
