package collector

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// stuckGPU never returns from Collect. NVML calls are cgo calls with no
// timeout, so a wedged driver blocks the caller indefinitely.
type stuckGPU struct{ release chan struct{} }

func (s stuckGPU) Collect() []GPUMetrics          { <-s.release; return nil }
func (s stuckGPU) CollectProcesses() []GPUProcess { <-s.release; return nil }

type countingHost struct{ n atomic.Int64 }

func (h *countingHost) Collect() (*HostMetrics, error) {
	h.n.Add(1)
	return &HostMetrics{NodeID: "local", Timestamp: time.Now().Unix()}, nil
}

type countingVLLM struct{ n atomic.Int64 }

func (v *countingVLLM) Collect() (*VLLMMetrics, error) {
	v.n.Add(1)
	return &VLLMMetrics{NodeID: "local", Timestamp: time.Now().Unix()}, nil
}

type nopSink struct{}

func (nopSink) WriteGPUMetrics([]GPUMetrics) error   { return nil }
func (nopSink) WriteHostMetrics(*HostMetrics) error  { return nil }
func (nopSink) WriteGPUProcesses([]GPUProcess) error { return nil }
func (nopSink) WriteVLLMMetrics(*VLLMMetrics) error  { return nil }

func waitForCount(n *atomic.Int64, want int64, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if n.Load() >= want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// One source hanging must not silence the others. Driving all three from a
// single select loop meant a blocked call stopped every metric at once,
// which is how GPU, host and vLLM series all ended on the same second.
func TestStuckSourceDoesNotStopTheOthers(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	host := &countingHost{}
	vllm := &countingVLLM{}

	c := &Collector{
		gpu:          stuckGPU{release: release},
		host:         host,
		storage:      nopSink{},
		gpuInterval:  10 * time.Millisecond,
		hostInterval: 10 * time.Millisecond,
	}
	c.SetVLLM(vllm, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	if !waitForCount(&host.n, 5, 3*time.Second) {
		t.Fatalf("host collection stalled behind the stuck GPU source: %d ticks", host.n.Load())
	}
	if !waitForCount(&vllm.n, 5, 3*time.Second) {
		t.Fatalf("vLLM collection stalled behind the stuck GPU source: %d ticks", vllm.n.Load())
	}
}

// Cancelling the context must stop every loop.
func TestRunStopsOnContextCancel(t *testing.T) {
	host := &countingHost{}
	c := &Collector{
		gpu:          stuckGPU{release: make(chan struct{})},
		host:         host,
		storage:      nopSink{},
		gpuInterval:  time.Hour,
		hostInterval: 10 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)

	if !waitForCount(&host.n, 2, 2*time.Second) {
		t.Fatal("host loop never ran")
	}
	cancel()

	settled := host.n.Load()
	time.Sleep(200 * time.Millisecond)
	if got := host.n.Load(); got > settled+1 {
		t.Fatalf("host loop kept running after cancel: %d -> %d", settled, got)
	}
}
