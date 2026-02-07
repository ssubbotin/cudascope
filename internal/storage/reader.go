package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/sergey/cudascope/internal/collector"
)

// GPUMetricsQuery defines a time-range query.
type GPUMetricsQuery struct {
	GPUID  int
	NodeID string // empty = all nodes
	From   int64  // unix seconds
	To     int64
}

// GetNodes returns all registered nodes with online status.
func (db *DB) GetNodes() ([]collector.Node, error) {
	rows, err := db.conn.Query("SELECT node_id, hostname, gpu_count, first_seen, last_seen FROM nodes ORDER BY node_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now().Unix()
	var nodes []collector.Node
	for rows.Next() {
		var n collector.Node
		if err := rows.Scan(&n.NodeID, &n.Hostname, &n.GPUCount, &n.FirstSeen, &n.LastSeen); err != nil {
			return nil, err
		}
		// Node is online if seen within last 60 seconds
		n.Online = (now - n.LastSeen) < 60
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// GetGPUDevices returns all registered GPU devices, optionally filtered by node.
func (db *DB) GetGPUDevices(nodeID string) ([]collector.GPUDevice, error) {
	var query string
	var args []any
	if nodeID != "" {
		query = "SELECT node_id, gpu_id, uuid, name, mem_total, driver_ver FROM gpu_devices WHERE node_id = ? ORDER BY gpu_id"
		args = []any{nodeID}
	} else {
		query = "SELECT node_id, gpu_id, uuid, name, mem_total, driver_ver FROM gpu_devices ORDER BY node_id, gpu_id"
	}

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []collector.GPUDevice
	for rows.Next() {
		var d collector.GPUDevice
		if err := rows.Scan(&d.NodeID, &d.ID, &d.UUID, &d.Name, &d.MemTotal, &d.DriverVer); err != nil {
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

	var query string
	var args []any
	if q.NodeID != "" {
		query = fmt.Sprintf("SELECT %s FROM %s WHERE node_id = ? AND gpu_id = ? AND ts >= ? AND ts <= ? ORDER BY ts", cols, table)
		args = []any{q.NodeID, q.GPUID, q.From, q.To}
	} else {
		query = fmt.Sprintf("SELECT %s FROM %s WHERE gpu_id = ? AND ts >= ? AND ts <= ? ORDER BY ts", cols, table)
		args = []any{q.GPUID, q.From, q.To}
	}

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanGPUMetrics(rows)
}

// selectResolution picks the appropriate table based on time span.
func selectResolution(spanSec int64) (table, cols string) {
	switch {
	case spanSec <= 3600: // <=1h: raw data
		return "gpu_metrics_raw",
			"ts, COALESCE(node_id, 'local'), gpu_id, gpu_util, mem_util, mem_used, temperature, fan_speed, power_draw, power_limit, clock_gfx, clock_mem, pcie_tx, pcie_rx, pstate, encoder_util, decoder_util"
	case spanSec <= 86400: // <=24h: 1m rollup
		return "gpu_metrics_1m",
			"ts, COALESCE(node_id, 'local'), gpu_id, gpu_util_avg, mem_util_avg, mem_used_avg, temperature_avg, fan_speed_avg, power_draw_avg, 0, clock_gfx_avg, clock_mem_avg, pcie_tx_avg, pcie_rx_avg, 0, 0, 0"
	default: // >24h: 1h rollup
		return "gpu_metrics_1h",
			"ts, COALESCE(node_id, 'local'), gpu_id, gpu_util_avg, mem_util_avg, mem_used_avg, temperature_avg, 0, power_draw_avg, 0, 0, 0, 0, 0, 0, 0, 0"
	}
}

func scanGPUMetrics(rows *sql.Rows) ([]collector.GPUMetrics, error) {
	var metrics []collector.GPUMetrics
	for rows.Next() {
		var m collector.GPUMetrics

		err := rows.Scan(
			&m.Timestamp, &m.NodeID, &m.GPUID, &m.GPUUtil, &m.MemUtil, &m.MemUsed,
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

// GetHostMetrics returns host metrics for a time range, optionally filtered by node.
func (db *DB) GetHostMetrics(from, to int64, nodeID string) ([]collector.HostMetrics, error) {
	span := to - from
	table, cols := selectHostResolution(span)

	var query string
	var args []any
	if nodeID != "" {
		query = fmt.Sprintf("SELECT %s FROM %s WHERE node_id = ? AND ts >= ? AND ts <= ? ORDER BY ts", cols, table)
		args = []any{nodeID, from, to}
	} else {
		query = fmt.Sprintf("SELECT %s FROM %s WHERE ts >= ? AND ts <= ? ORDER BY ts", cols, table)
		args = []any{from, to}
	}

	rows, err := db.conn.Query(query, args...)
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

func selectHostResolution(spanSec int64) (table, cols string) {
	switch {
	case spanSec <= 3600:
		return "host_metrics_raw",
			"ts, node_id, cpu_percent, mem_used, mem_total, disk_used, disk_total, net_rx, net_tx, load_1m, load_5m, load_15m"
	case spanSec <= 86400:
		return "host_metrics_1m",
			"ts, node_id, cpu_percent_avg, mem_used_avg, mem_total, disk_used, disk_total, net_rx_avg, net_tx_avg, load_1m_avg, 0, 0"
	default:
		return "host_metrics_1h",
			"ts, node_id, cpu_percent_avg, mem_used_avg, mem_total, 0, 0, 0, 0, load_1m_avg, 0, 0"
	}
}

// GetGPUProcesses returns current GPU processes (latest snapshot), optionally filtered by node.
func (db *DB) GetGPUProcesses(gpuID int, nodeID string) ([]collector.GPUProcess, error) {
	cutoff := time.Now().Unix() - 30

	var query string
	var args []any
	if nodeID != "" {
		query = `SELECT ts, COALESCE(node_id, 'local'), gpu_id, pid, name, gpu_mem FROM gpu_processes
			WHERE gpu_id = ? AND COALESCE(node_id, 'local') = ? AND ts >= ?
			AND ts = (SELECT MAX(ts) FROM gpu_processes WHERE gpu_id = ? AND COALESCE(node_id, 'local') = ?)`
		args = []any{gpuID, nodeID, cutoff, gpuID, nodeID}
	} else {
		query = `SELECT ts, COALESCE(node_id, 'local'), gpu_id, pid, name, gpu_mem FROM gpu_processes
			WHERE gpu_id = ? AND ts >= ?
			AND ts = (SELECT MAX(ts) FROM gpu_processes WHERE gpu_id = ?)`
		args = []any{gpuID, cutoff, gpuID}
	}

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var procs []collector.GPUProcess
	for rows.Next() {
		var p collector.GPUProcess
		if err := rows.Scan(&p.Timestamp, &p.NodeID, &p.GPUID, &p.PID, &p.Name, &p.GPUMem); err != nil {
			return nil, err
		}
		procs = append(procs, p)
	}
	return procs, rows.Err()
}

// GetLatestGPUMetrics returns the most recent metric for each GPU across all nodes.
func (db *DB) GetLatestGPUMetrics() ([]collector.GPUMetrics, error) {
	cutoff := time.Now().Unix() - 30
	rows, err := db.conn.Query(`
		WITH latest AS (
			SELECT ts, COALESCE(node_id, 'local') as node_id, gpu_id, gpu_util, mem_util, mem_used,
				temperature, fan_speed, power_draw, power_limit, clock_gfx, clock_mem,
				pcie_tx, pcie_rx, pstate, encoder_util, decoder_util,
				ROW_NUMBER() OVER (PARTITION BY COALESCE(node_id, 'local'), gpu_id ORDER BY ts DESC) as rn
			FROM gpu_metrics_raw
			WHERE ts >= ?
		)
		SELECT ts, node_id, gpu_id, gpu_util, mem_util, mem_used,
			temperature, fan_speed, power_draw, power_limit, clock_gfx, clock_mem,
			pcie_tx, pcie_rx, pstate, encoder_util, decoder_util
		FROM latest WHERE rn = 1 ORDER BY node_id, gpu_id`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []collector.GPUMetrics
	for rows.Next() {
		var m collector.GPUMetrics
		err := rows.Scan(&m.Timestamp, &m.NodeID, &m.GPUID, &m.GPUUtil, &m.MemUtil, &m.MemUsed,
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

// GetLatestHostMetrics returns the most recent host metrics (one per node).
func (db *DB) GetLatestHostMetrics() ([]collector.HostMetrics, error) {
	cutoff := time.Now().Unix() - 30
	rows, err := db.conn.Query(`
		WITH latest AS (
			SELECT ts, node_id, cpu_percent, mem_used, mem_total,
				disk_used, disk_total, net_rx, net_tx, load_1m, load_5m, load_15m,
				ROW_NUMBER() OVER (PARTITION BY node_id ORDER BY ts DESC) as rn
			FROM host_metrics_raw
			WHERE ts >= ?
		)
		SELECT ts, node_id, cpu_percent, mem_used, mem_total,
			disk_used, disk_total, net_rx, net_tx, load_1m, load_5m, load_15m
		FROM latest WHERE rn = 1 ORDER BY node_id`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []collector.HostMetrics
	for rows.Next() {
		var m collector.HostMetrics
		err := rows.Scan(&m.Timestamp, &m.NodeID, &m.CPUPercent, &m.MemUsed, &m.MemTotal,
			&m.DiskUsed, &m.DiskTotal, &m.NetRx, &m.NetTx,
			&m.Load1m, &m.Load5m, &m.Load15m)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}

// GetAllGPUProcesses returns the latest process snapshot across all GPUs and nodes.
func (db *DB) GetAllGPUProcesses() ([]collector.GPUProcess, error) {
	cutoff := time.Now().Unix() - 30

	rows, err := db.conn.Query(`
		WITH latest AS (
			SELECT ts, COALESCE(node_id, 'local') as node_id, gpu_id, pid, name, gpu_mem,
				ROW_NUMBER() OVER (PARTITION BY COALESCE(node_id, 'local'), gpu_id, pid ORDER BY ts DESC) as rn
			FROM gpu_processes
			WHERE ts >= ?
		)
		SELECT ts, node_id, gpu_id, pid, name, gpu_mem
		FROM latest WHERE rn = 1 ORDER BY node_id, gpu_id, pid`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var procs []collector.GPUProcess
	for rows.Next() {
		var p collector.GPUProcess
		if err := rows.Scan(&p.Timestamp, &p.NodeID, &p.GPUID, &p.PID, &p.Name, &p.GPUMem); err != nil {
			return nil, err
		}
		procs = append(procs, p)
	}
	return procs, rows.Err()
}
