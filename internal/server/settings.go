package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/audemed44/lookout/internal/config"
	"github.com/audemed44/lookout/internal/cron"
	"github.com/audemed44/lookout/internal/discovery"
	"github.com/audemed44/lookout/internal/importer"
	"github.com/audemed44/lookout/internal/store"
)

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.Settings(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var st store.Settings
	if !readJSON(w, r, 32<<10, &st) {
		return
	}
	if _, err := cron.Parse(st.Speedtest.Schedule); st.Speedtest.Schedule != "" && err != nil {
		writeError(w, http.StatusBadRequest, "speedtest schedule: "+err.Error())
		return
	}
	for _, t := range []string{st.Notify.QuietOn, st.Notify.QuietOff, st.Notify.DigestAt} {
		if _, err := time.Parse("15:04", t); t != "" && err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%q isn't a time (HH:MM)", t))
			return
		}
	}
	if err := s.Store.SaveSettings(r.Context(), st); err != nil {
		storeError(w, err)
		return
	}
	s.getSettings(w, r)
}

func (s *Server) listSpeedtests(w http.ResponseWriter, r *http.Request) {
	span, ok := ranges[r.URL.Query().Get("range")]
	if !ok {
		span = ranges["30d"]
	}
	if r.URL.Query().Get("range") == "all" {
		span = 100 * 365 * 24 * time.Hour
	}
	list, err := s.Store.Speedtests(r.Context(), time.Now().Add(-span))
	if err != nil {
		storeError(w, err)
		return
	}
	running, started, next := s.Speedtest.Status()
	writeJSON(w, http.StatusOK, map[string]any{"results": list, "running": running, "started": started, "next": next})
}

func (s *Server) runSpeedtest(w http.ResponseWriter, _ *http.Request) {
	if err := s.Speedtest.Start("manual"); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "Speedtest started"})
}

func (s *Server) deleteSpeedtest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteSpeedtest(r.Context(), id); err != nil {
		storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Discovery.

type discState struct {
	mu      sync.Mutex
	syncing bool
}

type discoveryInfo struct {
	Configured bool              `json:"configured"`
	Source     string            `json:"source,omitempty"`
	Last       *discovery.Result `json:"last,omitempty"`
}

func (s *Server) getDiscovery(w http.ResponseWriter, r *http.Request) {
	info := discoveryInfo{Configured: s.Proxy != nil}
	if s.Proxy != nil {
		info.Source = s.Proxy.Name()
	}
	var last discovery.Result
	if err := s.Store.Get(r.Context(), "discovery_last", &last); err == nil && !last.Time.IsZero() {
		info.Last = &last
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) syncDiscovery(w http.ResponseWriter, r *http.Request) {
	res, err := s.Discover(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

var errNoProxy = errors.New("no proxy configured: set LOOKOUT_NPM_URL, LOOKOUT_NPM_EMAIL and LOOKOUT_NPM_PASSWORD")

// Discover syncs checks with the proxy's routes.
func (s *Server) Discover(ctx context.Context) (discovery.Result, error) {
	if s.Proxy == nil {
		return discovery.Result{}, errNoProxy
	}
	s.disc.mu.Lock()
	if s.disc.syncing {
		s.disc.mu.Unlock()
		return discovery.Result{}, errors.New("a sync is already running")
	}
	s.disc.syncing = true
	s.disc.mu.Unlock()
	defer func() {
		s.disc.mu.Lock()
		s.disc.syncing = false
		s.disc.mu.Unlock()
	}()

	settings, err := s.Store.Settings(ctx)
	if err != nil {
		return discovery.Result{}, err
	}
	existing, err := s.Store.Checks(ctx)
	if err != nil {
		return discovery.Result{}, err
	}
	res, changed, err := discovery.Sync(ctx, s.Proxy, existing, settings.Discovery)
	if err != nil {
		res.Error = err.Error()
		_ = s.Store.Put(ctx, "discovery_last", res)
		return res, err
	}
	for _, c := range changed {
		if err := s.Store.SaveCheck(ctx, &c); err != nil {
			return res, err
		}
		s.Engine.Upsert(c)
	}
	_ = s.Store.Put(ctx, "discovery_last", res)
	if len(res.Added) > 0 {
		slog.Info("discovered domains", "added", res.Added)
	}
	return res, nil
}

// RunDiscovery syncs on the configured interval while auto-sync is on.
func (s *Server) RunDiscovery(ctx context.Context) {
	if s.Proxy == nil {
		return
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		settings, err := s.Store.Settings(ctx)
		if err != nil || !settings.Discovery.Auto {
			continue
		}
		var last discovery.Result
		_ = s.Store.Get(ctx, "discovery_last", &last)
		if time.Since(last.Time) < time.Duration(settings.Discovery.Every)*time.Minute {
			continue
		}
		if _, err := s.Discover(ctx); err != nil {
			slog.Warn("discovery", "err", err)
		}
	}
}

func (s *Server) ignoreDomain(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Domain string `json:"domain"`
		Ignore bool   `json:"ignore"`
	}
	if !readJSON(w, r, 1<<10, &body) {
		return
	}
	if err := s.ignore(r.Context(), body.Domain, body.Ignore); err != nil {
		storeError(w, err)
		return
	}
	s.getSettings(w, r)
}

func (s *Server) ignore(ctx context.Context, domain string, add bool) error {
	st, err := s.Store.Settings(ctx)
	if err != nil {
		return err
	}
	st.Discovery.Ignored = slices.DeleteFunc(st.Discovery.Ignored, func(d string) bool { return d == domain })
	if add {
		st.Discovery.Ignored = append(st.Discovery.Ignored, domain)
		slices.Sort(st.Discovery.Ignored)
	}
	return s.Store.SaveSettings(ctx, st)
}

// Imports.

// upload saves a multipart file field to a temporary file in the data
// folder and returns its path; the caller removes it.
func (s *Server) upload(w http.ResponseWriter, r *http.Request, limit int64) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	f, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "upload the database as the form field \"file\" (at most "+strconv.FormatInt(limit>>20, 10)+" MB)")
		return "", false
	}
	defer f.Close()
	tmp, err := os.CreateTemp(s.DataDir, "import-*.db")
	if err != nil {
		storeError(w, err)
		return "", false
	}
	_, err = io.Copy(tmp, f)
	tmp.Close()
	if err != nil {
		os.Remove(tmp.Name())
		writeError(w, http.StatusBadRequest, "upload failed: "+err.Error())
		return "", false
	}
	return tmp.Name(), true
}

