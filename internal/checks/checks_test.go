package checks

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/audemed44/lookout/internal/store"
)

func TestStatusAccepted(t *testing.T) {
	cases := []struct {
		spec string
		code int
		want bool
	}{
		{"", 200, true}, {"", 301, false}, {"200-399", 302, true},
		{"200-299,401", 401, true}, {"200-299, 401", 403, false}, {"404", 404, true}, {"junk", 200, false},
	}
	for _, c := range cases {
		if got := StatusAccepted(c.spec, c.code); got != c.want {
			t.Errorf("StatusAccepted(%q, %d) = %v", c.spec, c.code, got)
		}
	}
}

func TestJSONLookup(t *testing.T) {
	doc := map[string]any{"data": map[string]any{"items": []any{map[string]any{"ok": true, "n": 3.0}}, "name": "x"}}
	cases := map[string]string{"data.name": "x", "$.data.items[0].ok": "true", "data.items[0].n": "3", "data.items": `[{"n":3,"ok":true}]`}
	for path, want := range cases {
		if got, ok := JSONLookup(doc, path); !ok || got != want {
			t.Errorf("%s = %q, %v", path, got, ok)
		}
	}
	if _, ok := JSONLookup(doc, "data.items[3]"); ok {
		t.Error("out of range index found")
	}
}

func TestProbeHTTP(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/down" {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"status":"healthy","version":"1.2"}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	c := store.Check{Name: "x", Type: store.HTTP, Target: srv.URL}
	c.Defaults()

	// httptest's certificate isn't trusted.
	if out := Probe(ctx, c); out.OK || !strings.Contains(out.Message, "unknown authority") {
		t.Errorf("untrusted: %+v", out)
	}
	c.IgnoreTLS = true
	out := Probe(ctx, c)
	if !out.OK || out.CertExpires.IsZero() {
		t.Errorf("ignore TLS: %+v", out)
	}
	c.Keyword = "healthy"
	if out := Probe(ctx, c); !out.OK {
		t.Errorf("keyword: %+v", out)
	}
	c.InvertKeyword = true
	if out := Probe(ctx, c); out.OK {
		t.Errorf("inverted keyword: %+v", out)
	}
	c.Keyword, c.InvertKeyword = "", false
	c.JSONPath, c.Expect = "status", "healthy"
	if out := Probe(ctx, c); !out.OK {
		t.Errorf("json: %+v", out)
	}
	c.Expect = "sick"
	if out := Probe(ctx, c); out.OK || !strings.Contains(out.Message, `expected "sick"`) {
		t.Errorf("json mismatch: %+v", out)
	}
	c.JSONPath, c.Target = "", srv.URL+"/down"
	if out := Probe(ctx, c); out.OK || out.Message != "HTTP 503 (expected 200-299)" {
		t.Errorf("503: %+v", out)
	}

	// TLS check on the same server.
	tc := store.Check{Name: "t", Type: store.TLS, Target: strings.TrimPrefix(srv.URL, "https://")}
	tc.Defaults()
	if out := Probe(ctx, tc); out.OK || out.CertExpires.IsZero() {
		t.Errorf("tls untrusted: %+v", out)
	}
	tc.IgnoreTLS = true
	if out := Probe(ctx, tc); !out.OK {
		t.Errorf("tls: %+v", out)
	}
}

func TestProbeTCP(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	c := store.Check{Name: "x", Type: store.TCP, Target: addr}
	c.Defaults()
	if out := Probe(context.Background(), c); !out.OK {
		t.Errorf("open: %+v", out)
	}
	l.Close()
	if out := Probe(context.Background(), c); out.OK {
		t.Errorf("closed: %+v", out)
	}
}

func newEngine(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewEngine(db, nil), db
}

func titles(ev events) string {
	var out []string
	for _, n := range ev.notes {
		out = append(out, n.Title)
	}
	return strings.Join(out, "|")
}

