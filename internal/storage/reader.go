package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

// GPUMetricsQuery defines a time-range query.
type GPUMetricsQuery struct {
	GPUID int
	From  int64 // unix seconds
	To    int64
}

// GetGPUDevices returns all registered GPU devices.
func (db *DB) GetGPUDevices() ([]collector.GPUDevice, error) {
	rows, err := db.conn.Query("SELECT id, uuid, name, mem_total, driver_ver FROM gpu_devices ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []collector.GPUDevice
	for rows.Next() {
		var d collector.GPUDevice
		if err := rows.Scan(&d.ID, &d.UUID, &d.Name, &d.MemTotal, &d.DriverVer); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// GetGPUMetrics returns GPU metrics for a time range, auto-selecting resolution.
func (db *DB) GetGPUMetrics(q GPUMetricsQuery) ([]collector.GPUMetrics, error) {
	span := q.To - q.From
	table, cols := selectResolution(span)

	query := fmt.Sprintf("SELECT %s FROM %s WHERE gpu_id = ? AND ts >= ? AND ts <= ? ORDER BY ts", cols, table)
	rows, err := db.conn.Query(query, q.GPUID, q.From, q.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanGPUMetrics(rows, table, q.GPUID)
}

// selectResolution picks the appropriate table based on time span.
func selectResolution(spanSec int64) (table, cols string) {
	switch {
	case spanSec <= 3600: // <=1h: raw data
		return "gpu_metrics_raw",
			"ts, gpu_util, mem_util, mem_used, temperature, fan_speed, power_draw, power_limit, clock_gfx, clock_mem, pcie_tx, pcie_rx, pstate, encoder_util, decoder_util"
	case spanSec <= 86400: // <=24h: 1m rollup
		return "gpu_metrics_1m",
			"ts, gpu_util_avg, mem_util_avg, mem_used_avg, temperature_avg, fan_speed_avg, power_draw_avg, 0, clock_gfx_avg, clock_mem_avg, pcie_tx_avg, pcie_rx_avg, 0, 0, 0"
	default: // >24h: 1h rollup
		return "gpu_metrics_1h",
			"ts, gpu_util_avg, mem_util_avg, mem_used_avg, temperature_avg, 0, power_draw_avg, 0, 0, 0, 0, 0, 0, 0, 0"
	}
}

func scanGPUMetrics(rows *sql.Rows, table string, gpuID int) ([]collector.GPUMetrics, error) {
	var metrics []collector.GPUMetrics
	for rows.Next() {
		var m collector.GPUMetrics
		m.GPUID = gpuID

		err := rows.Scan(
			&m.Timestamp, &m.GPUUtil, &m.MemUtil, &m.MemUsed,
			&m.Temperature, &m.FanSpeed, &m.PowerDraw, &m.PowerLimit,
			&m.ClockGfx, &m.ClockMem, &m.PCIeTx, &m.PCIeRx,
			&m.PState, &m.EncoderUtil, &m.DecoderUtil,
		)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}

// GetHostMetrics returns host metrics for a time range.
func (db *DB) GetHostMetrics(from, to int64) ([]collector.HostMetrics, error) {
	rows, err := db.conn.Query(`SELECT ts, node_id, cpu_percent, mem_used, mem_total,
		disk_used, disk_total, net_rx, net_tx, load_1m, load_5m, load_15m
		FROM host_metrics_raw WHERE ts >= ? AND ts <= ? ORDER BY ts`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []collector.HostMetrics
	for rows.Next() {
		var m collector.HostMetrics
		err := rows.Scan(&m.Timestamp, &m.NodeID, &m.CPUPercent, &m.MemUsed, &m.MemTotal,
			&m.DiskUsed, &m.DiskTotal, &m.NetRx, &m.NetTx, &m.Load1m, &m.Load5m, &m.Load15m)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}

// GetGPUProcesses returns current GPU processes (latest snapshot).
func (db *DB) GetGPUProcesses(gpuID int) ([]collector.GPUProcess, error) {
	// Get the latest timestamp
	var latestTs int64
	err := db.conn.QueryRow("SELECT COALESCE(MAX(ts), 0) FROM gpu_processes WHERE gpu_id = ?", gpuID).Scan(&latestTs)
	if err != nil || latestTs == 0 {
		return nil, err
	}

	rows, err := db.conn.Query("SELECT ts, gpu_id, pid, name, gpu_mem FROM gpu_processes WHERE gpu_id = ? AND ts = ?", gpuID, latestTs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var procs []collector.GPUProcess
	for rows.Next() {
		var p collector.GPUProcess
		if err := rows.Scan(&p.Timestamp, &p.GPUID, &p.PID, &p.Name, &p.GPUMem); err != nil {
			return nil, err
		}
		procs = append(procs, p)
	}
	return procs, rows.Err()
}

// GetLatestGPUMetrics returns the most recent metric for each GPU.
func (db *DB) GetLatestGPUMetrics() ([]collector.GPUMetrics, error) {
	rows, err := db.conn.Query(`SELECT ts, gpu_id, gpu_util, mem_util, mem_used,
		temperature, fan_speed, power_draw, power_limit, clock_gfx, clock_mem,
		pcie_tx, pcie_rx, pstate, encoder_util, decoder_util
		FROM gpu_metrics_raw WHERE ts = (SELECT MAX(ts) FROM gpu_metrics_raw) ORDER BY gpu_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []collector.GPUMetrics
	for rows.Next() {
		var m collector.GPUMetrics
		err := rows.Scan(&m.Timestamp, &m.GPUID, &m.GPUUtil, &m.MemUtil, &m.MemUsed,
			&m.Temperature, &m.FanSpeed, &m.PowerDraw, &m.PowerLimit,
			&m.ClockGfx, &m.ClockMem, &m.PCIeTx, &m.PCIeRx,
			&m.PState, &m.EncoderUtil, &m.DecoderUtil)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}

// GetLatestHostMetrics returns the most recent host metrics.
func (db *DB) GetLatestHostMetrics() (*collector.HostMetrics, error) {
	var m collector.HostMetrics
	err := db.conn.QueryRow(`SELECT ts, node_id, cpu_percent, mem_used, mem_total,
		disk_used, disk_total, net_rx, net_tx, load_1m, load_5m, load_15m
		FROM host_metrics_raw ORDER BY ts DESC LIMIT 1`).Scan(
		&m.Timestamp, &m.NodeID, &m.CPUPercent, &m.MemUsed, &m.MemTotal,
		&m.DiskUsed, &m.DiskTotal, &m.NetRx, &m.NetTx,
		&m.Load1m, &m.Load5m, &m.Load15m)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetAllGPUProcesses returns the latest process snapshot across all GPUs.
func (db *DB) GetAllGPUProcesses() ([]collector.GPUProcess, error) {
	var latestTs int64
	err := db.conn.QueryRow("SELECT COALESCE(MAX(ts), 0) FROM gpu_processes").Scan(&latestTs)
	if err != nil || latestTs == 0 {
		return nil, err
	}

	// Only return if snapshot is fresh (within last 30s)
	if time.Now().Unix()-latestTs > 30 {
		return nil, nil
	}

	rows, err := db.conn.Query("SELECT ts, gpu_id, pid, name, gpu_mem FROM gpu_processes WHERE ts = ?", latestTs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var procs []collector.GPUProcess
	for rows.Next() {
		var p collector.GPUProcess
		if err := rows.Scan(&p.Timestamp, &p.GPUID, &p.PID, &p.Name, &p.GPUMem); err != nil {
			return nil, err
		}
		procs = append(procs, p)
	}
	return procs, rows.Err()
}
