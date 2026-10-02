package server

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/audemed44/lookout/internal/checks"
	"github.com/audemed44/lookout/internal/config"
	"github.com/audemed44/lookout/internal/store"
)

// CheckRow is a check as the lists show it.
type CheckRow struct {
	checks.View
	// Uptime over 24 h, 7 d and 30 d in percent; -1 without data.
	Uptime []float64 `json:"uptime"`
	// Spark is the latest latencies, oldest first; -1 marks a failure.
	Spark []float64 `json:"spark"`
}

func (s *Server) rows(ctx context.Context) ([]CheckRow, error) {
	now := time.Now()
	uptime, err := s.Store.Uptime(ctx, now.Add(-24*time.Hour), now.AddDate(0, 0, -7), now.AddDate(0, 0, -30))
	if err != nil {
		return nil, err
	}
	spark, err := s.Store.Sparklines(ctx, 40)
	if err != nil {
		return nil, err
	}
	views := s.Engine.Views()
	out := make([]CheckRow, len(views))
	for i, v := range views {
		out[i] = CheckRow{View: v, Uptime: uptime[v.Check.ID], Spark: spark[v.Check.ID]}
		if out[i].Uptime == nil {
			out[i].Uptime = []float64{-1, -1, -1}
		}
		if out[i].Spark == nil {
			out[i].Spark = []float64{}
		}
	}
	return out, nil
}

type Counts struct {
	Total   int `json:"total"`
	Up      int `json:"up"`
	Down    int `json:"down"`
	Pending int `json:"pending"`
	Paused  int `json:"paused"`
	Other   int `json:"other"` // unknown, maintenance
	// CertDays is the fewest days left on any watched certificate (-1: none).
	CertDays  int    `json:"cert_days"`
	CertCheck string `json:"cert_check,omitempty"`
}

func count(rows []CheckRow) Counts {
	c := Counts{CertDays: -1}
	for _, r := range rows {
		c.Total++
		switch r.Status {
		case store.Up, store.Running, store.Asleep:
			c.Up++
		case store.Down:
			c.Down++
		case store.Pending:
			c.Pending++
		case store.Paused:
			c.Paused++
		default:
			c.Other++
		}
		if !r.State.CertExpires.IsZero() && r.Check.CertDays >= 0 && !r.Check.Paused {
			d := int(time.Until(r.State.CertExpires).Hours() / 24)
			if c.CertDays < 0 || d < c.CertDays {
				c.CertDays, c.CertCheck = d, r.Check.Name
			}
		}
	}
	return c
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	rows, err := s.rows(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	latest, err := s.Store.LatestSpeedtest(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	incidents, err := s.Store.Incidents(r.Context(), 0, 8)
	if err != nil {
		storeError(w, err)
		return
	}
	queued, _ := s.Store.QueuedCount(r.Context())
	running, _, next := s.Speedtest.Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"checks":    rows,
		"counts":    count(rows),
		"incidents": incidents,
		"speedtest": map[string]any{"latest": latest, "running": running, "next": next},
		"queued":    queued,
	})
}

func (s *Server) listChecks(w http.ResponseWriter, r *http.Request) {
	rows, err := s.rows(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) getCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	v, ok := s.Engine.View(id)
	if !ok {
		writeError(w, http.StatusNotFound, "no such check")
		return
	}
	now := time.Now()
	ctx := r.Context()
	uptime, err := s.Store.Uptime(ctx, now.Add(-24*time.Hour), now.AddDate(0, 0, -7), now.AddDate(0, 0, -30), now.AddDate(0, 0, -365))
	if err != nil {
		storeError(w, err)
		return
	}
	recent, err := s.Store.Recent(ctx, id, 30)
	if err != nil {
		storeError(w, err)
		return
	}
	slices.Reverse(recent)
	incidents, err := s.Store.Incidents(ctx, id, 50)
	if err != nil {
		storeError(w, err)
		return
	}
	up := uptime[id]
	if up == nil {
		up = []float64{-1, -1, -1, -1}
	}
	writeJSON(w, http.StatusOK, map[string]any{"view": v, "uptime": up, "recent": recent, "incidents": incidents})
}

// Point is one sample on a check's chart: a raw result over the last day,
// an hour beyond that.
type Point struct {
	Time    time.Time `json:"t"`
	Latency float64   `json:"latency"` // ms, 0 when unknown
	Max     float64   `json:"max"`
	Uptime  float64   `json:"uptime"` // percent
	Message string    `json:"message,omitempty"`
}

var ranges = map[string]time.Duration{
	"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour,
}