func TestApplyRetriesAndRecovery(t *testing.T) {
	e, _ := newEngine(t)
	c := store.Check{Name: "web", Type: store.HTTP, Target: "http://x", Retries: 2}
	st := &store.State{Status: store.Unknown}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	if ev := e.apply(c, st, true, "ok", now); st.Status != store.Up || len(ev.notes) != 0 {
		t.Fatalf("first up: %+v %s", st, titles(ev))
	}
	for i := 1; i <= 2; i++ {
		ev := e.apply(c, st, false, "boom", now.Add(time.Duration(i)*time.Minute))
		if st.Status != store.Pending || len(ev.notes) != 0 {
			t.Fatalf("retry %d: %s %s", i, st.Status, titles(ev))
		}
	}
	ev := e.apply(c, st, false, "boom", now.Add(3*time.Minute))
	if st.Status != store.Down || titles(ev) != "web is down" || !ev.opened {
		t.Fatalf("down: %s %s", st.Status, titles(ev))
	}
	if ev := e.apply(c, st, false, "boom", now.Add(4*time.Minute)); len(ev.notes) != 0 {
		t.Fatalf("still down notified again: %s", titles(ev))
	}
	ev = e.apply(c, st, true, "ok", now.Add(13*time.Minute))
	if st.Status != store.Up || titles(ev) != "web is back up" || !ev.closed || ev.notes[0].Body != "After 10 min." {
		t.Fatalf("recovery: %s %+v", st.Status, ev.notes)
	}
}

func TestFlapping(t *testing.T) {
	e, _ := newEngine(t)
	c := store.Check{Name: "web", Type: store.HTTP, Target: "http://x"}
	st := &store.State{Status: store.Unknown}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	e.apply(c, st, true, "ok", now)
	var all []string
	for i := 1; i <= 8; i++ {
		ev := e.apply(c, st, i%2 == 0, "x", now.Add(time.Duration(i)*time.Minute))
		if s := titles(ev); s != "" {
			all = append(all, s)
		}
	}
	want := "web is down|web is back up|web is down|web is flapping"
	if strings.Join(all, "|") != want {
		t.Fatalf("notifications = %v", all)
	}
	// Steady for the flap window: one "settled" message.
	ev := e.apply(c, st, true, "ok", now.Add(45*time.Minute))
	if titles(ev) != "web has settled: up" || st.Flapping {
		t.Fatalf("settle: %s flapping=%v", titles(ev), st.Flapping)
	}
}

func TestCertNotes(t *testing.T) {
	c := store.Check{Name: "site", Target: "https://x", CertDays: 14}
	st := &store.State{}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	days := func(d int) time.Time { return now.Add(time.Duration(d)*24*time.Hour + time.Hour) }

	if n := certNotes(c, st, days(30), now); len(n) != 0 {
		t.Fatal("warned with 30 days left")
	}
	n := certNotes(c, st, days(13), now)
	if len(n) != 1 || n[0].Title != "Certificate for site expires in 13 days" {
		t.Fatalf("13 days: %+v", n)
	}
	if n := certNotes(c, st, days(12), now); len(n) != 0 {
		t.Fatal("warned twice for the same threshold")
	}
	if n := certNotes(c, st, days(6), now); len(n) != 1 {
		t.Fatal("no warning at 7 days")
	}
	if n := certNotes(c, st, days(1), now); len(n) != 1 || n[0].Type != store.Failure {
		t.Fatalf("1 day: %+v", n)
	}
	if n := certNotes(c, st, now.Add(-time.Hour), now); len(n) != 1 || !strings.Contains(n[0].Title, "expired") {
		t.Fatalf("expired: %+v", n)
	}
	if n := certNotes(c, st, days(89), now); len(n) != 1 || n[0].Type != store.Success {
		t.Fatalf("renewed: %+v", n)
	}
	c.CertDays = -1
	if n := certNotes(c, st, now, now); len(n) != 0 {
		t.Fatal("warned with warnings off")
	}
}

