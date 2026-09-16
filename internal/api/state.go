package api

import (
	"context"
	"log"
	"time"

	"github.com/sergey/cudascope/internal/alerts"
	"github.com/sergey/cudascope/internal/collector"
)

const (
	// defaultStateInterval is how often the state snapshot goes out even
	// when nothing has changed, so a tab that connects between changes does
	// not sit on stale lists until the next one.
	defaultStateInterval = 15 * time.Second

	// stateDebounce bounds how often a burst of changes reaches the wire.
	stateDebounce = time.Second
)

// StateSnapshot is what an open dashboard needs beyond the metric stream:
// which nodes and devices exist, and what is alerting. It used to arrive
// once, in the single /api/v1/status call a page made at mount, which is
// why a node could die and stay green in an open tab.
type StateSnapshot struct {
	Type      string                `json:"type"`
	Timestamp int64                 `json:"ts"`
	Nodes     []collector.Node      `json:"nodes"`
	Devices   []collector.GPUDevice `json:"devices"`
	Alerts    []alerts.Event        `json:"alerts"`
}

// notifyState asks the broadcaster for a snapshot. It never blocks: a
// request already pending covers this change too.
func (s *Server) notifyState() {
	select {
	case s.stateTrigger <- struct{}{}:
	default:
	}
}

// RunStateBroadcast pushes state to connected dashboards when it changes
// and periodically regardless. Blocks until ctx is cancelled.
func (s *Server) RunStateBroadcast(ctx context.Context) {
	ticker := time.NewTicker(s.stateInterval)
	defer ticker.Stop()

	s.broadcastState()
	last := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.stateTrigger:
			// Hold a burst together rather than sending a snapshot per
			// transition: eight GPUs crossing a threshold on the same tick
			// are one change as far as a dashboard is concerned.
			if wait := stateDebounce - time.Since(last); wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}

		last = time.Now()
		s.broadcastState()
	}
}

// broadcastState assembles and sends one snapshot.
func (s *Server) broadcastState() {
	if s.hub == nil || s.hub.ClientCount() == 0 {
		return
	}

	nodes, err := s.store.GetNodes()
	if err != nil {
		log.Printf("state: get nodes: %v", err)
		return
	}
	devices, err := s.store.GetGPUDevices("")
	if err != nil {
		log.Printf("state: get devices: %v", err)
		return
	}

	if nodes == nil {
		nodes = []collector.Node{}
	}
	if devices == nil {
		devices = []collector.GPUDevice{}
	}

	s.hub.BroadcastState(StateSnapshot{
		Type:      "state",
		Timestamp: time.Now().Unix(),
		Nodes:     nodes,
		Devices:   devices,
		Alerts:    s.openAlerts(),
	})
}
