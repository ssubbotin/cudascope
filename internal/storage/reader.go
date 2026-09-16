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
	offlineAfter := int64(db.opts.NodeOfflineAfter.Seconds())
	var nodes []collector.Node
	for rows.Next() {
		var n collector.Node
		if err := rows.Scan(&n.NodeID, &n.Hostname, &n.GPUCount, &n.FirstSeen, &n.LastSeen); err != nil {
			return nil, err
		}
		// Online means the heartbeat is younger than the configured silence.
		n.Online = (now - n.LastSeen) < offlineAfter
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// GetGPUDevices returns all registered GPU devices, optionally filtered by node.
func (db *DB) GetGPUDevices(nodeID string) ([]collector.GPUDevice, error) {
	var query string
	var args []any
	if nodeID != "" {
		query = "SELECT node_id, gpu_id, uuid, name, mem_total, driver_ver, ecc_supported, throttle_supported FROM gpu_devices WHERE node_id = ? ORDER BY gpu_id"
		args = []any{nodeID}
	} else {
		query = "SELECT node_id, gpu_id, uuid, name, mem_total, driver_ver, ecc_supported, throttle_supported FROM gpu_devices ORDER BY node_id, gpu_id"
	}

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []collector.GPUDevice
	for rows.Next() {
		var d collector.GPUDevice
		if err := rows.Scan(&d.NodeID, &d.ID, &d.UUID, &d.Name, &d.MemTotal, &d.DriverVer,
			&d.EccSupported, &d.ThrottleSupported); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// GetGPUMetrics returns GPU metrics for a window, at the finest resolution
// that both survives and fits.
func (db *DB) GetGPUMetrics(q GPUMetricsQuery) ([]collector.GPUMetrics, error) {
	tier, bucket := gpuSeries.pick(db.opts, q.From, q.To)

	where := "gpu_id = ? AND ts >= ? AND ts <= ?"
	args := []any{q.GPUID, q.From, q.To}
	if q.NodeID != "" {
		where = "node_id = ? AND " + where
		args = append([]any{q.NodeID}, args...)
	}

	rows, err := db.conn.Query(tier.query(bucket, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanGPUMetrics(rows)
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
			&m.ThrottleReasons, &m.EccCorrected, &m.EccUncorrected,
		)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}

// GetHostMetrics returns host metrics for a window, optionally for one node.
func (db *DB) GetHostMetrics(from, to int64, nodeID string) ([]collector.HostMetrics, error) {
	tier, bucket := hostSeries.pick(db.opts, from, to)

	where := "ts >= ? AND ts <= ?"
	args := []any{from, to}
	if nodeID != "" {
		where = "node_id = ? AND " + where
		args = append([]any{nodeID}, args...)
	}

	rows, err := db.conn.Query(tier.query(bucket, where), args...)
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

// GetGPUProcesses returns current GPU processes (latest snapshot), optionally filtered by node.
func (db *DB) GetGPUProcesses(gpuID int, nodeID string) ([]collector.GPUProcess, error) {
	cutoff := db.freshCutoff()

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
	cutoff := db.freshCutoff()
	rows, err := db.conn.Query(`
		WITH latest AS (
			SELECT ts, COALESCE(node_id, 'local') as node_id, gpu_id, gpu_util, mem_util, mem_used,
				temperature, fan_speed, power_draw, power_limit, clock_gfx, clock_mem,
				pcie_tx, pcie_rx, pstate, encoder_util, decoder_util,
				throttle_reasons, ecc_corrected, ecc_uncorrected,
				ROW_NUMBER() OVER (PARTITION BY COALESCE(node_id, 'local'), gpu_id ORDER BY ts DESC) as rn
			FROM gpu_metrics_raw
			WHERE ts >= ?
		)
		SELECT ts, node_id, gpu_id, gpu_util, mem_util, mem_used,
			temperature, fan_speed, power_draw, power_limit, clock_gfx, clock_mem,
			pcie_tx, pcie_rx, pstate, encoder_util, decoder_util,
			throttle_reasons, ecc_corrected, ecc_uncorrected
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
			&m.PState, &m.EncoderUtil, &m.DecoderUtil,
			&m.ThrottleReasons, &m.EccCorrected, &m.EccUncorrected)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}

// GetLatestHostMetrics returns the most recent host metrics (one per node).
func (db *DB) GetLatestHostMetrics() ([]collector.HostMetrics, error) {
	cutoff := db.freshCutoff()
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

// ReadVLLMMetrics returns vLLM metrics for a window, optionally for one node.
func (db *DB) ReadVLLMMetrics(nodeID string, from, to int64) ([]collector.VLLMMetrics, error) {
	tier, bucket := vllmSeries.pick(db.opts, from, to)

	where := "ts >= ? AND ts <= ?"
	args := []any{from, to}
	if nodeID != "" {
		where = "node_id = ? AND " + where
		args = append([]any{nodeID}, args...)
	}

	rows, err := db.conn.Query(tier.query(bucket, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []collector.VLLMMetrics
	for rows.Next() {
		var m collector.VLLMMetrics
		err := rows.Scan(&m.Timestamp, &m.NodeID, &m.ModelName, &m.RequestsRunning,
			&m.RequestsWaiting, &m.KVCacheUsage, &m.GenerationTokensTotal,
			&m.PromptTokensTotal, &m.TimeToFirstTokenAvg, &m.TimePerOutputTokenAvg,
			&m.TokenThroughput, &m.PrefixCacheHitRate, &m.NumPreemptions)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}

// ReadLatestVLLMMetrics returns the most recent vLLM metrics for a node.
func (db *DB) ReadLatestVLLMMetrics(nodeID string) (*collector.VLLMMetrics, error) {
	cutoff := db.freshCutoff()

	var query string
	var args []any
	if nodeID != "" {
		query = `SELECT ts, node_id, model_name, requests_running, requests_waiting, kv_cache_usage,
			generation_tokens_total, prompt_tokens_total, ttft_avg, tpot_avg,
			token_throughput, prefix_cache_hit_rate, num_preemptions
			FROM vllm_metrics_raw WHERE node_id = ? AND ts >= ? ORDER BY ts DESC LIMIT 1`
		args = []any{nodeID, cutoff}
	} else {
		query = `SELECT ts, node_id, model_name, requests_running, requests_waiting, kv_cache_usage,
			generation_tokens_total, prompt_tokens_total, ttft_avg, tpot_avg,
			token_throughput, prefix_cache_hit_rate, num_preemptions
			FROM vllm_metrics_raw WHERE ts >= ? ORDER BY ts DESC LIMIT 1`
		args = []any{cutoff}
	}

	var m collector.VLLMMetrics
	err := db.conn.QueryRow(query, args...).Scan(
		&m.Timestamp, &m.NodeID, &m.ModelName, &m.RequestsRunning,
		&m.RequestsWaiting, &m.KVCacheUsage, &m.GenerationTokensTotal,
		&m.PromptTokensTotal, &m.TimeToFirstTokenAvg, &m.TimePerOutputTokenAvg,
		&m.TokenThroughput, &m.PrefixCacheHitRate, &m.NumPreemptions)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetAllGPUProcesses returns the newest process snapshot of every GPU on
// every node.
//
// The snapshot is the rows of one collection tick, found per GPU through
// MAX(ts). Taking the newest row of every PID seen inside the freshness
// window instead would keep listing processes that have already exited,
// with their memory still counted against the card, until the window rolled
// past them.
func (db *DB) GetAllGPUProcesses() ([]collector.GPUProcess, error) {
	cutoff := db.freshCutoff()

	rows, err := db.conn.Query(`
		WITH ticks AS (
			SELECT COALESCE(node_id, 'local') AS node_id, gpu_id, MAX(ts) AS ts
			FROM gpu_processes
			WHERE ts >= ?
			GROUP BY COALESCE(node_id, 'local'), gpu_id
		)
		SELECT p.ts, COALESCE(p.node_id, 'local'), p.gpu_id, p.pid, p.name, p.gpu_mem
		FROM gpu_processes p
		JOIN ticks t
			ON COALESCE(p.node_id, 'local') = t.node_id
			AND p.gpu_id = t.gpu_id
			AND p.ts = t.ts
		WHERE p.ts >= ?
		ORDER BY 2, p.gpu_id, p.pid`, cutoff, cutoff)
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

// LatestGPUMetricTs returns the timestamp of the newest raw GPU metric row
// for nodeID, or 0 when there is none. An empty nodeID spans every node.
//
// Callers that ask "is collection still running here?" must name their own
// node: standalone mode serves the ingest endpoints too, so a remote agent's
// pushes land in the same table and would keep the answer moving while the
// local collector is wedged. Both node_id and ts are indexed.
func (db *DB) LatestGPUMetricTs(nodeID string) (int64, error) {
	var ts sql.NullInt64
	var err error

	if nodeID == "" {
		err = db.conn.QueryRow(`SELECT MAX(ts) FROM gpu_metrics_raw`).Scan(&ts)
	} else {
		err = db.conn.QueryRow(`SELECT MAX(ts) FROM gpu_metrics_raw WHERE node_id = ?`, nodeID).Scan(&ts)
	}
	if err != nil {
		return 0, err
	}
	return ts.Int64, nil
}
