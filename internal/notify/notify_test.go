package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/audemed44/lookout/internal/store"
)

type sink struct {
	mu   sync.Mutex
	got  []map[string]any
	path []string
	srv  *httptest.Server
	fail bool
}

func newSink(t *testing.T) *sink {
	s := &sink{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		s.mu.Lock()
		s.got = append(s.got, m)
		s.path = append(s.path, r.URL.Path)
		fail := s.fail
		s.mu.Unlock()
		if fail {
			http.Error(w, "nope", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

func (s *sink) url(path string) string {
	return "json://" + strings.TrimPrefix(s.srv.URL, "http://") + path
}

func setup(t *testing.T) (*Notifier, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db), db
}

func TestParse(t *testing.T) {
	good := []string{
		"tgram://123456:ABC-def/987654",
		"tgram://123456:ABC/-100123:7/42",
		"ntfy://alerts",
		"ntfys://user:pass@ntfy.example.com/alerts",
		"discord://1234/abcd",
		"json://foyer:8080/hook",
		"jsons://example.com/hook?x=1",
	}
	for _, u := range good {
		if _, err := Parse(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	bad := []string{"tgram://onlytoken", "mailto://x", "not a url", "discord://1234"}
	for _, u := range bad {
		if _, err := Parse(u); err == nil {
			t.Errorf("%s: expected an error", u)
		}
	}
	tg, _ := Parse("tgram://123:ABC/-100:7/42")
	if got := tg.(*telegram); got.token != "123:ABC" || len(got.chats) != 2 || got.chats[0] != "-100:7" {
		t.Errorf("telegram parsed as %+v", got)
	}
	n, _ := Parse("ntfys://u:p@ntfy.example.com/alerts")
	if got := n.(*ntfy); got.base != "https://ntfy.example.com" || got.topic != "alerts" || got.user != "u" || got.pass != "p" {
		t.Errorf("ntfy parsed as %+v", got)
	}
}

func TestTelegram(t *testing.T) {
	var body map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
	}))
	defer srv.Close()
	old := TelegramAPI
	TelegramAPI = srv.URL
	defer func() { TelegramAPI = old }()

	s, _ := Parse("tgram://123:ABC/-100:7")
	if err := s.Send(context.Background(), Message{Title: "a <b>", Body: "c & d", Type: "failure"}); err != nil {
		t.Fatal(err)
	}
	if path != "/bot123:ABC/sendMessage" {
		t.Errorf("path = %s", path)
	}
	if body["chat_id"] != "-100" || body["message_thread_id"] != "7" || body["parse_mode"] != "HTML" {
		t.Errorf("body = %v", body)
	}
	if text := body["text"].(string); !strings.Contains(text, "<b>a &lt;b&gt;</b>") || !strings.Contains(text, "c &amp; d") {
		t.Errorf("text = %q", text)
	}
}

func TestEnv(t *testing.T) {
	t.Setenv("LOOKOUT_TEST_URL", "json://x/y")
	if got := Expand("${LOOKOUT_TEST_URL}"); got != "json://x/y" {
		t.Errorf("Expand = %q", got)
	}
	if !UsesEnv("${A}") || UsesEnv("tgram://x/y") {
		t.Error("UsesEnv")
	}
	if m := MissingEnv("${LOOKOUT_TEST_URL}${LOOKOUT_NOT_SET}"); len(m) != 1 || m[0] != "LOOKOUT_NOT_SET" {
		t.Errorf("MissingEnv = %v", m)
	}
}

func TestRoutingDedupeAndFailures(t *testing.T) {
	ctx := context.Background()
	n, db := setup(t)
	phone, digest := newSink(t), newSink(t)
	for _, tg := range []store.Target{
		{Name: "Phone", URL: phone.url("/phone"), Enabled: true},
		{Name: "Digest", URL: digest.url("/digest"), Enabled: true},
	} {
		if err := db.SaveTarget(ctx, &tg); err != nil {
			t.Fatal(err)
		}
	}

	// Without routes, everything goes everywhere.
	rec, err := n.Notify(ctx, Note{Title: "hello", Type: "info"})
	if err != nil || rec.Status != store.Sent {
		t.Fatalf("Notify = %+v, %v", rec, err)
	}
	if phone.count() != 1 || digest.count() != 1 {
		t.Fatalf("counts %d %d", phone.count(), digest.count())
	}

	// The same again within the dedupe window is suppressed.
	rec, _ = n.Notify(ctx, Note{Title: "hello", Type: "info"})
	if rec.Status != store.Suppressed {
		t.Errorf("duplicate status = %s", rec.Status)
	}

	// Failures go to the phone; everything else to the digest.
	for _, r := range []store.Route{
		{Name: "critical", MinType: store.Failure, Targets: []string{"Phone"}, Stop: true},
		{Name: "rest", Targets: []string{"Digest"}, Digest: true},
	} {
		if err := db.SaveRoute(ctx, &r); err != nil {
			t.Fatal(err)
		}
	}
	rec, _ = n.Notify(ctx, Note{Title: "down", Type: "failure"})
	if rec.Status != store.Sent || phone.count() != 2 || digest.count() != 1 {
		t.Errorf("failure: %s, counts %d %d", rec.Status, phone.count(), digest.count())
	}
	rec, _ = n.Notify(ctx, Note{Title: "fyi 1", Type: "info"})
	if rec.Status != store.Queued {
		t.Errorf("digest status = %s", rec.Status)
	}
	_, _ = n.Notify(ctx, Note{Title: "fyi 2", Body: "more", Type: "warning"})
	if digest.count() != 1 {
		t.Fatal("digest sent early")
	}
	n.FlushAll(ctx)
	if digest.count() != 2 {
		t.Fatalf("digest count = %d", digest.count())
	}
	last := digest.got[1]
	if last["title"] != "Lookout: 2 notifications" || last["type"] != "warning" || !strings.Contains(last["message"].(string), "fyi 2 — more") {
		t.Errorf("digest = %v", last)
	}
	hist, _ := db.Notifications(ctx, 0, 10)
	if hist[0].Status != store.Sent {
		t.Errorf("after flush: %s %s", hist[0].Status, hist[0].Detail)
	}

	// A failing target is reported.
	phone.mu.Lock()
	phone.fail = true
	phone.mu.Unlock()
	rec, _ = n.Notify(ctx, Note{Title: "down again", Type: "failure"})
	if rec.Status != store.Failed || !strings.Contains(rec.Detail, "HTTP 500") {
		t.Errorf("failed target: %s %s", rec.Status, rec.Detail)
	}

	// Tags select routes.
	_ = db.SaveRoute(ctx, &store.Route{ID: 1, Name: "critical", Tags: []string{"foyer"}, Targets: []string{"Phone"}, Stop: true})
	phone.mu.Lock()
	phone.fail = false
	phone.mu.Unlock()
	before := phone.count()
	_, _ = n.Notify(ctx, Note{Title: "from foyer", Type: "info", Tags: []string{"Foyer"}})
	if phone.count() != before+1 {
		t.Error("tagged notification didn't reach the phone")
	}
}

func TestQuietHours(t *testing.T) {
	ctx := context.Background()
	n, db := setup(t)
	phone := newSink(t)
	_ = db.SaveTarget(ctx, &store.Target{Name: "Phone", URL: phone.url("/"), Enabled: true})
	settings := store.DefaultSettings()
	settings.Notify.Quiet = true
	settings.Notify.QuietOn, settings.Notify.QuietOff = "23:00", "07:00"
	_ = db.SaveSettings(ctx, settings)
	night := time.Date(2026, 10, 2, 2, 0, 0, 0, time.Local)
	n.Now = func() time.Time { return night }

	rec, _ := n.Notify(ctx, Note{Title: "back up", Type: "success"})
	if rec.Status != store.Queued || phone.count() != 0 {
		t.Fatalf("quiet: %s, %d sent", rec.Status, phone.count())
	}
	rec, _ = n.Notify(ctx, Note{Title: "down", Type: "failure"})
	if rec.Status != store.Sent || phone.count() != 1 {
		t.Fatalf("failure in quiet hours: %s", rec.Status)
	}
	n.Flush(ctx)
	if phone.count() != 1 {
		t.Fatal("released before the end of quiet hours")
	}
	n.Now = func() time.Time { return night.Add(5*time.Hour + time.Minute) }
	n.Flush(ctx)
	if phone.count() != 2 || phone.got[1]["title"] != "back up" {
		t.Fatalf("after quiet hours: %v", phone.got)
	}
}

func TestWindowAndNextAt(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 2, h, m, 0, 0, time.UTC) }
	cases := []struct {
		from, to string
		t        time.Time
		want     bool
	}{
		{"23:00", "07:00", at(2, 0), true},
		{"23:00", "07:00", at(23, 30), true},
		{"23:00", "07:00", at(7, 0), false},
		{"09:00", "17:00", at(12, 0), true},
		{"09:00", "17:00", at(18, 0), false},
		{"bad", "17:00", at(12, 0), false},
	}
	for _, c := range cases {
		if got := InWindow(c.from, c.to, c.t); got != c.want {
			t.Errorf("InWindow(%s, %s, %s) = %v", c.from, c.to, c.t.Format("15:04"), got)
		}
	}
	if got := NextAt("09:00", at(8, 0)); !got.Equal(at(9, 0)) {
		t.Errorf("NextAt same day = %v", got)
	}
	if got := NextAt("09:00", at(9, 0)); !got.Equal(at(9, 0).AddDate(0, 0, 1)) {
		t.Errorf("NextAt next day = %v", got)
	}
}

func TestParseTags(t *testing.T) {
	got := ParseTags("Foyer, hoist  backup,foyer")
	if strings.Join(got, "|") != "foyer|hoist|backup" {
		t.Errorf("ParseTags = %v", got)
	}
}
