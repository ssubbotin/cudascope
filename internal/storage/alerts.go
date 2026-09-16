package storage

import (
	"database/sql"
	"fmt"

	"github.com/sergey/cudascope/internal/alerts"
)

// AlertEventQuery selects a slice of the alert journal.
type AlertEventQuery struct {
	From, To int64  // by start time, unix seconds
	NodeID   string // empty = every node
	Kind     string // empty = every kind
	Limit    int    // 0 = default
}

const defaultAlertLimit = 200

// OpenAlertEvent records a new alert and returns its id. The partial unique
// index refuses a second open event for the same node, GPU and kind.
func (db *DB) OpenAlertEvent(e alerts.Event) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	res, err := db.conn.Exec(`INSERT INTO alert_events
		(node_id, gpu_id, kind, threshold, started_at, peak_value, last_value)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.NodeID, gpuIDArg(e.GPUID), string(e.Kind), e.Threshold, e.StartedAt, e.PeakValue, e.LastValue)
	if err != nil {
		return 0, fmt.Errorf("open alert event: %w", err)
	}
	return res.LastInsertId()
}

// UpdateAlertEvent stores the running values of an open event.
func (db *DB) UpdateAlertEvent(id int64, last, peak float64) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.conn.Exec(`UPDATE alert_events SET last_value = ?, peak_value = ?
		WHERE id = ? AND ended_at IS NULL`, last, peak, id)
	return err
}

// CloseAlertEvent ends an open event. Closing an already closed event
// changes nothing, which keeps a retried close harmless.
func (db *DB) CloseAlertEvent(id, endedAt int64, last, peak float64) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.conn.Exec(`UPDATE alert_events SET ended_at = ?, last_value = ?, peak_value = ?
		WHERE id = ? AND ended_at IS NULL`, endedAt, last, peak, id)
	return err
}

// OpenAlertEvents returns the events still open, so a restarting process
// can adopt them instead of opening duplicates.
func (db *DB) OpenAlertEvents() ([]alerts.Event, error) {
	rows, err := db.conn.Query(`SELECT id, node_id, gpu_id, kind, threshold, started_at, ended_at, peak_value, last_value
		FROM alert_events WHERE ended_at IS NULL ORDER BY started_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanAlertEvents(rows)
}

// ListAlertEvents returns the journal for a time range, newest first.
func (db *DB) ListAlertEvents(q AlertEventQuery) ([]alerts.Event, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultAlertLimit
	}

	query := `SELECT id, node_id, gpu_id, kind, threshold, started_at, ended_at, peak_value, last_value
		FROM alert_events WHERE started_at >= ? AND started_at <= ?`
	args := []any{q.From, q.To}

	if q.NodeID != "" {
		query += " AND node_id = ?"
		args = append(args, q.NodeID)
	}
	if q.Kind != "" {
		query += " AND kind = ?"
		args = append(args, q.Kind)
	}
	query += " ORDER BY started_at DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanAlertEvents(rows)
}

// NodeLastSeen returns the heartbeat of every known node.
func (db *DB) NodeLastSeen() (map[string]int64, error) {
	rows, err := db.conn.Query(`SELECT node_id, last_seen FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]int64)
	for rows.Next() {
		var node string
		var ts int64
		if err := rows.Scan(&node, &ts); err != nil {
			return nil, err
		}
		seen[node] = ts
	}
	return seen, rows.Err()
}

func scanAlertEvents(rows *sql.Rows) ([]alerts.Event, error) {
	var out []alerts.Event
	for rows.Next() {
		var (
			e     alerts.Event
			gpu   sql.NullInt64
			ended sql.NullInt64
			kind  string
		)
		if err := rows.Scan(&e.ID, &e.NodeID, &gpu, &kind, &e.Threshold,
			&e.StartedAt, &ended, &e.PeakValue, &e.LastValue); err != nil {
			return nil, fmt.Errorf("scan alert event: %w", err)
		}
		e.Kind = alerts.Kind(kind)
		if gpu.Valid {
			id := int(gpu.Int64)
			e.GPUID = &id
		}
		if ended.Valid {
			at := ended.Int64
			e.EndedAt = &at
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func gpuIDArg(id *int) any {
	if id == nil {
		return nil
	}
	return *id
}