func TestHeartbeats(t *testing.T) {
	e, db := newEngine(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	c := store.Check{Name: "backup", Type: store.Push, Token: "tok", Period: 3600, Grace: 300}
	if err := db.SaveCheck(ctx, &c); err != nil {
		t.Fatal(err)
	}
	e.Upsert(c)
	status := func() string { v, _ := e.View(c.ID); return v.Status }

	if err := e.Ping("nope", "", ""); err != ErrUnknownToken {
		t.Fatalf("unknown token: %v", err)
	}
	if err := e.Ping("tok", PingStart, ""); err != nil {
		t.Fatal(err)
	}
	if status() != store.Running {
		t.Fatalf("after start: %s", status())
	}
	now = now.Add(2 * time.Minute)
	if err := e.Ping("tok", "", "12 GB"); err != nil {
		t.Fatal(err)
	}
	v, _ := e.View(c.ID)
	if v.Status != store.Up || v.State.Message != "12 GB" || v.State.Latency != 120000 {
		t.Fatalf("after ping: %+v", v)
	}

	// On time: still up. Late (period + grace): down.
	now = now.Add(time.Hour)
	e.checkLate()
	if status() != store.Up {
		t.Fatalf("on time: %s", status())
	}
	now = now.Add(6 * time.Minute)
	e.checkLate()
	if status() != store.Down {
		t.Fatalf("late: %s", status())
	}
	_ = e.Ping("tok", "0", "")
	if status() != store.Up {
		t.Fatalf("exit 0: %s", status())
	}
	_ = e.Ping("tok", "2", "")
	if v, _ := e.View(c.ID); v.Status != store.Down || v.State.Message != "exit code 2" {
		t.Fatalf("exit 2: %+v", v.State)
	}

	// A job that started and never finished.
	_ = e.Ping("tok", "", "")
	_ = e.Ping("tok", PingStart, "")
	now = now.Add(10 * time.Minute)
	e.checkLate()
	if v, _ := e.View(c.ID); v.Status != store.Down || !strings.Contains(v.State.Message, "hasn't finished") {
		t.Fatalf("stuck job: %+v", v.State)
	}
}

func TestMaintenance(t *testing.T) {
	e, _ := newEngine(t)
	now := time.Date(2026, 10, 2, 3, 30, 0, 0, time.UTC) // Friday
	e.Now = func() time.Time { return now }
	c := store.Check{ID: 1, Name: "web", Type: store.HTTP, Target: "http://x", Tags: []string{"media"}}
	e.checks[1] = &entry{check: c, state: store.State{Status: store.Up}}
	e.SetWindows([]store.Window{{Name: "nightly", Enabled: true, Repeat: "daily", From: "03:00", Minutes: 60, Tags: []string{"media"}}})
	if v, _ := e.View(1); v.Status != store.Maintenance {
		t.Fatalf("in window: %s", v.Status)
	}
	now = now.Add(time.Hour)
	if v, _ := e.View(1); v.Status != store.Up {
		t.Fatalf("after window: %s", v.Status)
	}
}

func TestWindowActive(t *testing.T) {
	at := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, time.UTC) }
	w := store.Window{Enabled: true, Repeat: "weekly", Weekdays: []int{5}, From: "23:30", Minutes: 60} // Fri 23:30–00:30
	for when, want := range map[time.Time]bool{
		at(2, 23, 45): true,  // Friday night
		at(3, 0, 15):  true,  // past midnight, still Friday's window
		at(3, 0, 45):  false, // over
		at(3, 23, 45): false, // Saturday
	} {
		if w.Active(when) != want {
			t.Errorf("weekly window at %s: want %v", when.Format("Mon 15:04"), want)
		}
	}
	one := store.Window{Enabled: true, Start: at(2, 10, 0), End: at(2, 11, 0)}
	if !one.Active(at(2, 10, 30)) || one.Active(at(2, 11, 0)) {
		t.Error("one-off window")
	}
	one.Enabled = false
	if one.Active(at(2, 10, 30)) {
		t.Error("disabled window active")
	}
}

func TestAsleepUnderGatehouse(t *testing.T) {
	sawProbe := false
	asleepNow := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawProbe = r.Header.Get(ProbeHeader) != ""
		if asleepNow {
			w.Header().Set(StateHeader, "sleeping")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e, db := newEngine(t)
	ctx := context.Background()
	c := store.Check{Name: "convertx", Type: store.HTTP, Target: srv.URL}
	c.Defaults()
	if err := db.SaveCheck(ctx, &c); err != nil {
		t.Fatal(err)
	}
	e.Upsert(c)
	view := func() View { v, _ := e.View(c.ID); return v }

	out, err := e.RunNow(ctx, c.ID)
	if err != nil || !out.Asleep || !sawProbe {
		t.Fatalf("probe: %+v %v sawProbe=%v", out, err, sawProbe)
	}
	for range 3 { // never counts as a failure
		e.RunNow(ctx, c.ID)
	}
	if v := view(); v.Status != store.Asleep || v.State.Fails != 0 {
		t.Fatalf("asleep: %+v", v)
	}
	if res, _ := db.Recent(ctx, c.ID, 10); len(res) != 0 {
		t.Fatalf("asleep results went in the history: %+v", res)
	}
	asleepNow = false
	e.RunNow(ctx, c.ID)
	if v := view(); v.Status != store.Up || v.State.Asleep {
		t.Fatalf("awake again: %+v", v)
	}

	// A Docker check on a container Gatehouse put to sleep: not probed.
	d := store.Check{Name: "convertx container", Type: store.Docker, Target: "convertx"}
	d.Defaults()
	db.SaveCheck(ctx, &d)
	e.Upsert(d)
	e.SetAsleep(map[string]string{"convertx": "sleeping"})
	e.run(ctx, d.ID)
	if v, _ := e.View(d.ID); v.Status != store.Asleep {
		t.Fatalf("docker check: %+v", v)
	}
}
