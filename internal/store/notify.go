package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (s *Store) Targets(ctx context.Context) ([]Target, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, url, enabled FROM targets ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Target{}
	for rows.Next() {
		var t Target
		if err := rows.Scan(&t.ID, &t.Name, &t.URL, &t.Enabled); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Target(ctx context.Context, id int64) (Target, error) {
	var t Target
	err := s.db.QueryRowContext(ctx, `SELECT id, name, url, enabled FROM targets WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &t.URL, &t.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) SaveTarget(ctx context.Context, t *Target) error {
	if t.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO targets (name, url, enabled) VALUES (?, ?, ?)`, t.Name, t.URL, t.Enabled)
		if err != nil {
			return err
		}
		t.ID, err = res.LastInsertId()
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE targets SET name = ?, url = ?, enabled = ? WHERE id = ?`, t.Name, t.URL, t.Enabled, t.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteTarget(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM targets WHERE id = ?`, id)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `DELETE FROM queue WHERE target_id = ?`, id)
	}
	return err
}

func (s *Store) Routes(ctx context.Context) ([]Route, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, config FROM routes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Route{}
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var r Route
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		r.ID = id
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SaveRoute(ctx context.Context, r *Route) error {
	cfg := *r
	cfg.ID = 0
	if r.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO routes (config) VALUES (?)`, mustJSON(cfg))
		if err != nil {
			return err
		}
		r.ID, err = res.LastInsertId()
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE routes SET config = ? WHERE id = ?`, mustJSON(cfg), r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteRoute(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM routes WHERE id = ?`, id)
	return err
}

func (s *Store) Senders(ctx context.Context) ([]Sender, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, name, tags, created, last_used FROM senders ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sender{}
	for rows.Next() {
		var snd Sender
		var tags string
		var created, used int64
		if err := rows.Scan(&snd.Key, &snd.Name, &tags, &created, &used); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tags), &snd.Tags)
		snd.Created, snd.LastUsed = fromMS(created), fromMS(used)
		out = append(out, snd)
	}
	return out, rows.Err()
}

// Sender looks a key up and marks it used.
func (s *Store) UseSender(ctx context.Context, key string) (Sender, error) {
	var snd Sender
	var tags string
	err := s.db.QueryRowContext(ctx, `SELECT key, name, tags FROM senders WHERE key = ?`, key).Scan(&snd.Key, &snd.Name, &tags)
	if errors.Is(err, sql.ErrNoRows) {
		return snd, ErrNotFound
	}
	if err != nil {
		return snd, err
	}
	_ = json.Unmarshal([]byte(tags), &snd.Tags)
	_, _ = s.db.ExecContext(ctx, `UPDATE senders SET last_used = ? WHERE key = ?`, time.Now().UnixMilli(), key)
	return snd, nil
}

// SaveSender adds a sender, or renames and retags an existing one.
func (s *Store) SaveSender(ctx context.Context, snd Sender) error {
	if snd.Tags == nil {
		snd.Tags = []string{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO senders (key, name, tags, created) VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET name = excluded.name, tags = excluded.tags`,
		snd.Key, snd.Name, mustJSON(snd.Tags), time.Now().UnixMilli())
	return err
}

func (s *Store) DeleteSender(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM senders WHERE key = ?`, key)
	return err
}

func (s *Store) AddNotification(ctx context.Context, n *Notification) error {
	if n.Tags == nil {
		n.Tags = []string{}
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO notifications (time, source, type, title, body, tags, status, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ms(n.Time), n.Source, n.Type, n.Title, n.Body, mustJSON(n.Tags), n.Status, n.Detail)
	if err != nil {
		return err
	}
	n.ID, err = res.LastInsertId()
	return err
}

func (s *Store) UpdateNotification(ctx context.Context, id int64, status, detail string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET status = ?, detail = ? WHERE id = ?`, status, detail, id)
	return err
}