func (s *Server) checkHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rng := r.URL.Query().Get("range")
	span, ok := ranges[rng]
	if !ok {
		rng, span = "24h", ranges["24h"]
	}
	since := time.Now().Add(-span)
	points := []Point{}
	if rng == "24h" {
		results, err := s.Store.Recent(r.Context(), id, 5000)
		if err != nil {
			storeError(w, err)
			return
		}
		for _, res := range results {
			if res.Time.Before(since) {
				continue
			}
			p := Point{Time: res.Time, Latency: res.Latency, Max: res.Latency}
			if res.OK {
				p.Uptime = 100
			} else {
				p.Message = res.Message
			}
			points = append(points, p)
		}
	} else {
		rolls, err := s.Store.Rollups(r.Context(), id, since)
		if err != nil {
			storeError(w, err)
			return
		}
		for _, ru := range rolls {
			p := Point{Time: ru.Time, Latency: ru.Latency, Max: ru.Max}
			if ru.Total > 0 {
				p.Uptime = 100 * float64(ru.Up) / float64(ru.Total)
			}
			points = append(points, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": rng, "points": points})
}

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Incidents(r.Context(), 0, 100)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createCheck(w http.ResponseWriter, r *http.Request) {
	var c store.Check
	if !readJSON(w, r, 64<<10, &c) {
		return
	}
	c.ID, c.Source, c.SourceKey = 0, "", ""
	if err := config.Validate(&c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.SaveCheck(r.Context(), &c); err != nil {
		storeError(w, err)
		return
	}
	s.Engine.Upsert(c)
	v, _ := s.Engine.View(c.ID)
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	old, err := s.Store.Check(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	var c store.Check
	if !readJSON(w, r, 64<<10, &c) {
		return
	}
	c.ID, c.Source, c.SourceKey = id, old.Source, old.SourceKey
	if err := config.Validate(&c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.SaveCheck(r.Context(), &c); err != nil {
		storeError(w, err)
		return
	}
	s.Engine.Upsert(c)
	v, _ := s.Engine.View(c.ID)
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) deleteCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := s.Store.Check(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	if err := s.Store.DeleteCheck(r.Context(), id); err != nil {
		storeError(w, err)
		return
	}
	s.Engine.Remove(id)
	// A discovered check would come back on the next sync; deleting it
	// means "don't watch this domain".
	if c.Source == "npm" && c.SourceKey != "" {
		if err := s.ignore(r.Context(), c.SourceKey, true); err != nil {
			storeError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := s.Engine.RunNow(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			storeError(w, err)
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, outcomeJSON(out))
}

func outcomeJSON(o checks.Outcome) map[string]any {
	out := map[string]any{"ok": o.OK, "latency": float64(o.Latency.Microseconds()) / 1000, "message": o.Message}
	if !o.CertExpires.IsZero() {
		out["cert_expires"] = o.CertExpires
	}
	return out
}

// testCheck probes a check that isn't saved, for the form's Test button.
func (s *Server) testCheck(w http.ResponseWriter, r *http.Request) {
	var c store.Check
	if !readJSON(w, r, 64<<10, &c) {
		return
	}
	if err := config.Validate(&c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if c.Type == store.Push {
		writeError(w, http.StatusBadRequest, "heartbeats can't be tested from here: ping their URL")
		return
	}
	writeJSON(w, http.StatusOK, outcomeJSON(checks.Probe(r.Context(), c)))
}

func (s *Server) pauseCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Paused bool `json:"paused"`
	}
	if !readJSON(w, r, 1<<10, &body) {
		return
	}
	c, err := s.Store.Check(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	c.Paused = body.Paused
	if err := s.Store.SaveCheck(r.Context(), &c); err != nil {
		storeError(w, err)
		return
	}
	s.Engine.Upsert(c)
	v, _ := s.Engine.View(id)
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) listContainers(w http.ResponseWriter, r *http.Request) {
	if s.Docker == nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	list, err := s.Docker.Containers(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	slices.Sort(list)
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) listWindows(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Windows(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	now := time.Now()
	type row struct {
		store.Window
		Active bool `json:"active"`
	}
	out := make([]row, len(list))
	for i, win := range list {
		out[i] = row{win, win.Active(now)}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) saveWindow(w http.ResponseWriter, r *http.Request) {
	var win store.Window
	if !readJSON(w, r, 16<<10, &win) {
		return
	}
	win.ID = 0
	if r.PathValue("id") != "" {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		win.ID = id
	}
	if win.Name == "" {
		writeError(w, http.StatusBadRequest, "a name is required")
		return
	}
	switch win.Repeat {
	case "":
		if win.Start.IsZero() || (!win.End.IsZero() && !win.End.After(win.Start)) {
			writeError(w, http.StatusBadRequest, "a one-off window needs a start, and an end after it")
			return
		}
	case "daily", "weekly":
		if _, err := time.Parse("15:04", win.From); err != nil || win.Minutes <= 0 {
			writeError(w, http.StatusBadRequest, "a repeating window needs a start time (HH:MM) and a length")
			return
		}
		if win.Repeat == "weekly" && len(win.Weekdays) == 0 {
			writeError(w, http.StatusBadRequest, "pick at least one weekday")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "repeat must be daily, weekly or empty")
		return
	}
	if err := s.Store.SaveWindow(r.Context(), &win); err != nil {
		storeError(w, err)
		return
	}
	s.reloadWindows(r.Context())
	writeJSON(w, http.StatusOK, win)
}

func (s *Server) deleteWindow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteWindow(r.Context(), id); err != nil {
		storeError(w, err)
		return
	}
	s.reloadWindows(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reloadWindows(ctx context.Context) {
	if list, err := s.Store.Windows(ctx); err == nil {
		s.Engine.SetWindows(list)
	}
}
