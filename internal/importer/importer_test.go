package importer

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/audemed44/lookout/internal/store"
)

func exec(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func open(t *testing.T) *store.Store {
	db, err := store.Open(filepath.Join(t.TempDir(), "lookout.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestKuma(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kuma.db")
	exec(t, path,
		`CREATE TABLE monitor (id INTEGER PRIMARY KEY, name TEXT, active BOOLEAN, interval INTEGER, url TEXT, type TEXT,
			hostname TEXT, port INTEGER, keyword TEXT, maxretries INTEGER, ignore_tls BOOLEAN, upside_down BOOLEAN,
			accepted_statuscodes_json TEXT, dns_resolve_type TEXT, dns_resolve_server TEXT, push_token TEXT, method TEXT,
			docker_container TEXT, timeout DOUBLE, parent INTEGER, description TEXT, basic_auth_user TEXT, headers TEXT, body TEXT)`,
		`INSERT INTO monitor VALUES
			(1, 'Media', 1, 60, NULL, 'group', NULL, NULL, NULL, 0, 0, 0, '["200-299"]', NULL, NULL, NULL, 'GET', NULL, 48, NULL, NULL, NULL, NULL, NULL),
			(2, 'Jellyfin', 1, 60, 'https://jf.example.com', 'keyword', NULL, NULL, 'Jellyfin', 2, 0, 0, '["200-299","401"]', NULL, NULL, NULL, 'GET', NULL, 48, 1, NULL, NULL, NULL, NULL),
			(3, 'SSH', 0, 120, NULL, 'port', 'nas.lan', 22, NULL, 0, 0, 0, '[]', NULL, NULL, NULL, 'GET', NULL, 0, NULL, NULL, NULL, NULL, NULL),
			(4, 'Backup', 1, 86400, NULL, 'push', NULL, NULL, NULL, 0, 0, 0, '[]', NULL, NULL, 'abc123', 'GET', NULL, 0, NULL, NULL, NULL, NULL, NULL),
			(5, 'MQTT', 1, 60, NULL, 'mqtt', 'broker', 1883, NULL, 0, 0, 0, '[]', NULL, NULL, NULL, 'GET', NULL, 0, NULL, NULL, NULL, NULL, NULL),
			(6, 'Secret', 1, 60, 'https://x', 'http', NULL, NULL, NULL, 0, 0, 0, '[]', NULL, NULL, NULL, 'GET', NULL, 0, NULL, NULL, 'admin', NULL, NULL)`,
		`CREATE TABLE stat_hourly (id INTEGER PRIMARY KEY, monitor_id INTEGER, timestamp INTEGER, ping FLOAT, ping_min FLOAT, ping_max FLOAT, up INTEGER, down INTEGER)`,
		`INSERT INTO stat_hourly (monitor_id, timestamp, ping, ping_max, up, down) VALUES (2, 1790899200, 100, 300, 58, 2), (2, 1790902800, 50, 60, 60, 0)`,
	)
	db := open(t)
	ctx := context.Background()
	var saved int
	rep, err := Kuma(ctx, path, db, true, func(store.Check) { saved++ })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rep.Imported, ",") != "Jellyfin,SSH,Backup" || len(rep.Skipped) != 2 || rep.Hours != 2 || saved != 3 {
		t.Fatalf("report = %+v", rep)
	}
	list, _ := db.Checks(ctx)
	jf, ssh, backup := list[0], list[1], list[2]
	if jf.Type != store.HTTP || jf.Keyword != "Jellyfin" || jf.Status != "200-299,401" || jf.Group != "Media" || jf.Retries != 2 || jf.Timeout != 48 {
		t.Errorf("jellyfin = %+v", jf)
	}
	if ssh.Type != store.TCP || ssh.Target != "nas.lan:22" || !ssh.Paused {
		t.Errorf("ssh = %+v", ssh)
	}
	if backup.Type != store.Push || backup.Token != "abc123" || backup.Period != 86400 {
		t.Errorf("backup = %+v", backup)
	}
	up, _ := db.Uptime(ctx, time.Unix(1790899200, 0).Add(-time.Hour))
	if got := up[jf.ID][0]; got < 98.3 || got > 98.4 {
		t.Errorf("imported uptime = %v", got)
	}
	// Again: nothing new.
	rep, _ = Kuma(ctx, path, db, true, nil)
	if len(rep.Imported) != 0 || len(rep.Existing) != 3 {
		t.Errorf("second import = %+v", rep)
	}
	if _, err := Kuma(ctx, filepath.Join(t.TempDir(), "nope.db"), db, false, nil); err == nil {
		t.Error("missing file imported")
	}
}

func TestSpeedtestTracker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.sqlite")
	exec(t, path,
		`CREATE TABLE results (id INTEGER PRIMARY KEY, service TEXT, ping FLOAT, download INTEGER, upload INTEGER, data TEXT, status TEXT, created_at DATETIME)`,
		`INSERT INTO results (ping, download, upload, data, status, created_at) VALUES
			(5.471, 22364149, 23511439, '{"isp":"Excitel","ping":{"jitter":0.304},"server":{"id":20485,"name":"Excitel","location":"Delhi"}}', 'completed', '2026-10-02 00:08:01'),
			(0, 0, 0, NULL, 'failed', '2026-10-01 16:08:01')`,
	)
	db := open(t)
	ctx := context.Background()
	rep, err := SpeedtestTracker(ctx, path, db)
	if err != nil || rep.Imported != 1 || rep.Skipped != 1 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	got, _ := db.LatestSpeedtest(ctx)
	if got.ISP != "Excitel" || got.ServerID != "20485" || got.Server != "Excitel · Delhi" || got.Download < 178.9 || got.Download > 179 || got.Jitter != 0.304 {
		t.Errorf("result = %+v", got)
	}
	rep, _ = SpeedtestTracker(ctx, path, db)
	if rep.Imported != 0 || rep.Existing != 1 {
		t.Errorf("second import = %+v", rep)
	}
}
