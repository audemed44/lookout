package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// DockerClient reads container state from the Docker socket (read-only
// calls only).
type DockerClient struct {
	http *http.Client
}

func NewDocker(socket string) *DockerClient {
	return &DockerClient{http: &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
			DisableKeepAlives: true,
		},
	}}
}

type ContainerState struct {
	Status string // running, exited, restarting, …
	Health string // healthy, unhealthy, starting, or "" without a healthcheck
}

func (d *DockerClient) get(ctx context.Context, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("docker: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}

func (d *DockerClient) State(ctx context.Context, name string) (ContainerState, error) {
	var info struct {
		State struct {
			Status string
			Health *struct{ Status string }
		}
	}
	code, err := d.get(ctx, "/containers/"+url.PathEscape(name)+"/json", &info)
	if err != nil {
		return ContainerState{}, err
	}
	if code == http.StatusNotFound {
		return ContainerState{}, fmt.Errorf("no container named %s", name)
	}
	if code != http.StatusOK {
		return ContainerState{}, fmt.Errorf("docker answered HTTP %d", code)
	}
	st := ContainerState{Status: info.State.Status}
	if info.State.Health != nil {
		st.Health = info.State.Health.Status
	}
	return st, nil
}

// Containers lists container names, for the check form.
func (d *DockerClient) Containers(ctx context.Context) ([]string, error) {
	var list []struct{ Names []string }
	code, err := d.get(ctx, "/containers/json?all=1", &list)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("docker answered HTTP %d", code)
	}
	out := []string{}
	for _, c := range list {
		if len(c.Names) > 0 {
			out = append(out, c.Names[0][1:])
		}
	}
	return out, nil
}
