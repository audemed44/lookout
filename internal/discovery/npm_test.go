package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/audemed44/lookout/internal/store"
)

func fakeNPM(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tokens":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["secret"] != "pw" {
				http.Error(w, `{"error":"bad"}`, 401)
				return
			}
			_, _ = w.Write([]byte(`{"token":"t","expires":"2099-01-01T00:00:00Z"}`))
		case "/api/nginx/proxy-hosts":
			if r.Header.Get("Authorization") != "Bearer t" {
				http.Error(w, "no", 401)
				return
			}
			_, _ = w.Write([]byte(`[
				{"domain_names":["books.example.com","Read.example.com"],"enabled":true,"certificate_id":3},
				{"domain_names":["plain.example.com"],"enabled":1,"certificate_id":0},
				{"domain_names":["off.example.com"],"enabled":false,"certificate_id":3},
				{"domain_names":["*.example.com"],"enabled":true,"certificate_id":3}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSync(t *testing.T) {
	srv := fakeNPM(t)
	src := &NPM{URL: srv.URL, Email: "a@b", Password: "pw"}
	cfg := store.DiscoverySettings{Interval: 60, Tags: []string{"proxy"}, Ignored: []string{"read.example.com"}}
	existing := []store.Check{
		{ID: 1, Name: "gone", Source: "npm", SourceKey: "gone.example.com", Target: "https://gone.example.com"},
		{ID: 2, Name: "plain", Source: "npm", SourceKey: "plain.example.com", Target: "https://plain.example.com"},
	}
	res, changed, err := Sync(context.Background(), src, existing, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Routes != 3 || len(res.Added) != 1 || res.Added[0] != "books.example.com" {
		t.Errorf("added = %v (routes %d)", res.Added, res.Routes)
	}
	if len(res.Updated) != 1 || res.Updated[0] != "plain.example.com" {
		t.Errorf("updated = %v", res.Updated)
	}
	if len(res.Missing) != 1 || res.Missing[0] != "gone.example.com" {
		t.Errorf("missing = %v", res.Missing)
	}
	if len(changed) != 2 || changed[0].Target != "https://books.example.com" || changed[1].Target != "http://plain.example.com" {
		t.Errorf("changed = %+v", changed)
	}
	if c := changed[0]; c.Type != store.HTTP || c.Group != "Proxy" || c.Tags[0] != "proxy" || c.Status != "200-499" {
		t.Errorf("new check = %+v", c)
	}

	src.Password, src.token = "wrong", ""
	if _, _, err := Sync(context.Background(), src, nil, cfg); err == nil {
		t.Error("bad login didn't fail")
	}
}
