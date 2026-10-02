package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/audemed44/lookout/internal/store"
)

// Note is a notification on its way in.
type Note struct {
	Title  string
	Body   string
	Type   string
	Tags   []string
	Source string // check, heartbeat, speedtest, or the sender's name
}

// Delivery is what happened for one target, kept in the history.
type Delivery struct {
	Target string    `json:"target"`
	State  string    `json:"state"` // sent, failed, queued
	Error  string    `json:"error,omitempty"`
	Reason string    `json:"reason,omitempty"` // why it was queued: quiet hours, digest
	Until  time.Time `json:"until,omitzero"`
}

type Notifier struct {
	Store *store.Store
	Now   func() time.Time

	mu sync.Mutex // dedupe check and insert happen together
}

func New(s *store.Store) *Notifier {
	return &Notifier{Store: s, Now: time.Now}
}

// Types are the notification types Apprise knows.
var Types = []string{store.Info, store.Success, store.Warning, store.Failure}

// Notify routes, records and delivers a notification. Deliveries that are
// held (quiet hours, digests) go out later from Run.
func (n *Notifier) Notify(ctx context.Context, note Note) (store.Notification, error) {
	if !slices.Contains(Types, note.Type) {
		note.Type = store.Info
	}
	note.Title = strings.TrimSpace(note.Title)
	note.Body = strings.TrimSpace(note.Body)
	if note.Body == "" && note.Title == "" {
		return store.Notification{}, fmt.Errorf("a notification needs a title or a body")
	}
	settings, err := n.Store.Settings(ctx)
	if err != nil {
		return store.Notification{}, err
	}
	now := n.Now()
	rec := store.Notification{
		Time: now, Source: note.Source, Type: note.Type, Title: note.Title, Body: note.Body,
		Tags: dedupeTags(note.Tags),
	}

	n.mu.Lock()
	if mins := settings.Notify.DedupeMinutes; mins > 0 {
		dup, err := n.Store.Duplicate(ctx, rec, now.Add(-time.Duration(mins)*time.Minute))
		if err != nil {
			n.mu.Unlock()
			return rec, err
		}
		if dup {
			rec.Status = store.Suppressed
			rec.Detail = fmt.Sprintf("Same as one sent in the last %d min", mins)
			err := n.Store.AddNotification(ctx, &rec)
			n.mu.Unlock()
			return rec, err
		}
	}
	plan, err := n.plan(ctx, rec, settings.Notify, now)
	if err != nil {
		n.mu.Unlock()
		return rec, err
	}
	if len(plan) == 0 {
		rec.Status = store.NoRoute
		rec.Detail = "No target matched"
		err := n.Store.AddNotification(ctx, &rec)
		n.mu.Unlock()
		return rec, err
	}
	rec.Status = store.Queued
	if err := n.Store.AddNotification(ctx, &rec); err != nil {
		n.mu.Unlock()
		return rec, err
	}
	n.mu.Unlock()

	deliveries := make([]Delivery, len(plan))
	var wg sync.WaitGroup
	for i, p := range plan {
		deliveries[i] = Delivery{Target: p.target.Name}
		if !p.until.IsZero() {
			deliveries[i].State, deliveries[i].Reason, deliveries[i].Until = store.Queued, p.reason, p.until
			if err := n.Store.Enqueue(ctx, rec.ID, p.target.ID, p.until, p.reason); err != nil {
				return rec, err
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			deliveries[i].State = store.Sent
			if err := deliver(ctx, p.target, Message{Title: rec.Title, Body: rec.Body, Type: rec.Type}); err != nil {
				deliveries[i].State, deliveries[i].Error = store.Failed, err.Error()
				slog.Warn("notification failed", "target", p.target.Name, "err", err)
			}
		}()
	}
	wg.Wait()
	rec.Status = summarize(deliveries)
	raw, _ := json.Marshal(deliveries)
	rec.Detail = string(raw)
	return rec, n.Store.UpdateNotification(ctx, rec.ID, rec.Status, rec.Detail)
}

type planned struct {
	target store.Target
	until  time.Time // zero: send now
	reason string
}

// plan picks the targets for a notification, and when each gets it.
func (n *Notifier) plan(ctx context.Context, rec store.Notification, cfg store.NotifySettings, now time.Time) ([]planned, error) {
	targets, err := n.Store.Targets(ctx)
	if err != nil {
		return nil, err
	}
	routes, err := n.Store.Routes(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]store.Target{}
	for _, t := range targets {
		if t.Enabled {
			byName[t.Name] = t
		}
	}
	// digest[name] is false once any matching route wants it sent now.
	digest := map[string]bool{}
	var order []string
	add := func(name string, d bool) {
		if _, ok := byName[name]; !ok {
			return
		}
		prev, seen := digest[name]
		if !seen {
			order = append(order, name)
			digest[name] = d
			return
		}
		digest[name] = prev && d
	}
	if len(routes) == 0 {
		for _, t := range targets {
			add(t.Name, false)
		}
	}
	for _, r := range routes {
		if !Matches(r, rec) {
			continue
		}
		for _, name := range r.Targets {
			add(name, r.Digest)
		}
		if r.Stop {
			break
		}
	}
	quiet := cfg.Quiet && rec.Type != store.Failure && InWindow(cfg.QuietOn, cfg.QuietOff, now)
	var out []planned
	for _, name := range order {
		p := planned{target: byName[name]}
		switch {
		case digest[name]:
			p.until, p.reason = NextAt(cfg.DigestAt, now), "digest"
		case quiet:
			p.until, p.reason = NextAt(cfg.QuietOff, now), "quiet hours"
		}
		out = append(out, p)
	}
	return out, nil
}

// Matches reports whether a route takes a notification.
func Matches(r store.Route, rec store.Notification) bool {
	if store.TypeRank(rec.Type) < store.TypeRank(r.MinType) {
		return false
	}
	if len(r.Tags) == 0 {
		return true
	}
	for _, t := range r.Tags {
		if slices.Contains(rec.Tags, t) {
			return true
		}
	}
	return false
}

func summarize(ds []Delivery) string {
	var sent, failed, queued int
	for _, d := range ds {
		switch d.State {
		case store.Sent:
			sent++
		case store.Failed:
			failed++
		default:
			queued++
		}
	}
	switch {
	case failed > 0 && sent+queued == 0:
		return store.Failed
	case failed > 0:
		return store.Partial
	case sent == 0:
		return store.Queued
	}
	return store.Sent
}

func deliver(ctx context.Context, t store.Target, m Message) error {
	if missing := MissingEnv(t.URL); len(missing) > 0 {
		return fmt.Errorf("%s isn't set", strings.Join(missing, ", "))
	}
	s, err := Parse(Expand(t.URL))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.Send(ctx, m)
}

// Test sends a test message straight to a target, skipping routes.
func (n *Notifier) Test(ctx context.Context, t store.Target) error {
	return deliver(ctx, t, Message{
		Title: "Lookout test",
		Body:  "Notifications to " + t.Name + " work.",
		Type:  store.Info,
	})
}

// Run sends held notifications when they're due.
func (n *Notifier) Run(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		n.Flush(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Flush sends whatever is due: one message per target, a summary when
// several are waiting.
func (n *Notifier) Flush(ctx context.Context) { n.flush(ctx, n.Now()) }

// FlushAll sends everything held, due or not.
func (n *Notifier) FlushAll(ctx context.Context) { n.flush(ctx, n.Now().AddDate(100, 0, 0)) }

func (n *Notifier) flush(ctx context.Context, until time.Time) {
	due, err := n.Store.Due(ctx, until)
	if err != nil {
		slog.Warn("notification queue", "err", err)
		return
	}
	if len(due) == 0 {
		return
	}
	targets, err := n.Store.Targets(ctx)
	if err != nil {
		return
	}
	byID := map[int64]store.Target{}
	for _, t := range targets {
		byID[t.ID] = t
	}
	updated := map[int64]*store.Notification{}
	for tid, notes := range due {
		t, ok := byID[tid]
		if !ok {
			continue
		}
		err := deliver(ctx, t, Combine(notes))
		for _, note := range notes {
			rec := updated[note.ID]
			if rec == nil {
				c := note
				rec = &c
				updated[note.ID] = rec
			}
			var ds []Delivery
			_ = json.Unmarshal([]byte(rec.Detail), &ds)
			for i := range ds {
				if ds[i].Target == t.Name && ds[i].State == store.Queued {
					ds[i].State, ds[i].Error = store.Sent, ""
					if err != nil {
						ds[i].State, ds[i].Error = store.Failed, err.Error()
					}
				}
			}
			raw, _ := json.Marshal(ds)
			rec.Detail = string(raw)
			rec.Status = summarize(ds)
		}
	}
	for id, rec := range updated {
		_ = n.Store.UpdateNotification(ctx, id, rec.Status, rec.Detail)
	}
}

// Combine makes one message out of several held ones.
func Combine(notes []store.Notification) Message {
	if len(notes) == 1 {
		return Message{Title: notes[0].Title, Body: notes[0].Body, Type: notes[0].Type}
	}
	worst := store.Info
	var lines []string
	for _, n := range notes {
		if store.TypeRank(n.Type) > store.TypeRank(worst) {
			worst = n.Type
		}
		line := "• " + n.Time.Format("15:04") + " " + n.Title
		if first, _, _ := strings.Cut(n.Body, "\n"); first != "" && first != n.Title {
			line += " — " + first
		}
		lines = append(lines, line)
	}
	return Message{
		Title: fmt.Sprintf("Lookout: %d notifications", len(notes)),
		Body:  strings.Join(lines, "\n"),
		Type:  worst,
	}
}

// InWindow reports whether now's clock time is in [from, to), which may
// wrap past midnight.
func InWindow(from, to string, now time.Time) bool {
	f, ok1 := minutes(from)
	t, ok2 := minutes(to)
	if !ok1 || !ok2 || f == t {
		return false
	}
	m := now.Hour()*60 + now.Minute()
	if f < t {
		return m >= f && m < t
	}
	return m >= f || m < t
}

// NextAt is the next time the clock shows hhmm, after now.
func NextAt(hhmm string, now time.Time) time.Time {
	m, ok := minutes(hhmm)
	if !ok {
		return now
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), m/60, m%60, 0, 0, now.Location())
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at
}

func minutes(hhmm string) (int, bool) {
	t, err := time.Parse("15:04", strings.TrimSpace(hhmm))
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

func dedupeTags(tags []string) []string {
	out := []string{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// ParseTags splits an Apprise tag string ("a, b" or "a b") into tags.
func ParseTags(s string) []string {
	return dedupeTags(strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }))
}
