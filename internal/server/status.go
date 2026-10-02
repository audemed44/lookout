package server

import (
	"net/http"
	"slices"
	"time"

	"github.com/audemed44/lookout/internal/store"
)

// The status page is read-only and needs no token, so it's off unless
// turned on in Settings. It shows names and status only: no targets, no
// messages.

type statusCheck struct {
	Name   string    `json:"name"`
	Group  string    `json:"group,omitempty"`
	Status string    `json:"status"`
	Since  time.Time `json:"since"`
	Uptime float64   `json:"uptime"` // 30 days, percent; -1 without data
	// Days is uptime per day for the last 30 days, oldest first; -1 for
	// days without results.
	Days []float64 `json:"days"`
}

func (s *Server) publicStatus(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Store.Settings(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	if !settings.StatusPage.Enabled {
		writeError(w, http.StatusNotFound, "the status page is turned off")
		return
	}
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -29)
	out := []statusCheck{}
	var worst string
	for _, v := range s.Engine.Views() {
		if len(settings.StatusPage.Tags) > 0 && !slices.ContainsFunc(v.Check.Tags, func(t string) bool {
			return slices.Contains(settings.StatusPage.Tags, t)
		}) {
			continue
		}
		rolls, err := s.Store.Rollups(r.Context(), v.Check.ID, start)
		if err != nil {
			storeError(w, err)
			return
		}
		sc := statusCheck{Name: v.Check.Name, Group: v.Check.Group, Status: v.Status, Since: v.State.Since, Uptime: -1, Days: make([]float64, 30)}
		up, total := make([]int, 30), make([]int, 30)
		var allUp, all int
		for _, ru := range rolls {
			d := int(ru.Time.Sub(start).Hours() / 24)
			if d < 0 || d >= 30 {
				continue
			}
			up[d] += ru.Up
			total[d] += ru.Total
			allUp += ru.Up
			all += ru.Total
		}
		for d := range sc.Days {
			sc.Days[d] = -1
			if total[d] > 0 {
				sc.Days[d] = 100 * float64(up[d]) / float64(total[d])
			}
		}
		if all > 0 {
			sc.Uptime = 100 * float64(allUp) / float64(all)
		}
		switch {
		case v.Status == store.Down:
			worst = store.Down
		case v.Status == store.Pending && worst != store.Down:
			worst = store.Pending
		}
		out = append(out, sc)
	}
	title := settings.StatusPage.Title
	if title == "" {
		title = "Status"
	}
	if worst == "" {
		worst = store.Up
	}
	writeJSON(w, http.StatusOK, map[string]any{"title": title, "status": worst, "checks": out, "updated": now})
}
