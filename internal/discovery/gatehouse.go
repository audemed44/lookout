package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Gatehouse reads routes from Gatehouse's read-only discovery API, along
// with which apps it has put to sleep (scale-to-zero).
type Gatehouse struct {
	URL   string // the admin port, e.g. http://gatehouse:8081
	Token string // GATEHOUSE_DISCOVERY_TOKEN, or the admin token
}

func (g *Gatehouse) Name() string { return "gatehouse" }

type gatehouseHost struct {
	Domains   []string `json:"domains"`
	Enabled   bool     `json:"enabled"`
	HTTPS     bool     `json:"https"`
	Container string   `json:"container"`
	IdleStop  string   `json:"idle_stop"`
	State     string   `json:"state"`
}

func (g *Gatehouse) hosts(ctx context.Context) ([]gatehouseHost, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(g.URL, "/")+"/api/discovery", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach Gatehouse: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("Gatehouse answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var d struct {
		Hosts []gatehouseHost `json:"hosts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("Gatehouse: %v", err)
	}
	return d.Hosts, nil
}

func (g *Gatehouse) Routes(ctx context.Context) ([]Route, error) {
	hosts, err := g.hosts(ctx)
	if err != nil {
		return nil, err
	}
	var out []Route
	for _, h := range hosts {
		if !h.Enabled {
			continue
		}
		for _, d := range h.Domains {
			if strings.HasPrefix(d, "*.") {
				continue // a wildcard isn't a page to load
			}
			out = append(out, Route{Domain: strings.ToLower(d), HTTPS: h.HTTPS})
		}
	}
	return out, nil
}

// Asleep lists the containers Gatehouse has stopped on purpose, with their
// state (sleeping, waking or stopping).
func (g *Gatehouse) Asleep(ctx context.Context) (map[string]string, error) {
	hosts, err := g.hosts(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, h := range hosts {
		if h.Container != "" && h.IdleStop != "" && h.State != "" && h.State != "awake" {
			out[h.Container] = h.State
		}
	}
	return out, nil
}

// Sleeper is a source that knows which containers are asleep on purpose.
type Sleeper interface {
	Asleep(ctx context.Context) (map[string]string, error)
}
