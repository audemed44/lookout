// Package store keeps Lookout's state in SQLite: checks and their results,
// incidents, notification targets and history, speedtests and settings.
//
// Results are kept raw for a couple of days and rolled up per hour as they
// arrive, so uptime and latency over weeks come from the hourly table and
// nothing about history is held in memory.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure Go, so the build stays static
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS checks (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	name    TEXT NOT NULL,
	config  TEXT NOT NULL,
	created INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS check_state (
	check_id INTEGER PRIMARY KEY,
	state    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS results (
	check_id INTEGER NOT NULL,
	time     INTEGER NOT NULL,
	ok       INTEGER NOT NULL,
	latency  REAL NOT NULL DEFAULT 0,
	message  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS results_check ON results (check_id, time);
CREATE INDEX IF NOT EXISTS results_time ON results (time);
CREATE TABLE IF NOT EXISTS rollups (
	check_id    INTEGER NOT NULL,
	hour        INTEGER NOT NULL,
	up          INTEGER NOT NULL,
	total       INTEGER NOT NULL,
	latency_sum REAL NOT NULL,
	latency_n   INTEGER NOT NULL,
	latency_max REAL NOT NULL,
	PRIMARY KEY (check_id, hour)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS incidents (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	check_id INTEGER NOT NULL,
	started  INTEGER NOT NULL,
	ended    INTEGER NOT NULL DEFAULT 0,
	cause    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS incidents_check ON incidents (check_id, started);
CREATE TABLE IF NOT EXISTS maintenance (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	config TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS targets (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	name    TEXT NOT NULL,
	url     TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS routes (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	config TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS senders (
	key       TEXT PRIMARY KEY,
	name      TEXT NOT NULL,
	tags      TEXT NOT NULL DEFAULT '[]',
	created   INTEGER NOT NULL,
	last_used INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS notifications (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	time    INTEGER NOT NULL,
	source  TEXT NOT NULL,
	type    TEXT NOT NULL,
	title   TEXT NOT NULL,
	body    TEXT NOT NULL,
	tags    TEXT NOT NULL DEFAULT '[]',
	status  TEXT NOT NULL,
	detail  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS notifications_time ON notifications (time);
CREATE TABLE IF NOT EXISTS queue (
	notification_id INTEGER NOT NULL,
	target_id       INTEGER NOT NULL,
	release         INTEGER NOT NULL,
	reason          TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS speedtests (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	time     INTEGER NOT NULL,
	source   TEXT NOT NULL,
	server   TEXT NOT NULL DEFAULT '',
	server_id TEXT NOT NULL DEFAULT '',
	isp      TEXT NOT NULL DEFAULT '',
	ping     REAL NOT NULL DEFAULT 0,
	jitter   REAL NOT NULL DEFAULT 0,
	download REAL NOT NULL DEFAULT 0,
	upload   REAL NOT NULL DEFAULT 0,
	error    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS speedtests_time ON speedtests (time);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// Open opens (or creates) the database.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-1024)&_pragma=foreign_keys(0)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: writes come from one process, and every open SQLite
	// connection holds its own page cache. Closed when idle for a while.
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB is the underlying handle, for importers that read another database.
func (s *Store) DB() *sql.DB { return s.db }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Get reads a JSON setting into v; a missing key leaves v as it was.
func (s *Store) Get(ctx context.Context, key string, v any) error {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), v)
}

// Put stores v as a JSON setting.
func (s *Store) Put(ctx context.Context, key string, v any) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, mustJSON(v))
	return err
}

// Prune drops raw results, rollups and notifications older than the
// retention settings.
func (s *Store) Prune(ctx context.Context, r Retention) error {
	now := time.Now()
	stmts := []struct {
		q   string
		arg int64
	}{
		{`DELETE FROM results WHERE time < ?`, now.Add(-time.Duration(r.RawHours) * time.Hour).UnixMilli()},
		{`DELETE FROM rollups WHERE hour < ?`, now.AddDate(0, 0, -r.RollupDays).UnixMilli()},
		{`DELETE FROM notifications WHERE time < ? AND id NOT IN (SELECT notification_id FROM queue)`, now.AddDate(0, 0, -r.NotificationDays).UnixMilli()},
	}
	for _, st := range stmts {
		if _, err := s.db.ExecContext(ctx, st.q, st.arg); err != nil {
			return err
		}
	}
	return nil
}

// Settings returns the saved settings with defaults filled in.
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	out := DefaultSettings()
	err := s.Get(ctx, "settings", &out)
	out.Normalize()
	return out, err
}

func (s *Store) SaveSettings(ctx context.Context, v Settings) error {
	v.Normalize()
	return s.Put(ctx, "settings", v)
}
