package server

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/audemed44/lookout/internal/checks"
	"github.com/audemed44/lookout/internal/store"
)

// Lookout serves a card in the Foyer widget format
// (https://github.com/audemed44/foyer/blob/main/docs/app-widgets.md): how
// many checks are up, the failing ones, certificates and the latest
// speedtest, with a button to run one. It replaces Foyer's Uptime Kuma and
// Speedtest widgets.

type foyerStat struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	Unit    string `json:"unit,omitempty"`
	Caption string `json:"caption,omitempty"`
	Tone    string `json:"tone,omitempty"`
}

type foyerAction struct {
	Label   string `json:"label"`
	URL     string `json:"url"`
	Confirm string `json:"confirm,omitempty"`
}

type foyerItem struct {
	Title    string       `json:"title"`
	Subtitle string       `json:"subtitle,omitempty"`
	Caption  string       `json:"caption,omitempty"`
	URL      string       `json:"url,omitempty"`
	Action   *foyerAction `json:"action,omitempty"`
}

type foyerWidget struct {
	Version     int         `json:"version"`
	Stats       []foyerStat `json:"stats"`
	ItemsTitle  string      `json:"items_title,omitempty"`
	ItemsLayout string      `json:"items_layout,omitempty"`
	Items       []foyerItem `json:"items"`
}

func (s *Server) foyerWidget(w http.ResponseWriter, r *http.Request) {
	rows, err := s.rows(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	c := count(rows)
	out := foyerWidget{Version: 1, Items: []foyerItem{}}
	upTone := "good"
	if c.Down > 0 {
		upTone = "bad"
	} else if c.Pending > 0 {
		upTone = "warn"
	}
	active := c.Total - c.Paused
	out.Stats = append(out.Stats, foyerStat{Label: "Up", Value: strconv.Itoa(c.Up), Unit: "/" + strconv.Itoa(active), Caption: "checks", Tone: upTone})
	downTone := ""
	if c.Down > 0 {
		downTone = "bad"
	}
	out.Stats = append(out.Stats, foyerStat{Label: "Down", Value: strconv.Itoa(c.Down), Caption: plural(c.Pending, "retrying", "retrying"), Tone: downTone})
	if c.CertDays >= 0 {
		tone := ""
		if c.CertDays <= 7 {
			tone = "bad"
		} else if c.CertDays <= 14 {
			tone = "warn"
		}
		out.Stats = append(out.Stats, foyerStat{Label: "Certificates", Value: strconv.Itoa(c.CertDays), Unit: "d", Caption: c.CertCheck, Tone: tone})
	}
	if latest, err := s.Store.LatestSpeedtest(r.Context()); err == nil && latest != nil {
		out.Stats = append(out.Stats,
			foyerStat{Label: "Download", Value: fmt.Sprintf("%.0f", latest.Download), Unit: "Mbit/s", Caption: ago(latest.Time)},
			foyerStat{Label: "Upload", Value: fmt.Sprintf("%.0f", latest.Upload), Unit: "Mbit/s", Caption: fmt.Sprintf("ping %.0f ms", latest.Ping)},
		)
	}

	var failing []CheckRow
	for _, row := range rows {
		if row.Status == store.Down || row.Status == store.Pending {
			failing = append(failing, row)
		}
	}
	slices.SortFunc(failing, func(a, b CheckRow) int { return a.State.Since.Compare(b.State.Since) })
	if len(failing) > 0 {
		out.ItemsTitle, out.ItemsLayout = "Failing", "list"
		for _, row := range failing {
			item := foyerItem{
				Title:    row.Check.Name,
				Subtitle: row.State.Message,
				Caption:  row.Status + " for " + checks.Human(time.Since(row.State.Since)),
				URL:      "/checks/" + strconv.FormatInt(row.Check.ID, 10),
			}
			out.Items = append(out.Items, item)
			if len(out.Items) == 12 {
				break
			}
		}
	} else {
		out.ItemsTitle, out.ItemsLayout = "Speedtest", "list"
		item := foyerItem{Title: "All checks up", Subtitle: "Run a speedtest now", URL: "/speedtests",
			Action: &foyerAction{Label: "Run speedtest", URL: "/api/foyer/speedtest"}}
		if running, started, _ := s.Speedtest.Status(); running {
			item.Subtitle = "Speedtest running since " + started.Format("15:04")
			item.Action = nil
		}
		out.Items = append(out.Items, item)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) foyerSpeedtest(w http.ResponseWriter, _ *http.Request) {
	if err := s.Speedtest.Start("manual"); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "Speedtest started: results in about a minute", "url": "/speedtests"})
}

func plural(n int, one, many string) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
