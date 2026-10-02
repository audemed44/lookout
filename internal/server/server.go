// Package server exposes Lookout's JSON API, the Apprise-compatible
// /notify endpoint, heartbeat pings, and the built frontend.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/audemed44/lookout/internal/checks"
	"github.com/audemed44/lookout/internal/discovery"
	"github.com/audemed44/lookout/internal/notify"
	"github.com/audemed44/lookout/internal/speedtest"
	"github.com/audemed44/lookout/internal/store"
)

type Options struct {
	Store     *store.Store
	Engine    *checks.Engine
	Notifier  *notify.Notifier
	Speedtest *speedtest.Runner
	Docker    *checks.DockerClient // nil without a socket
	// Proxy is where discovery reads routes from; nil when not configured.
	Proxy   discovery.Source
	Token   string
	DataDir string // for uploads being imported
	Web     fs.FS
}

type Server struct {
	Options
	session string // cookie value for a signed-in browser
	disc    discState
}

func New(o Options) *Server {
	return &Server{Options: o, session: sessionValue(o.Token)}
}

func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/overview", s.overview)
	api.HandleFunc("GET /api/checks", s.listChecks)
	api.HandleFunc("POST /api/checks", s.createCheck)
	api.HandleFunc("POST /api/checks/test", s.testCheck)
	api.HandleFunc("GET /api/checks/{id}", s.getCheck)
	api.HandleFunc("PUT /api/checks/{id}", s.updateCheck)
	api.HandleFunc("DELETE /api/checks/{id}", s.deleteCheck)
	api.HandleFunc("POST /api/checks/{id}/run", s.runCheck)
	api.HandleFunc("POST /api/checks/{id}/pause", s.pauseCheck)
	api.HandleFunc("GET /api/checks/{id}/history", s.checkHistory)
	api.HandleFunc("GET /api/incidents", s.listIncidents)
	api.HandleFunc("GET /api/containers", s.listContainers)

	api.HandleFunc("GET /api/maintenance", s.listWindows)
	api.HandleFunc("POST /api/maintenance", s.saveWindow)
	api.HandleFunc("PUT /api/maintenance/{id}", s.saveWindow)
	api.HandleFunc("DELETE /api/maintenance/{id}", s.deleteWindow)

	api.HandleFunc("GET /api/notifications", s.listNotifications)
	api.HandleFunc("POST /api/notifications/flush", s.flushQueue)
	api.HandleFunc("GET /api/targets", s.listTargets)
	api.HandleFunc("POST /api/targets", s.saveTarget)
	api.HandleFunc("PUT /api/targets/{id}", s.saveTarget)
	api.HandleFunc("DELETE /api/targets/{id}", s.deleteTarget)
	api.HandleFunc("POST /api/targets/{id}/test", s.testTarget)
	api.HandleFunc("GET /api/routes", s.listRoutes)
	api.HandleFunc("POST /api/routes", s.saveRoute)
	api.HandleFunc("PUT /api/routes/{id}", s.saveRoute)
	api.HandleFunc("DELETE /api/routes/{id}", s.deleteRoute)
	api.HandleFunc("GET /api/senders", s.listSenders)
	api.HandleFunc("POST /api/senders", s.saveSender)
	api.HandleFunc("DELETE /api/senders/{key}", s.deleteSender)

	api.HandleFunc("GET /api/speedtests", s.listSpeedtests)
	api.HandleFunc("POST /api/speedtests/run", s.runSpeedtest)
	api.HandleFunc("DELETE /api/speedtests/{id}", s.deleteSpeedtest)

	api.HandleFunc("GET /api/settings", s.getSettings)
	api.HandleFunc("PUT /api/settings", s.putSettings)
	api.HandleFunc("GET /api/discovery", s.getDiscovery)
	api.HandleFunc("POST /api/discovery/sync", s.syncDiscovery)
	api.HandleFunc("POST /api/discovery/ignore", s.ignoreDomain)
	api.HandleFunc("POST /api/import/kuma", s.importKuma)
	api.HandleFunc("POST /api/import/speedtest-tracker", s.importSpeedtestTracker)
	api.HandleFunc("GET /api/config", s.exportConfig)
	api.HandleFunc("POST /api/config", s.importConfig)

	api.HandleFunc("GET /api/foyer/widget", s.foyerWidget)
	api.HandleFunc("POST /api/foyer/speedtest", s.foyerSpeedtest)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/session", s.getSession)
	mux.HandleFunc("POST /api/session", s.login)
	mux.HandleFunc("DELETE /api/session", s.logout)
	mux.HandleFunc("GET /api/public/status", s.publicStatus)
	mux.Handle("/api/", s.requireAuth(api))

	// For other apps and jobs, outside the token: a notify key or a ping
	// token is the credential.
	mux.HandleFunc("POST /notify", s.notifyHandler)
	mux.HandleFunc("POST /notify/{key}", s.notifyHandler)
	for _, m := range []string{"GET", "POST", "HEAD"} {
		mux.HandleFunc(m+" /ping/{token}", s.ping)
		mux.HandleFunc(m+" /ping/{token}/{kind}", s.ping)
	}

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", s.spa())
	return securityHeaders(sameOrigin(mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// storeError answers 404 for missing things and 500 otherwise.
func storeError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	slog.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, err.Error())
}

// readJSON decodes a JSON body of at most limit bytes.
func readJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "expected JSON")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not found")
		return 0, false
	}
	return id, true
}

// spa serves the built frontend, falling back to index.html for app routes.
func (s *Server) spa() http.Handler {
	files := http.FileServer(http.FS(s.Web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if info, err := fs.Stat(s.Web, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(s.Web, "index.html")
		if err != nil {
			http.Error(w, "frontend not built", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
