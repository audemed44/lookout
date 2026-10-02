package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (s *Store) Checks(ctx context.Context) ([]Check, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, config FROM checks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Check{}
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var c Check
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		c.ID = id
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Check(ctx context.Context, id int64) (Check, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT config FROM checks WHERE id = ?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Check{}, ErrNotFound
	}
	if err != nil {
		return Check{}, err
	}
	var c Check
	err = json.Unmarshal([]byte(raw), &c)
	c.ID = id
	return c, err
}

// SaveCheck inserts the check when its ID is 0, and updates it otherwise.
func (s *Store) SaveCheck(ctx context.Context, c *Check) error {
	c.ID = max(c.ID, 0)
	cfg := *c
	cfg.ID = 0
	if c.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO checks (name, config, created) VALUES (?, ?, ?)`,
			c.Name, mustJSON(cfg), time.Now().UnixMilli())
		if err != nil {
			return err
		}
		c.ID, err = res.LastInsertId()
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE checks SET name = ?, config = ? WHERE id = ?`, c.Name, mustJSON(cfg), c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCheck removes a check with its history.
func (s *Store) DeleteCheck(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM checks WHERE id = ?`,
		`DELETE FROM check_state WHERE check_id = ?`,
		`DELETE FROM results WHERE check_id = ?`,
		`DELETE FROM rollups WHERE check_id = ?`,
		`DELETE FROM incidents WHERE check_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) States(ctx context.Context) (map[int64]State, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT check_id, state FROM check_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]State{}
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var st State
		if json.Unmarshal([]byte(raw), &st) == nil {
			out[id] = st
		}
	}
	return out, rows.Err()
}

func (s *Store) SaveState(ctx context.Context, id int64, st State) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO check_state (check_id, state) VALUES (?, ?) ON CONFLICT(check_id) DO UPDATE SET state = excluded.state`,
		id, mustJSON(st))
	return err
}

func hourOf(t time.Time) int64 {
	return t.Truncate(time.Hour).UnixMilli()
}

// AddResult records a result and folds it into its hour's rollup.
func (s *Store) AddResult(ctx context.Context, id int64, r Result) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO results (check_id, time, ok, latency, message) VALUES (?, ?, ?, ?, ?)`,
		id, ms(r.Time), r.OK, r.Latency, r.Message); err != nil {
		return err
	}
	if err := addRollup(ctx, tx, id, hourOf(r.Time), r.OK, 1, r.Latency, r.OK && r.Latency > 0); err != nil {
		return err
	}
	return tx.Commit()
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func addRollup(ctx context.Context, db execer, id, hour int64, ok bool, n int, latency float64, withLatency bool) error {
	up, latN := 0, 0
	if ok {
		up = n
	}
	if withLatency {
		latN = 1
	} else {
		latency = 0
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO rollups (check_id, hour, up, total, latency_sum, latency_n, latency_max)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(check_id, hour) DO UPDATE SET
			up = up + excluded.up, total = total + excluded.total,
			latency_sum = latency_sum + excluded.latency_sum, latency_n = latency_n + excluded.latency_n,
			latency_max = max(latency_max, excluded.latency_max)`,
		id, hour, up, n, latency, latN, latency)
	return err
}

// Rollup is one hour (or, from Uptime, a longer span) of results.
type Rollup struct {
	Time    time.Time `json:"time"`
	Up      int       `json:"up"`
	Total   int       `json:"total"`
	Latency float64   `json:"latency"` // average, ms
	Max     float64   `json:"max"`
}

// ImportRollup adds results from another tool (Uptime Kuma) to an hour.
func (s *Store) ImportRollup(ctx context.Context, id int64, r Rollup) error {
	up := r.Up
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rollups (check_id, hour, up, total, latency_sum, latency_n, latency_max)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(check_id, hour) DO UPDATE SET
			up = up + excluded.up, total = total + excluded.total,
			latency_sum = latency_sum + excluded.latency_sum, latency_n = latency_n + excluded.latency_n,
			latency_max = max(latency_max, excluded.latency_max)`,
		id, hourOf(r.Time), up, r.Total, r.Latency*float64(up), up, r.Max)
	return err
}

