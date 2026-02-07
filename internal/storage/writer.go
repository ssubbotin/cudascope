package storage

import (
	"fmt"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

// WriteGPUMetrics batch-inserts GPU metrics.
func (db *DB) WriteGPUMetrics(metrics []collector.GPUMetrics) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO gpu_metrics_raw
		(ts, gpu_id, gpu_util, mem_util, mem_used, temperature, fan_speed,
		 power_draw, power_limit, clock_gfx, clock_mem, pcie_tx, pcie_rx,
		 pstate, encoder_util, decoder_util)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, m := range metrics {
		_, err := stmt.Exec(
			m.Timestamp, m.GPUID, m.GPUUtil, m.MemUtil, m.MemUsed,
			m.Temperature, m.FanSpeed, m.PowerDraw, m.PowerLimit,
			m.ClockGfx, m.ClockMem, m.PCIeTx, m.PCIeRx,
			m.PState, m.EncoderUtil, m.DecoderUtil,
		)
		if err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}

	return tx.Commit()
}

// WriteHostMetrics inserts a host metrics snapshot.
func (db *DB) WriteHostMetrics(m *collector.HostMetrics) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.conn.Exec(`INSERT INTO host_metrics_raw
		(ts, node_id, cpu_percent, mem_used, mem_total, disk_used, disk_total,
		 net_rx, net_tx, load_1m, load_5m, load_15m)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Timestamp, m.NodeID, m.CPUPercent, m.MemUsed, m.MemTotal,
		m.DiskUsed, m.DiskTotal, m.NetRx, m.NetTx,
		m.Load1m, m.Load5m, m.Load15m,
	)
	return err
}

// WriteGPUProcesses inserts a GPU process snapshot.
func (db *DB) WriteGPUProcesses(procs []collector.GPUProcess) error {
	if len(procs) == 0 {
		return nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO gpu_processes (ts, gpu_id, pid, name, gpu_mem) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, p := range procs {
		if _, err := stmt.Exec(p.Timestamp, p.GPUID, p.PID, p.Name, p.GPUMem); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// RegisterGPUDevices upserts GPU device info.
func (db *DB) RegisterGPUDevices(devices []collector.GPUDevice) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	now := time.Now().Unix()
	for _, d := range devices {
		_, err := db.conn.Exec(`INSERT INTO gpu_devices (id, uuid, name, mem_total, driver_ver, first_seen, node_id)
			VALUES (?, ?, ?, ?, ?, ?, 'local')
			ON CONFLICT(uuid) DO UPDATE SET name=excluded.name, mem_total=excluded.mem_total, driver_ver=excluded.driver_ver`,
			d.ID, d.UUID, d.Name, d.MemTotal, d.DriverVer, now,
		)
		if err != nil {
			return fmt.Errorf("register device %d: %w", d.ID, err)
		}
	}
	return nil
}
