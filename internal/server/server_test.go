package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/audemed44/lookout/internal/checks"
	"github.com/audemed44/lookout/internal/notify"
	"github.com/audemed44/lookout/internal/speedtest"
	"github.com/audemed44/lookout/internal/store"
)

type env struct {
	t    *testing.T
	srv  *httptest.Server
	db   *store.Store
	hook *[]map[string]any
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	n := notify.New(db)
	e := checks.NewEngine(db, n)
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	app := New(Options{
		Store: db, Engine: e, Notifier: n, Speedtest: speedtest.New(db, n), Token: "tok", DataDir: t.TempDir(),
		Web: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>app")}},
	})
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	got := []map[string]any{}
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		got = append(got, m)
	}))
	t.Cleanup(hook.Close)
	_ = db.SaveTarget(ctx, &store.Target{Name: "Hook", URL: "json://" + strings.TrimPrefix(hook.URL, "http://"), Enabled: true})
	return &env{t: t, srv: srv, db: db, hook: &got}
}

func (e *env) do(method, path, token, body string, header ...string) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestAuth(t *testing.T) {
	e := setup(t)
	if code, _ := e.do("GET", "/api/checks", "", ""); code != 401 {
		t.Errorf("no token: %d", code)
	}
	if code, _ := e.do("GET", "/api/checks", "wrong", ""); code != 401 {
		t.Errorf("wrong token: %d", code)
	}
	if code, _ := e.do("GET", "/api/checks", "tok", ""); code != 200 {
		t.Errorf("token: %d", code)
	}
	if code, _ := e.do("POST", "/api/checks", "tok", `{"name":"x","type":"tcp","target":"a:1"}`, "Origin", "https://evil.example.com"); code != 403 {
		t.Errorf("cross-origin: %d", code)
	}
	if code, body := e.do("GET", "/checks/1", "", ""); code != 200 || !strings.Contains(body, "app") {
		t.Errorf("spa: %d %s", code, body)
	}
	if code, _ := e.do("GET", "/healthz", "", ""); code != 204 {
		t.Errorf("healthz: %d", code)
	}
}

func TestNotifyEndpoint(t *testing.T) {
	e := setup(t)
	if code, _ := e.do("POST", "/notify/foyer", "", `{"title":"x"}`); code != 404 {
		t.Errorf("unknown key: %d", code)
	}
	if code, body := e.do("POST", "/api/senders", "tok", `{"name":"Foyer","key":"foyer-key","tags":["foyer"]}`); code != 200 {
		t.Fatalf("add sender: %d %s", code, body)
	}
	// What Foyer and Hoist send to apprise-api today.
	code, body := e.do("POST", "/notify/foyer-key", "", `{"title":"Hoist is down","body":"Its container hoist is restarting.","type":"failure","tag":"homelab"}`)
	if code != 200 || !strings.Contains(body, `"status":"sent"`) {
		t.Fatalf("notify: %d %s", code, body)
	}
	if len(*e.hook) != 1 || (*e.hook)[0]["title"] != "Hoist is down" || (*e.hook)[0]["type"] != "failure" {
		t.Fatalf("delivered: %v", *e.hook)
	}
	hist, _ := e.db.Notifications(context.Background(), 0, 1)
	if hist[0].Source != "Foyer" || strings.Join(hist[0].Tags, ",") != "foyer,homelab" {
		t.Errorf("history: %+v", hist[0])
	}
	// Form-encoded, as `apprise` CLI users do, with the token instead of a key.
	req, _ := http.NewRequest("POST", e.srv.URL+"/notify", strings.NewReader("title=Hi&body=there"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("form: %v %v", resp.StatusCode, err)
	}
	if code, _ := e.do("POST", "/notify", "", `{"title":"x"}`); code != 401 {
		t.Errorf("no key, no token: %d", code)
	}
	if code, _ := e.do("POST", "/notify/foyer-key", "", `{}`); code != 400 {
		t.Errorf("empty: %d", code)
	}
	// Nothing routed: Apprise's 204.
	_ = e.db.SaveRoute(context.Background(), &store.Route{Name: "only-x", Tags: []string{"x"}, Targets: []string{"Hook"}})
	if code, _ := e.do("POST", "/notify/foyer-key", "", `{"title":"unrouted"}`); code != 204 {
		t.Errorf("no route: %d", code)
	}
}

func TestChecksAndPing(t *testing.T) {
	e := setup(t)
	code, body := e.do("POST", "/api/checks", "tok", `{"name":"Backup","type":"push","period":3600}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	var v checks.View
	_ = json.Unmarshal([]byte(body), &v)
	if v.Check.Token == "" || v.Check.Grace != 360 {
		t.Fatalf("created: %+v", v.Check)
	}
	if code, _ := e.do("POST", "/ping/"+v.Check.Token, "", "done: 3 GB"); code != 200 {
		t.Fatalf("ping: %d", code)
	}
	if code, _ := e.do("GET", "/ping/nope", "", ""); code != 404 {
		t.Errorf("bad token: %d", code)
	}
	_, body = e.do("GET", "/api/overview", "tok", "")
	if !strings.Contains(body, `"message":"done: 3 GB"`) || !strings.Contains(body, `"up":1`) {
		t.Errorf("overview: %s", body)
	}
	if code, body := e.do("POST", "/api/checks", "tok", `{"name":"x","type":"http","target":"nope"}`); code != 400 || !strings.Contains(body, "http://") {
		t.Errorf("invalid: %d %s", code, body)
	}
	_, body = e.do("GET", "/api/foyer/widget", "tok", "")
	if !strings.Contains(body, `"label":"Up","value":"1","unit":"/1"`) {
		t.Errorf("widget: %s", body)
	}
}

func TestStatusPage(t *testing.T) {
	e := setup(t)
	if code, _ := e.do("GET", "/api/public/status", "", ""); code != 404 {
		t.Errorf("off: %d", code)
	}
	s := store.DefaultSettings()
	s.StatusPage = store.StatusPage{Enabled: true, Title: "Home"}
	_ = e.db.SaveSettings(context.Background(), s)
	e.do("POST", "/api/checks", "tok", `{"name":"Web","type":"tcp","target":"127.0.0.1:1","interval":60}`)
	code, body := e.do("GET", "/api/public/status", "", "")
	if code != 200 || !strings.Contains(body, `"title":"Home"`) || !strings.Contains(body, `"name":"Web"`) || strings.Contains(body, "127.0.0.1") {
		t.Errorf("on: %d %s", code, body)
	}
}

func TestTargetsMasked(t *testing.T) {
	e := setup(t)
	code, body := e.do("POST", "/api/targets", "tok", `{"name":"TG","url":"tgram://123:SECRET/42","enabled":true}`)
	if code != 200 || strings.Contains(body, "SECRET") || !strings.Contains(body, `"masked":true`) {
		t.Fatalf("create: %d %s", code, body)
	}
	_, body = e.do("GET", "/api/targets", "tok", "")
	if strings.Contains(body, "SECRET") {
		t.Errorf("list leaks the token: %s", body)
	}
	if code, _ := e.do("POST", "/api/targets", "tok", `{"name":"x","url":"smtp://x","enabled":true}`); code != 400 {
		t.Errorf("unsupported: %d", code)
	}
}
