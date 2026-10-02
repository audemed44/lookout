// Package importer brings over monitors from Uptime Kuma and results from
// Speedtest Tracker, read straight from their SQLite databases.
package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/lookout/internal/checks"
	"github.com/audemed44/lookout/internal/store"
)

func openRO(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("not a SQLite database: %v", err)
	}
	return db, nil
}

func hasTable(db *sql.DB, name string) bool {
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0
}

// rows reads every row of a query as column name → value, so differences
// between versions' schemas don't matter.
func rows(db *sql.DB, q string, args ...any) ([]map[string]any, error) {
	rs, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	cols, err := rs.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, c := range cols {
			m[c] = vals[i]
		}
		out = append(out, m)
	}
	return out, rs.Err()
}

func str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// timeOf reads a timestamp; the driver returns DATETIME columns as
// time.Time, other columns as text (Laravel writes UTC).
func timeOf(m map[string]any, k string) (time.Time, bool) {
	if t, ok := m[k].(time.Time); ok {
		return t, !t.IsZero()
	}
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, str(m, k), time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func num(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case int64:
		return float64(v)
	case float64:
		return v
	case bool:
		if v {
			return 1
		}
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	case []byte:
		f, _ := strconv.ParseFloat(string(v), 64)
		return f
	}
	return 0
}

// KumaReport says what an Uptime Kuma import did.
type KumaReport struct {
	Imported []string `json:"imported"`
	Existing []string `json:"existing"`
	Skipped  []string `json:"skipped"` // "name: reason"
	Hours    int      `json:"hours"`   // hourly history rows brought over
}

// Kuma imports monitor definitions (and, with history, their hourly uptime)
// from an Uptime Kuma database. Monitors imported before are left alone.
func Kuma(ctx context.Context, path string, s *store.Store, history bool, saved func(store.Check)) (KumaReport, error) {
	rep := KumaReport{Imported: []string{}, Existing: []string{}, Skipped: []string{}}
	db, err := openRO(path)
	if err != nil {
		return rep, err
	}
	defer db.Close()
	if !hasTable(db, "monitor") {
		return rep, fmt.Errorf("no monitor table: is this Uptime Kuma's kuma.db?")
	}
	monitors, err := rows(db, `SELECT * FROM monitor ORDER BY id`)
	if err != nil {
		return rep, err
	}
	groups := map[string]string{}
	for _, m := range monitors {
		if str(m, "type") == "group" {
			groups[str(m, "id")] = str(m, "name")
		}
	}
	existing, err := s.Checks(ctx)
	if err != nil {
		return rep, err
	}
	have := map[string]bool{}
	for _, c := range existing {
		if c.Source == "kuma" {
			have[c.SourceKey] = true
		}
	}
	for _, m := range monitors {
		name := str(m, "name")
		if str(m, "type") == "group" {
			continue
		}
		if have[str(m, "id")] {
			rep.Existing = append(rep.Existing, name)
			continue
		}
		c, reason := kumaCheck(m)
		if reason != "" {
			rep.Skipped = append(rep.Skipped, name+": "+reason)
			continue
		}
		c.Group = groups[str(m, "parent")]
		if err := s.SaveCheck(ctx, &c); err != nil {
			return rep, err
		}
		rep.Imported = append(rep.Imported, name)
		if saved != nil {
			saved(c)
		}
		if history {
			n, err := kumaHistory(ctx, db, s, str(m, "id"), c.ID)
			if err != nil {
				return rep, fmt.Errorf("history of %s: %v", name, err)
			}
			rep.Hours += n
		}
	}
	return rep, nil
}

func kumaCheck(m map[string]any) (store.Check, string) {
	c := store.Check{
		Name:        str(m, "name"),
		Description: str(m, "description"),
		Interval:    int(num(m, "interval")),
		Timeout:     int(num(m, "timeout")),
		Retries:     int(num(m, "maxretries")),
		Paused:      num(m, "active") == 0,
		Source:      "kuma",
		SourceKey:   str(m, "id"),
	}
	host, port := str(m, "hostname"), int(num(m, "port"))
	switch str(m, "type") {
	case "http", "keyword", "json-query":
		c.Type, c.Target = store.HTTP, str(m, "url")
		c.Method = strings.ToUpper(str(m, "method"))
		c.IgnoreTLS = num(m, "ignore_tls") != 0
		var codes []string
		if json.Unmarshal([]byte(str(m, "accepted_statuscodes_json")), &codes) == nil {
			c.Status = strings.Join(codes, ",")
		}
		if str(m, "type") == "keyword" {
			c.Keyword = str(m, "keyword")
			c.InvertKeyword = num(m, "invert_keyword") != 0
		}
		if str(m, "type") == "json-query" {
			c.JSONPath, c.Expect = str(m, "json_path"), str(m, "expected_value")
		}
		if str(m, "basic_auth_user") != "" || str(m, "headers") != "" || str(m, "body") != "" {
			return c, "uses auth, headers or a request body, which Lookout doesn't send"
		}
	case "port":
		c.Type, c.Target = store.TCP, fmt.Sprintf("%s:%d", host, port)
	case "ping":
		c.Type, c.Target = store.Ping, host
	case "dns":
		c.Type, c.Target = store.DNS, host
		c.Record = str(m, "dns_resolve_type")
		c.Resolver = str(m, "dns_resolve_server")
	case "docker":
		c.Type, c.Target = store.Docker, str(m, "docker_container")
	case "push":
		c.Type, c.Token = store.Push, str(m, "push_token")
		c.Period = c.Interval
		c.Interval = 0
		if c.Token == "" {
			c.Token = checks.NewToken()
		}
	default:
		return c, "type " + str(m, "type") + " isn't supported"
	}
	if num(m, "upside_down") != 0 {
		return c, "upside-down mode isn't supported"
	}
	if c.Timeout <= 0 && c.Interval > 0 {
		c.Timeout = max(1, int(float64(c.Interval)*0.8))
	}
	c.Defaults()
	return c, ""
}

