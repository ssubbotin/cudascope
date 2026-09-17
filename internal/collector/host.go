package collector

import (
	"log"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// HostCollector reads system-level metrics.
type HostCollector struct {
	nodeID    string
	prevNetRx uint64
	prevNetTx uint64
	prevNetTs time.Time
	firstRead bool

	// netReset keeps a counter reset from being logged on every tick.
	netReset bool
}

// NewHostCollector creates a new host metric collector.
func NewHostCollector(nodeID string) *HostCollector {
	return &HostCollector{
		nodeID:    nodeID,
		firstRead: true,
	}
}

// netRate turns two counter readings into bytes per second. It reports
// false when the counter went backwards, which happens on an interface
// reset, a namespace change or a driver reload: the difference is unsigned,
// so it wraps to something near 1.8e19 and the stored rate becomes a number
// nothing ever transferred.
func netRate(current, previous uint64, elapsed float64) (uint64, bool) {
	if elapsed <= 0 || current < previous {
		return 0, false
	}
	return uint64(float64(current-previous) / elapsed), true
}

// Collect reads a single host metrics snapshot.
func (hc *HostCollector) Collect() (*HostMetrics, error) {
	now := time.Now()

	m := &HostMetrics{
		Timestamp: now.Unix(),
		NodeID:    hc.nodeID,
	}

	// CPU
	cpuPcts, err := cpu.Percent(0, false)
	if err == nil && len(cpuPcts) > 0 {
		m.CPUPercent = cpuPcts[0]
	}

	// Memory
	vm, err := mem.VirtualMemory()
	if err == nil {
		m.MemUsed = vm.Used
		m.MemTotal = vm.Total
	}

	// Disk (root partition)
	du, err := disk.Usage("/")
	if err == nil {
		m.DiskUsed = du.Used
		m.DiskTotal = du.Total
	}

	// Load average
	la, err := load.Avg()
	if err == nil {
		m.Load1m = la.Load1
		m.Load5m = la.Load5
		m.Load15m = la.Load15
	}

	// Network (calculate delta bytes/sec)
	counters, err := net.IOCounters(false) // combined
	if err == nil && len(counters) > 0 {
		totalRx := counters[0].BytesRecv
		totalTx := counters[0].BytesSent

		if !hc.firstRead {
			elapsed := now.Sub(hc.prevNetTs).Seconds()
			rx, rxOK := netRate(totalRx, hc.prevNetRx, elapsed)
			tx, txOK := netRate(totalTx, hc.prevNetTx, elapsed)
			if rxOK && txOK {
				m.NetRx = rx
				m.NetTx = tx
			} else if !hc.netReset {
				hc.netReset = true
				log.Printf("host network counters went backwards, skipping the rate for this tick")
			}
			if rxOK && txOK {
				hc.netReset = false
			}
		}

		hc.prevNetRx = totalRx
		hc.prevNetTx = totalTx
		hc.prevNetTs = now
		hc.firstRead = false
	}

	return m, nil
}
