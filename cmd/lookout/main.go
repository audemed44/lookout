// Command lookout watches services, takes heartbeats, runs speedtests and
// delivers notifications, from one small binary.
package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // the runtime image may have no zoneinfo; TZ needs this

	"github.com/audemed44/lookout/internal/checks"
	"github.com/audemed44/lookout/internal/discovery"
	"github.com/audemed44/lookout/internal/notify"
	"github.com/audemed44/lookout/internal/server"
	"github.com/audemed44/lookout/internal/speedtest"
	"github.com/audemed44/lookout/internal/store"
	"github.com/audemed44/lookout/web"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	level := slog.LevelInfo
	if os.Getenv("LOOKOUT_DEBUG") != "" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	token := os.Getenv("LOOKOUT_TOKEN")
	if token == "" {
		slog.Error("set LOOKOUT_TOKEN: it's what you sign in with, and what Foyer uses for the widget")
		os.Exit(1)
	}
	dataDir := env("LOOKOUT_DATA_DIR", "/data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		slog.Error("could not create the data folder", "err", err)
		os.Exit(1)
	}
	db, err := store.Open(filepath.Join(dataDir, "lookout.db"))
	if err != nil {
		slog.Error("could not open the database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	var dock *checks.DockerClient
	if sock := env("LOOKOUT_DOCKER_SOCKET", "/var/run/docker.sock"); fileExists(sock) {
		dock = checks.NewDocker(sock)
		checks.Docker = dock
	}
	var proxy discovery.Source
	if u := os.Getenv("LOOKOUT_GATEHOUSE_URL"); u != "" {
		proxy = &discovery.Gatehouse{URL: u, Token: os.Getenv("LOOKOUT_GATEHOUSE_TOKEN")}
	} else if u := os.Getenv("LOOKOUT_NPM_URL"); u != "" {
		proxy = &discovery.NPM{URL: u, Email: os.Getenv("LOOKOUT_NPM_EMAIL"), Password: os.Getenv("LOOKOUT_NPM_PASSWORD")}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	notifier := notify.New(db)
	engine := checks.NewEngine(db, notifier)
	runner := speedtest.New(db, notifier)
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(err)
	}
	app := server.New(server.Options{
		Store: db, Engine: engine, Notifier: notifier, Speedtest: runner, Docker: dock,
		Proxy: proxy, Token: token, FoyerURL: foyerURL(), DataDir: dataDir, Web: dist,
	})
	if err := engine.Start(ctx); err != nil {
		slog.Error("could not start the checks", "err", err)
		os.Exit(1)
	}
	app.LoadFile(ctx, env("LOOKOUT_CONFIG", filepath.Join(dataDir, "lookout.yaml")))
	go notifier.Run(ctx)
	go runner.Schedule(ctx)
	go app.RunDiscovery(ctx)
	go prune(ctx, db)

	srv := &http.Server{
		Addr:              ":" + env("LOOKOUT_PORT", "8080"),
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("lookout listening", "addr", srv.Addr, "checks", len(engine.Views()), "docker", dock != nil, "proxy", proxy != nil)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// prune drops old history every hour.
func prune(ctx context.Context, db *store.Store) {
	for {
		if settings, err := db.Settings(ctx); err == nil {
			if err := db.Prune(ctx, settings.Retention); err != nil {
				slog.Warn("prune", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func healthcheck() int {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + env("LOOKOUT_PORT", "8080") + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return 1
	}
	return 0
}

// foyerURL is HOMEPAGE_URL, the link back to Foyer in the header, when
// it's an http(s) address.
func foyerURL() string {
	u := os.Getenv("HOMEPAGE_URL")
	if u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		slog.Warn("HOMEPAGE_URL isn't an http(s) address; ignoring it", "url", u)
		return ""
	}
	return u
}