func (s *Server) importKuma(w http.ResponseWriter, r *http.Request) {
	path, ok := s.upload(w, r, 1<<30)
	if !ok {
		return
	}
	defer os.Remove(path)
	rep, err := importer.Kuma(r.Context(), path, s.Store, r.FormValue("history") != "false", s.Engine.Upsert)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) importSpeedtestTracker(w http.ResponseWriter, r *http.Request) {
	path, ok := s.upload(w, r, 1<<30)
	if !ok {
		return
	}
	defer os.Remove(path)
	rep, err := importer.SpeedtestTracker(r.Context(), path, s.Store)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) exportConfig(w http.ResponseWriter, r *http.Request) {
	out, err := config.Export(r.Context(), s.Store)
	if err != nil {
		storeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="lookout.yaml"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
}

func (s *Server) importConfig(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "the file is too large")
		return
	}
	rep, _, err := s.ImportConfig(r.Context(), raw, r.URL.Query().Get("replace") == "true")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// ImportConfig applies a YAML file and tells the scheduler.
func (s *Server) ImportConfig(ctx context.Context, raw []byte, replace bool) (config.Report, config.Changes, error) {
	rep, changes, err := config.Import(ctx, s.Store, raw, replace)
	for _, id := range changes.Removed {
		s.Engine.Remove(id)
	}
	for _, c := range changes.Saved {
		s.Engine.Upsert(c)
	}
	s.reloadWindows(ctx)
	return rep, changes, err
}

// LoadFile imports a YAML file at startup when it's present.
func (s *Server) LoadFile(ctx context.Context, path string) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		slog.Warn("could not read the config file", "path", path, "err", err)
		return
	}
	rep, _, err := s.ImportConfig(ctx, raw, false)
	if err != nil {
		slog.Error("could not import the config file", "path", filepath.Base(path), "err", err)
		return
	}
	slog.Info("imported the config file", "path", path, "added", len(rep.Added), "updated", len(rep.Updated))
	for _, warn := range rep.Warnings {
		slog.Warn("config file", "warning", warn)
	}
}
