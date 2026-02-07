package storage

import (
	"context"
	"log"
	"time"
)

// RetentionConfig holds retention durations.
type RetentionConfig struct {
	Raw time.Duration
	M1  time.Duration
	H1  time.Duration
}

// RunRetention starts the background retention/rollup loop.
func (db *DB) RunRetention(ctx context.Context, cfg RetentionConfig) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	db.doRetention(cfg)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			db.doRetention(cfg)
		}
	}
}

func (db *DB) doRetention(cfg RetentionConfig) {
	now := time.Now().Unix()

	// Rollup raw -> 1m (data older than 2 minutes)
	db.rollupGPUTo1m(now - 120)
	db.rollupHostTo1m(now - 120)

	// Rollup 1m -> 1h (data older than 2 hours)
	db.rollupGPUTo1h(now - 7200)
	db.rollupHostTo1h(now - 7200)

	// Prune
	rawCutoff := now - int64(cfg.Raw.Seconds())
	m1Cutoff := now - int64(cfg.M1.Seconds())
	h1Cutoff := now - int64(cfg.H1.Seconds())

	db.prune("gpu_metrics_raw", rawCutoff)
	db.prune("gpu_metrics_1m", m1Cutoff)
	db.prune("gpu_metrics_1h", h1Cutoff)
	db.prune("host_metrics_raw", rawCutoff)
	db.prune("host_metrics_1m", m1Cutoff)
	db.prune("host_metrics_1h", h1Cutoff)
	db.prune("gpu_processes", rawCutoff)
}

func (db *DB) rollupGPUTo1m(beforeTs int64) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var lastRolled int64
	db.conn.QueryRow("SELECT COALESCE(MAX(ts), 0) FROM gpu_metrics_1m").Scan(&lastRolled)

	_, err := db.conn.Exec(`
		INSERT INTO gpu_metrics_1m (ts, node_id, gpu_id, gpu_util_avg, gpu_util_max, mem_util_avg,
			mem_used_avg, mem_used_max, temperature_avg, temperature_max, fan_speed_avg,
			power_draw_avg, power_draw_max, clock_gfx_avg, clock_mem_avg, pcie_tx_avg, pcie_rx_avg)
		SELECT
			(ts / 60) * 60 as minute_ts, COALESCE(node_id, 'local'), gpu_id,
			AVG(gpu_util), MAX(gpu_util), AVG(mem_util),
			AVG(mem_used), MAX(mem_used), AVG(temperature), MAX(temperature), AVG(fan_speed),
			AVG(power_draw), MAX(power_draw), AVG(clock_gfx), AVG(clock_mem), AVG(pcie_tx), AVG(pcie_rx)
		FROM gpu_metrics_raw
		WHERE ts > ? AND ts <= ?
		GROUP BY minute_ts, COALESCE(node_id, 'local'), gpu_id
	`, lastRolled, beforeTs)
	if err != nil {
		log.Printf("GPU rollup to 1m error: %v", err)
	}
}

func (db *DB) rollupGPUTo1h(beforeTs int64) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var lastRolled int64
	db.conn.QueryRow("SELECT COALESCE(MAX(ts), 0) FROM gpu_metrics_1h").Scan(&lastRolled)

	_, err := db.conn.Exec(`
		INSERT INTO gpu_metrics_1h (ts, node_id, gpu_id, gpu_util_avg, gpu_util_max, mem_util_avg,
			mem_used_avg, mem_used_max, temperature_avg, temperature_max, power_draw_avg, power_draw_max)
		SELECT
			(ts / 3600) * 3600 as hour_ts, COALESCE(node_id, 'local'), gpu_id,
			AVG(gpu_util_avg), MAX(gpu_util_max), AVG(mem_util_avg),
			AVG(mem_used_avg), MAX(mem_used_max), AVG(temperature_avg), MAX(temperature_max),
			AVG(power_draw_avg), MAX(power_draw_max)
		FROM gpu_metrics_1m
		WHERE ts > ? AND ts <= ?
		GROUP BY hour_ts, COALESCE(node_id, 'local'), gpu_id
	`, lastRolled, beforeTs)
	if err != nil {
		log.Printf("GPU rollup to 1h error: %v", err)
	}
}

func (db *DB) rollupHostTo1m(beforeTs int64) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var lastRolled int64
	db.conn.QueryRow("SELECT COALESCE(MAX(ts), 0) FROM host_metrics_1m").Scan(&lastRolled)

	_, err := db.conn.Exec(`
		INSERT INTO host_metrics_1m (ts, node_id, cpu_percent_avg, cpu_percent_max,
			mem_used_avg, mem_used_max, mem_total, disk_used, disk_total,
			net_rx_avg, net_tx_avg, load_1m_avg, load_1m_max)
		SELECT
			(ts / 60) * 60 as minute_ts, node_id,
			AVG(cpu_percent), MAX(cpu_percent),
			AVG(mem_used), MAX(mem_used), MAX(mem_total), MAX(disk_used), MAX(disk_total),
			AVG(net_rx), AVG(net_tx), AVG(load_1m), MAX(load_1m)
		FROM host_metrics_raw
		WHERE ts > ? AND ts <= ?
		GROUP BY minute_ts, node_id
	`, lastRolled, beforeTs)
	if err != nil {
		log.Printf("host rollup to 1m error: %v", err)
	}
}

func (db *DB) rollupHostTo1h(beforeTs int64) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var lastRolled int64
	db.conn.QueryRow("SELECT COALESCE(MAX(ts), 0) FROM host_metrics_1h").Scan(&lastRolled)

	_, err := db.conn.Exec(`
		INSERT INTO host_metrics_1h (ts, node_id, cpu_percent_avg, cpu_percent_max,
			mem_used_avg, mem_used_max, mem_total, load_1m_avg, load_1m_max)
		SELECT
			(ts / 3600) * 3600 as hour_ts, node_id,
			AVG(cpu_percent_avg), MAX(cpu_percent_max),
			AVG(mem_used_avg), MAX(mem_used_max), MAX(mem_total),
			AVG(load_1m_avg), MAX(load_1m_max)
		FROM host_metrics_1m
		WHERE ts > ? AND ts <= ?
		GROUP BY hour_ts, node_id
	`, lastRolled, beforeTs)
	if err != nil {
		log.Printf("host rollup to 1h error: %v", err)
	}
}

func (db *DB) prune(table string, beforeTs int64) {
	db.mu.Lock()
	defer db.mu.Unlock()

	result, err := db.conn.Exec("DELETE FROM "+table+" WHERE ts < ?", beforeTs)
	if err != nil {
		log.Printf("prune %s error: %v", table, err)
		return
	}
	if rows, _ := result.RowsAffected(); rows > 0 {
		log.Printf("pruned %d rows from %s", rows, table)
	}
}
