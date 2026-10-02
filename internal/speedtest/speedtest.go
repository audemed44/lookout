// Package speedtest runs scheduled speedtests against Ookla's servers
// (speedtest-go, no CLI), stores the results and alerts when a run comes in
// under the thresholds.
package speedtest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	ookla "github.com/showwin/speedtest-go/speedtest"

	"github.com/audemed44/lookout/internal/cron"
	"github.com/audemed44/lookout/internal/notify"
	"github.com/audemed44/lookout/internal/store"
)

// Measure runs one speedtest; tests replace it.
var Measure = measure

type Runner struct {
	Store    *store.Store
	Notifier *notify.Notifier
	Now      func() time.Time

	mu      sync.Mutex
	running bool
	started time.Time
	next    time.Time
}

func New(s *store.Store, n *notify.Notifier) *Runner {
	return &Runner{Store: s, Notifier: n, Now: time.Now}
}

var ErrRunning = errors.New("a speedtest is already running")

// Status reports whether a test is running, since when, and the next
// scheduled one.
func (r *Runner) Status() (running bool, started, next time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running, r.started, r.next
}

// Start runs a test in the background.
func (r *Runner) Start(source string) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return ErrRunning
	}
	r.running, r.started = true, r.Now()
	r.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		r.run(ctx, source)
	}()
	return nil
}

func (r *Runner) run(ctx context.Context, source string) store.Speedtest {
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()
	settings, _ := r.Store.Settings(ctx)
	res, err := Measure(ctx, settings.Speedtest.ServerID)
	res.Time, res.Source = r.Now(), source
	if err != nil {
		res.Error = err.Error()
		slog.Warn("speedtest failed", "err", err)
	}
	if err := r.Store.AddSpeedtest(ctx, &res); err != nil {
		slog.Warn("could not save a speedtest", "err", err)
	}
	if note, ok := Judge(res, settings.Speedtest); ok && r.Notifier != nil {
		if _, err := r.Notifier.Notify(context.Background(), note); err != nil {
			slog.Warn("notify", "err", err)
		}
	}
	return res
}

// Judge says whether a result deserves a notification: a failed run, or
// one under the thresholds.
func Judge(res store.Speedtest, cfg store.SpeedtestSettings) (notify.Note, bool) {
	note := notify.Note{Type: store.Warning, Source: "speedtest", Tags: []string{"speedtest"}}
	if res.Error != "" {
		note.Title, note.Body = "Speedtest failed", res.Error
		return note, true
	}
	var low []string
	if cfg.MinDownload > 0 && res.Download < cfg.MinDownload {
		low = append(low, fmt.Sprintf("download %.0f Mbit/s (under %.0f)", res.Download, cfg.MinDownload))
	}
	if cfg.MinUpload > 0 && res.Upload < cfg.MinUpload {
		low = append(low, fmt.Sprintf("upload %.0f Mbit/s (under %.0f)", res.Upload, cfg.MinUpload))
	}
	if cfg.MaxPing > 0 && res.Ping > cfg.MaxPing {
		low = append(low, fmt.Sprintf("ping %.0f ms (over %.0f)", res.Ping, cfg.MaxPing))
	}
	if len(low) == 0 {
		return note, false
	}
	note.Title = "Slow internet"
	note.Body = strings.Join(low, ", ")
	if res.Server != "" {
		note.Body += "\nvia " + res.Server
	}
	return note, true
}

// Schedule runs tests on the configured cron schedule until ctx ends. The
// schedule is re-read every minute, so edits apply without a restart.
func (r *Runner) Schedule(ctx context.Context) {
	var spec string
	var sched *cron.Schedule
	for {
		settings, err := r.Store.Settings(ctx)
		if err == nil && settings.Speedtest.Enabled {
			if settings.Speedtest.Schedule != spec || sched == nil {
				spec = settings.Speedtest.Schedule
				sched, err = cron.Parse(spec)
				if err != nil {
					slog.Warn("speedtest schedule", "err", err)
				}
				r.setNext(time.Time{})
			}
		} else {
			sched, spec = nil, ""
			r.setNext(time.Time{})
		}
		if sched != nil {
			r.mu.Lock()
			if r.next.IsZero() {
				r.next = sched.Next(r.Now())
			}
			due := !r.next.After(r.Now())
			r.mu.Unlock()
			if due {
				r.setNext(sched.Next(r.Now()))
				if err := r.Start("scheduled"); err != nil {
					slog.Info("skipped a scheduled speedtest", "err", err)
				}
			}
		}
		wait := time.Minute
		if _, _, next := r.Status(); !next.IsZero() {
			wait = min(wait, max(time.Second, next.Sub(r.Now())))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (r *Runner) setNext(t time.Time) {
	r.mu.Lock()
	r.next = t
	r.mu.Unlock()
}

func measure(ctx context.Context, serverID string) (store.Speedtest, error) {
	var out store.Speedtest
	client := ookla.New()
	if user, err := client.FetchUserInfoContext(ctx); err == nil && user.Isp != "Unknown" {
		out.ISP = user.Isp
	}
	var server *ookla.Server
	if serverID != "" {
		s, err := client.FetchServerByIDContext(ctx, serverID)
		if err != nil {
			return out, fmt.Errorf("server %s: %v", serverID, err)
		}
		server = s
	} else {
		list, err := client.FetchServerListContext(ctx)
		if err != nil {
			return out, fmt.Errorf("could not list servers: %v", err)
		}
		// The list comes sorted by distance; take the closest few and keep
		// the one that answers fastest.
		if len(list) > 5 {
			list = list[:5]
		}
		for _, s := range list {
			_ = s.PingTestContext(ctx, nil)
		}
		best, err := list.FindServer(nil)
		if err != nil || len(best) == 0 {
			return out, fmt.Errorf("no speedtest server answered")
		}
		server = best[0]
	}
	out.Server = strings.TrimSpace(server.Sponsor + " · " + server.Name)
	out.ServerID = server.ID
	if err := server.PingTestContext(ctx, nil); err != nil {
		return out, fmt.Errorf("ping: %v", err)
	}
	if err := server.DownloadTestContext(ctx); err != nil {
		return out, fmt.Errorf("download: %v", err)
	}
	if err := server.UploadTestContext(ctx); err != nil {
		return out, fmt.Errorf("upload: %v", err)
	}
	out.Ping = float64(server.Latency.Microseconds()) / 1000
	out.Jitter = float64(server.Jitter.Microseconds()) / 1000
	out.Download = server.DLSpeed.Mbps()
	out.Upload = server.ULSpeed.Mbps()
	server.Context.Reset()
	return out, nil
}