// kumaHistory copies a monitor's hourly stats (Kuma 2) or, from older
// versions, its heartbeats summed per hour.
func kumaHistory(ctx context.Context, db *sql.DB, s *store.Store, kumaID string, id int64) (int, error) {
	var list []map[string]any
	var err error
	if hasTable(db, "stat_hourly") {
		list, err = rows(db, `SELECT timestamp * 1000 AS t, up, down, ping, ping_max FROM stat_hourly WHERE monitor_id = ? ORDER BY timestamp`, kumaID)
	} else {
		list, err = rows(db, `
			SELECT CAST(strftime('%s', strftime('%Y-%m-%d %H:00:00', time)) AS INTEGER) * 1000 AS t,
				sum(status = 1) AS up, sum(status = 0) AS down,
				avg(CASE WHEN status = 1 THEN ping END) AS ping, max(ping) AS ping_max
			FROM heartbeat WHERE monitor_id = ? AND status IN (0, 1)
			GROUP BY t ORDER BY t`, kumaID)
	}
	if err != nil {
		return 0, err
	}
	for _, r := range list {
		up, down := int(num(r, "up")), int(num(r, "down"))
		if up+down == 0 {
			continue
		}
		err := s.ImportRollup(ctx, id, store.Rollup{
			Time: time.UnixMilli(int64(num(r, "t"))), Up: up, Total: up + down,
			Latency: num(r, "ping"), Max: num(r, "ping_max"),
		})
		if err != nil {
			return 0, err
		}
	}
	return len(list), nil
}

// SpeedtestReport says what a Speedtest Tracker import did.
type SpeedtestReport struct {
	Imported int `json:"imported"`
	Existing int `json:"existing"`
	Skipped  int `json:"skipped"`
}

// SpeedtestTracker imports completed results from Speedtest Tracker's
// database.sqlite. Results already imported are skipped.
func SpeedtestTracker(ctx context.Context, path string, s *store.Store) (SpeedtestReport, error) {
	var rep SpeedtestReport
	db, err := openRO(path)
	if err != nil {
		return rep, err
	}
	defer db.Close()
	if !hasTable(db, "results") {
		return rep, fmt.Errorf("no results table: is this Speedtest Tracker's database.sqlite?")
	}
	list, err := rows(db, `SELECT * FROM results ORDER BY id`)
	if err != nil {
		return rep, err
	}
	for _, r := range list {
		if st := str(r, "status"); st != "" && st != "completed" {
			rep.Skipped++
			continue
		}
		at, ok := timeOf(r, "created_at")
		if !ok {
			rep.Skipped++
			continue
		}
		if dup, err := s.HasSpeedtest(ctx, at); err != nil {
			return rep, err
		} else if dup {
			rep.Existing++
			continue
		}
		t := store.Speedtest{
			Time: at, Source: "import",
			Ping: num(r, "ping"),
			// Bytes per second.
			Download: num(r, "download") * 8 / 1e6,
			Upload:   num(r, "upload") * 8 / 1e6,
		}
		var data struct {
			ISP    string                   `json:"isp"`
			Ping   struct{ Jitter float64 } `json:"ping"`
			Server struct {
				ID       json.Number `json:"id"`
				Name     string      `json:"name"`
				Location string      `json:"location"`
			} `json:"server"`
		}
		if json.Unmarshal([]byte(str(r, "data")), &data) == nil {
			t.ISP, t.Jitter, t.ServerID = data.ISP, data.Ping.Jitter, data.Server.ID.String()
			t.Server = strings.Trim(data.Server.Name+" · "+data.Server.Location, " ·")
		}
		if err := s.AddSpeedtest(ctx, &t); err != nil {
			return rep, err
		}
		rep.Imported++
	}
	return rep, nil
}
