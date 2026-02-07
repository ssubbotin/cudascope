package storage

import (
	"context"
	"log"
	"time"
)

// RetentionConfig holds retention durations.
type RetentionConfig struct {
	Raw time.Duration // raw data retention (default 24h)
	M1  time.Duration // 1-minute rollup retention (default 30d)
	H1  time.Duration // 1-hour rollup retention (default 365d)
}

// RunRetention starts the background retention/rollup loop.
func (db *DB) RunRetention(ctx context.Context, cfg RetentionConfig) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// Run once at startup
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

	// Rollup raw -> 1m (aggregate data older than 2 minutes to avoid partial minutes)
	db.rollupTo1m(now - 120)

	// Rollup 1m -> 1h (aggregate data older than 2 hours)
	db.rollupTo1h(now - 7200)

	// Prune old data
	db.prune("gpu_metrics_raw", now-int64(cfg.Raw.Seconds()))
	db.prune("gpu_metrics_1m", now-int64(cfg.M1.Seconds()))
	db.prune("gpu_metrics_1h", now-int64(cfg.H1.Seconds()))
	db.prune("host_metrics_raw", now-int64(cfg.Raw.Seconds()))
	db.prune("gpu_processes", now-int64(cfg.Raw.Seconds()))
}

func (db *DB) rollupTo1m(beforeTs int64) {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Find the latest already-rolled-up minute
	var lastRolled int64
	db.conn.QueryRow("SELECT COALESCE(MAX(ts), 0) FROM gpu_metrics_1m").Scan(&lastRolled)

	_, err := db.conn.Exec(`
		INSERT INTO gpu_metrics_1m (ts, gpu_id, gpu_util_avg, gpu_util_max, mem_util_avg,
			mem_used_avg, mem_used_max, temperature_avg, temperature_max, fan_speed_avg,
			power_draw_avg, power_draw_max, clock_gfx_avg, clock_mem_avg, pcie_tx_avg, pcie_rx_avg)
		SELECT
			(ts / 60) * 60 as minute_ts,
			gpu_id,
			AVG(gpu_util), MAX(gpu_util), AVG(mem_util),
			AVG(mem_used), MAX(mem_used), AVG(temperature), MAX(temperature), AVG(fan_speed),
			AVG(power_draw), MAX(power_draw), AVG(clock_gfx), AVG(clock_mem), AVG(pcie_tx), AVG(pcie_rx)
		FROM gpu_metrics_raw
		WHERE ts > ? AND ts <= ?
		GROUP BY minute_ts, gpu_id
	`, lastRolled, beforeTs)
	if err != nil {
		log.Printf("rollup to 1m error: %v", err)
	}
}

func (db *DB) rollupTo1h(beforeTs int64) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var lastRolled int64
	db.conn.QueryRow("SELECT COALESCE(MAX(ts), 0) FROM gpu_metrics_1h").Scan(&lastRolled)

	_, err := db.conn.Exec(`
		INSERT INTO gpu_metrics_1h (ts, gpu_id, gpu_util_avg, gpu_util_max, mem_util_avg,
			mem_used_avg, mem_used_max, temperature_avg, temperature_max, power_draw_avg, power_draw_max)
		SELECT
			(ts / 3600) * 3600 as hour_ts,
			gpu_id,
			AVG(gpu_util_avg), MAX(gpu_util_max), AVG(mem_util_avg),
			AVG(mem_used_avg), MAX(mem_used_max), AVG(temperature_avg), MAX(temperature_max),
			AVG(power_draw_avg), MAX(power_draw_max)
		FROM gpu_metrics_1m
		WHERE ts > ? AND ts <= ?
		GROUP BY hour_ts, gpu_id
	`, lastRolled, beforeTs)
	if err != nil {
		log.Printf("rollup to 1h error: %v", err)
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