// Duplicate reports whether the same notification was let through since t.
func (s *Store) Duplicate(ctx context.Context, n Notification, since time.Time) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM notifications
		WHERE time >= ? AND type = ? AND title = ? AND body = ? AND status NOT IN (?, ?)`,
		ms(since), n.Type, n.Title, n.Body, Suppressed, NoRoute).Scan(&found)
	return found > 0, err
}

// Notifications lists the history, newest first, before an ID (0: latest).
func (s *Store) Notifications(ctx context.Context, before int64, limit int) ([]Notification, error) {
	q := `SELECT id, time, source, type, title, body, tags, status, detail FROM notifications`
	args := []any{}
	if before > 0 {
		q += ` WHERE id < ?`
		args = append(args, before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotifications(rows)
}

func scanNotifications(rows *sql.Rows) ([]Notification, error) {
	out := []Notification{}
	for rows.Next() {
		var n Notification
		var t int64
		var tags string
		if err := rows.Scan(&n.ID, &t, &n.Source, &n.Type, &n.Title, &n.Body, &tags, &n.Status, &n.Detail); err != nil {
			return nil, err
		}
		n.Time = fromMS(t)
		_ = json.Unmarshal([]byte(tags), &n.Tags)
		out = append(out, n)
	}
	return out, rows.Err()
}

// Enqueue holds a notification for a target until release.
func (s *Store) Enqueue(ctx context.Context, notification, target int64, release time.Time, reason string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO queue (notification_id, target_id, release, reason) VALUES (?, ?, ?, ?)`,
		notification, target, ms(release), reason)
	return err
}

// Due takes the queued notifications released by now, per target.
func (s *Store) Due(ctx context.Context, now time.Time) (map[int64][]Notification, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT q.target_id, n.id, n.time, n.source, n.type, n.title, n.body, n.tags, n.status, n.detail
		FROM queue q JOIN notifications n ON n.id = q.notification_id
		WHERE q.release <= ? ORDER BY n.id`, ms(now))
	if err != nil {
		return nil, err
	}
	out := map[int64][]Notification{}
	for rows.Next() {
		var target int64
		var n Notification
		var t int64
		var tags string
		if err := rows.Scan(&target, &n.ID, &t, &n.Source, &n.Type, &n.Title, &n.Body, &tags, &n.Status, &n.Detail); err != nil {
			rows.Close()
			return nil, err
		}
		n.Time = fromMS(t)
		_ = json.Unmarshal([]byte(tags), &n.Tags)
		out[target] = append(out[target], n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM queue WHERE release <= ?`, ms(now))
	return out, err
}

// QueuedCount is how many notifications wait in the queue.
func (s *Store) QueuedCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(DISTINCT notification_id) FROM queue`).Scan(&n)
	return n, err
}

func (s *Store) AddSpeedtest(ctx context.Context, t *Speedtest) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO speedtests (time, source, server, server_id, isp, ping, jitter, download, upload, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ms(t.Time), t.Source, t.Server, t.ServerID, t.ISP, t.Ping, t.Jitter, t.Download, t.Upload, t.Error)
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// HasSpeedtest reports whether a result at exactly this time exists, so
// imports can be repeated.
func (s *Store) HasSpeedtest(ctx context.Context, t time.Time) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM speedtests WHERE time = ?`, ms(t)).Scan(&n)
	return n > 0, err
}

// Speedtests lists results since a time, oldest first.
func (s *Store) Speedtests(ctx context.Context, since time.Time) ([]Speedtest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, time, source, server, server_id, isp, ping, jitter, download, upload, error
		FROM speedtests WHERE time >= ? ORDER BY time`, ms(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Speedtest{}
	for rows.Next() {
		var t Speedtest
		var at int64
		if err := rows.Scan(&t.ID, &at, &t.Source, &t.Server, &t.ServerID, &t.ISP, &t.Ping, &t.Jitter, &t.Download, &t.Upload, &t.Error); err != nil {
			return nil, err
		}
		t.Time = fromMS(at)
		out = append(out, t)
	}
	return out, rows.Err()
}

// LatestSpeedtest is the newest successful result, if any.
func (s *Store) LatestSpeedtest(ctx context.Context) (*Speedtest, error) {
	var t Speedtest
	var at int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, time, source, server, server_id, isp, ping, jitter, download, upload, error
		FROM speedtests WHERE error = '' ORDER BY time DESC LIMIT 1`).
		Scan(&t.ID, &at, &t.Source, &t.Server, &t.ServerID, &t.ISP, &t.Ping, &t.Jitter, &t.Download, &t.Upload, &t.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.Time = fromMS(at)
	return &t, nil
}

func (s *Store) DeleteSpeedtest(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM speedtests WHERE id = ?`, id)
	return err
}