// Rollups returns a check's hours since a time, oldest first.
func (s *Store) Rollups(ctx context.Context, id int64, since time.Time) ([]Rollup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT hour, up, total, latency_sum, latency_n, latency_max FROM rollups
		WHERE check_id = ? AND hour >= ? ORDER BY hour`, id, hourOf(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rollup{}
	for rows.Next() {
		var hour int64
		var r Rollup
		var sum float64
		var n int
		if err := rows.Scan(&hour, &r.Up, &r.Total, &sum, &n, &r.Max); err != nil {
			return nil, err
		}
		r.Time = fromMS(hour)
		if n > 0 {
			r.Latency = sum / float64(n)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Uptime is the share of successful results per check since each of the
// given times, as a percentage; -1 when there are no results.
func (s *Store) Uptime(ctx context.Context, since ...time.Time) (map[int64][]float64, error) {
	out := map[int64][]float64{}
	for i, t := range since {
		rows, err := s.db.QueryContext(ctx,
			`SELECT check_id, sum(up), sum(total) FROM rollups WHERE hour >= ? GROUP BY check_id`, hourOf(t))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var up, total int
			if err := rows.Scan(&id, &up, &total); err != nil {
				rows.Close()
				return nil, err
			}
			if out[id] == nil {
				out[id] = make([]float64, len(since))
				for j := range out[id] {
					out[id][j] = -1
				}
			}
			if total > 0 {
				out[id][i] = 100 * float64(up) / float64(total)
			}
		}
		rows.Close()
	}
	return out, nil
}

// Recent returns a check's last n raw results, oldest first.
func (s *Store) Recent(ctx context.Context, id int64, n int) ([]Result, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT time, ok, latency, message FROM (
			SELECT rowid, time, ok, latency, message FROM results WHERE check_id = ? ORDER BY time DESC LIMIT ?
		) ORDER BY time`, id, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Result{}
	for rows.Next() {
		var t int64
		var r Result
		if err := rows.Scan(&t, &r.OK, &r.Latency, &r.Message); err != nil {
			return nil, err
		}
		r.Time = fromMS(t)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Sparklines returns the last n latencies of every check, oldest first,
// with -1 for failures.
func (s *Store) Sparklines(ctx context.Context, n int) (map[int64][]float64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT check_id, ok, latency FROM (
			SELECT check_id, time, ok, latency,
				row_number() OVER (PARTITION BY check_id ORDER BY time DESC) AS rn
			FROM results
		) WHERE rn <= ? ORDER BY check_id, time`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]float64{}
	for rows.Next() {
		var id int64
		var ok bool
		var lat float64
		if err := rows.Scan(&id, &ok, &lat); err != nil {
			return nil, err
		}
		if !ok {
			lat = -1
		}
		out[id] = append(out[id], lat)
	}
	return out, rows.Err()
}

func (s *Store) OpenIncident(ctx context.Context, id int64, started time.Time, cause string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO incidents (check_id, started, cause) VALUES (?, ?, ?)`,
		id, ms(started), cause)
	return err
}

func (s *Store) CloseIncident(ctx context.Context, id int64, ended time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE incidents SET ended = ? WHERE check_id = ? AND ended = 0`, ms(ended), id)
	return err
}

// Incidents lists incidents, newest first; checkID 0 means every check.
func (s *Store) Incidents(ctx context.Context, checkID int64, limit int) ([]Incident, error) {
	q := `SELECT id, check_id, started, ended, cause FROM incidents`
	args := []any{}
	if checkID != 0 {
		q += ` WHERE check_id = ?`
		args = append(args, checkID)
	}
	q += ` ORDER BY started DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var in Incident
		var started, ended int64
		if err := rows.Scan(&in.ID, &in.CheckID, &started, &ended, &in.Cause); err != nil {
			return nil, err
		}
		in.Started, in.Ended = fromMS(started), fromMS(ended)
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *Store) Windows(ctx context.Context) ([]Window, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, config FROM maintenance ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Window{}
	for rows.Next() {
		var w Window
		var raw string
		if err := rows.Scan(&w.ID, &raw); err != nil {
			return nil, err
		}
		id := w.ID
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return nil, err
		}
		w.ID = id
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) SaveWindow(ctx context.Context, w *Window) error {
	cfg := *w
	cfg.ID = 0
	if w.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO maintenance (config) VALUES (?)`, mustJSON(cfg))
		if err != nil {
			return err
		}
		w.ID, err = res.LastInsertId()
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE maintenance SET config = ? WHERE id = ?`, mustJSON(cfg), w.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteWindow(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM maintenance WHERE id = ?`, id)
	return err
}
