package collector

import (
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// HostCollector reads system-level metrics.
type HostCollector struct {
	nodeID     string
	prevNetRx  uint64
	prevNetTx  uint64
	prevNetTs  time.Time
	firstRead  bool
}

// NewHostCollector creates a new host metric collector.
func NewHostCollector(nodeID string) *HostCollector {
	return &HostCollector{
		nodeID:    nodeID,
		firstRead: true,
	}
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
			if elapsed > 0 {
				m.NetRx = uint64(float64(totalRx-hc.prevNetRx) / elapsed)
				m.NetTx = uint64(float64(totalTx-hc.prevNetTx) / elapsed)
			}
		}

		hc.prevNetRx = totalRx
		hc.prevNetTx = totalTx
		hc.prevNetTs = now
		hc.firstRead = false
	}

	return m, nil
}
