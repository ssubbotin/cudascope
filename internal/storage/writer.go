package storage

import (
	"fmt"
	"log"
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

	seen := make(map[string]struct{})

	// OR REPLACE because a buffered agent can resend a batch the hub already
	// stored: the unique index turns the duplicate into an overwrite.
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO gpu_metrics_raw
		(ts, node_id, gpu_id, gpu_util, mem_util, mem_used, temperature, fan_speed,
		 power_draw, power_limit, clock_gfx, clock_mem, pcie_tx, pcie_rx,
		 pstate, encoder_util, decoder_util, throttle_reasons, ecc_corrected, ecc_uncorrected)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, m := range metrics {
		nodeID := m.NodeID
		if nodeID == "" {
			nodeID = "local"
		}
		seen[nodeID] = struct{}{}

		_, err := stmt.Exec(
			m.Timestamp, nodeID, m.GPUID, m.GPUUtil, m.MemUtil, m.MemUsed,
			m.Temperature, m.FanSpeed, m.PowerDraw, m.PowerLimit,
			m.ClockGfx, m.ClockMem, m.PCIeTx, m.PCIeRx,
			m.PState, m.EncoderUtil, m.DecoderUtil,
			m.ThrottleReasons, m.EccCorrected, m.EccUncorrected,
		)
		if err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	for nodeID := range seen {
		db.touchNodeSeen(nodeID)
	}
	return nil
}

// WriteHostMetrics inserts a host metrics snapshot.
func (db *DB) WriteHostMetrics(m *collector.HostMetrics) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.conn.Exec(`INSERT OR REPLACE INTO host_metrics_raw
		(ts, node_id, cpu_percent, mem_used, mem_total, disk_used, disk_total,
		 net_rx, net_tx, load_1m, load_5m, load_15m)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Timestamp, m.NodeID, m.CPUPercent, m.MemUsed, m.MemTotal,
		m.DiskUsed, m.DiskTotal, m.NetRx, m.NetTx,
		m.Load1m, m.Load5m, m.Load15m,
	)
	if err != nil {
		return err
	}

	db.touchNodeSeen(m.NodeID)
	return nil
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

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO gpu_processes (ts, node_id, gpu_id, pid, name, cmdline, gpu_mem) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, p := range procs {
		nodeID := p.NodeID
		if nodeID == "" {
			nodeID = "local"
		}
		if _, err := stmt.Exec(p.Timestamp, nodeID, p.GPUID, p.PID, p.Name, p.Cmdline, p.GPUMem); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// WriteVLLMMetrics inserts a vLLM metrics snapshot.
func (db *DB) WriteVLLMMetrics(m *collector.VLLMMetrics) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.conn.Exec(`INSERT OR REPLACE INTO vllm_metrics_raw
		(ts, node_id, model_name, requests_running, requests_waiting, kv_cache_usage,
		 generation_tokens_total, prompt_tokens_total, ttft_avg, tpot_avg,
		 token_throughput, prefix_cache_hit_rate, num_preemptions)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Timestamp, m.NodeID, m.ModelName, m.RequestsRunning, m.RequestsWaiting,
		m.KVCacheUsage, m.GenerationTokensTotal, m.PromptTokensTotal,
		m.TimeToFirstTokenAvg, m.TimePerOutputTokenAvg, m.TokenThroughput,
		m.PrefixCacheHitRate, m.NumPreemptions,
	)
	return err
}

// WriteOllamaMetrics stores one ollama reading: a row per loaded model, and
// a row for the tick itself so that a server holding nothing is still a
// reading rather than a gap.
func (db *DB) WriteOllamaMetrics(m *collector.OllamaMetrics) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	nodeID := m.NodeID
	if nodeID == "" {
		nodeID = "local"
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT OR REPLACE INTO ollama_ticks (ts, node_id, loaded, version)
		VALUES (?, ?, ?, ?)`, m.Timestamp, nodeID, len(m.Models), m.Version); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO ollama_models_raw
		(ts, node_id, model, size_bytes, vram_bytes, context_length, expires_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, model := range m.Models {
		var expires any
		if model.ExpiresAt > 0 {
			expires = model.ExpiresAt
		}
		if _, err := stmt.Exec(m.Timestamp, nodeID, model.Name, model.SizeBytes,
			model.VRAMBytes, model.ContextLength, expires, m.Version); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// RegisterGPUDevices upserts GPU device info for a given node.
func (db *DB) RegisterGPUDevices(nodeID string, devices []collector.GPUDevice) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	now := time.Now().Unix()
	for _, d := range devices {
		_, err := db.conn.Exec(`INSERT INTO gpu_devices
			(node_id, gpu_id, uuid, name, mem_total, driver_ver, first_seen, ecc_supported, throttle_supported)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(node_id, gpu_id) DO UPDATE SET name=excluded.name, mem_total=excluded.mem_total,
				driver_ver=excluded.driver_ver, uuid=excluded.uuid,
				ecc_supported=excluded.ecc_supported, throttle_supported=excluded.throttle_supported`,
			nodeID, d.ID, d.UUID, d.Name, d.MemTotal, d.DriverVer, now, d.EccSupported, d.ThrottleSupported,
		)
		if err != nil {
			return fmt.Errorf("register device %d: %w", d.ID, err)
		}
	}
	return nil
}

// RegisterNode registers or updates a node in the nodes table.
func (db *DB) RegisterNode(nodeID, hostname string, gpuCount int) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	now := time.Now().Unix()
	_, err := db.conn.Exec(`INSERT INTO nodes (node_id, hostname, gpu_count, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET hostname=excluded.hostname, gpu_count=excluded.gpu_count, last_seen=excluded.last_seen`,
		nodeID, hostname, gpuCount, now, now,
	)
	return err
}

// UpdateNodeSeen updates the last_seen timestamp for a node.
func (db *DB) UpdateNodeSeen(nodeID string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.conn.Exec(`UPDATE nodes SET last_seen = ? WHERE node_id = ?`, time.Now().Unix(), nodeID)
	return err
}

// touchNodeSeen records that fresh data has arrived for a node. The caller
// must already hold db.mu.
//
// It lives on the write path because the local collector writes to storage
// directly, without going through the ingest handlers that refresh last_seen
// for remote agents. Without it the local node reports offline from 60
// seconds after startup onwards, however well collection is going.
func (db *DB) touchNodeSeen(nodeID string) {
	if nodeID == "" {
		return
	}
	_, err := db.conn.Exec(`UPDATE nodes SET last_seen = ? WHERE node_id = ?`, time.Now().Unix(), nodeID)
	if err != nil {
		log.Printf("update last_seen for node %s: %v", nodeID, err)
	}
}
