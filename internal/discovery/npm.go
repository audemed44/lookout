// Package discovery creates checks for every domain the reverse proxy
// serves, so a new subdomain gets watched without anyone adding it.
package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/audemed44/lookout/internal/store"
)

// Route is one domain the proxy serves.
type Route struct {
	Domain string `json:"domain"`
	HTTPS  bool   `json:"https"`
}

// Source lists the proxy's routes: Gatehouse's discovery API, or Nginx
// Proxy Manager's.
type Source interface {
	Name() string
	Routes(ctx context.Context) ([]Route, error)
}

// NPM reads proxy hosts from Nginx Proxy Manager's API.
type NPM struct {
	URL, Email, Password string

	mu      sync.Mutex
	token   string
	expires time.Time
}

func (n *NPM) Name() string { return "npm" }

var client = &http.Client{Timeout: 15 * time.Second}

func (n *NPM) call(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(n.URL, "/")+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if path != "/api/tokens" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Nginx Proxy Manager: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("Nginx Proxy Manager answered HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (n *NPM) login(ctx context.Context) error {
	if n.token != "" && time.Until(n.expires) > 5*time.Minute {
		return nil
	}
	var resp struct {
		Token   string `json:"token"`
		Expires string `json:"expires"`
	}
	if err := n.call(ctx, http.MethodPost, "/api/tokens", map[string]string{"identity": n.Email, "secret": n.Password}, &resp); err != nil {
		return err
	}
	n.token = resp.Token
	n.expires, _ = time.Parse(time.RFC3339, resp.Expires)
	if n.expires.IsZero() {
		n.expires = time.Now().Add(time.Hour)
	}
	return nil
}

func (n *NPM) Routes(ctx context.Context) ([]Route, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.login(ctx); err != nil {
		return nil, err
	}
	var hosts []struct {
		DomainNames   []string `json:"domain_names"`
		Enabled       any      `json:"enabled"` // bool, or 0/1 on older versions
		CertificateID any      `json:"certificate_id"`
	}
	if err := n.call(ctx, http.MethodGet, "/api/nginx/proxy-hosts", nil, &hosts); err != nil {
		return nil, err
	}
	var out []Route
	for _, h := range hosts {
		if !truthy(h.Enabled) {
			continue
		}
		for _, d := range h.DomainNames {
			if strings.HasPrefix(d, "*.") {
				continue // a wildcard isn't a page to load
			}
			out = append(out, Route{Domain: strings.ToLower(d), HTTPS: truthy(h.CertificateID)})
		}
	}
	return out, nil
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != "" && x != "0"
	}
	return false
}

// Result says what a sync did.
type Result struct {
	Time    time.Time `json:"time"`
	Routes  int       `json:"routes"`
	Added   []string  `json:"added"`
	Updated []string  `json:"updated"`
	// Missing are discovered checks whose domain the proxy no longer has.
	Missing []string `json:"missing"`
	Error   string   `json:"error,omitempty"`
}

// Sync adds a check for each new domain. Domains on the ignore list are
// skipped, and checks are never deleted: a domain that disappears is
// reported as missing.
func Sync(ctx context.Context, src Source, existing []store.Check, cfg store.DiscoverySettings) (Result, []store.Check, error) {
	res := Result{Time: time.Now(), Added: []string{}, Updated: []string{}, Missing: []string{}}
	routes, err := src.Routes(ctx)
	if err != nil {
		return res, nil, err
	}
	res.Routes = len(routes)
	byKey := map[string]store.Check{}
	for _, c := range existing {
		if c.Source == src.Name() {
			byKey[c.SourceKey] = c
		}
	}
	seen := map[string]bool{}
	var changed []store.Check
	for _, r := range routes {
		if seen[r.Domain] {
			continue
		}
		seen[r.Domain] = true
		if slices.Contains(cfg.Ignored, r.Domain) {
			continue
		}
		scheme := "http://"
		if r.HTTPS {
			scheme = "https://"
		}
		if c, ok := byKey[r.Domain]; ok {
			// The proxy got (or lost) a certificate: follow it.
			if want := scheme + r.Domain; c.Target == scheme2(c.Target, r.Domain) && c.Target != want {
				c.Target = want
				changed = append(changed, c)
				res.Updated = append(res.Updated, r.Domain)
			}
			continue
		}
		c := store.Check{
			Name: r.Domain, Type: store.HTTP, Target: scheme + r.Domain,
			Group: "Proxy", Tags: slices.Clone(cfg.Tags), Interval: cfg.Interval,
			Status: "200-499", Source: src.Name(), SourceKey: r.Domain,
		}
		c.Defaults()
		changed = append(changed, c)
		res.Added = append(res.Added, r.Domain)
	}
	for key := range byKey {
		if !seen[key] {
			res.Missing = append(res.Missing, key)
		}
	}
	slices.Sort(res.Missing)
	return res, changed, nil
}

// scheme2 returns target if it's the plain http:// or https:// URL of the
// domain (one Lookout made), so edited targets are left alone.
func scheme2(target, domain string) string {
	if target == "http://"+domain || target == "https://"+domain {
		return target
	}
	return ""
}
