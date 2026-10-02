package checks

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/audemed44/lookout/internal/notify"
	"github.com/audemed44/lookout/internal/store"
)

// Flapping: this many up/down changes within flapWindow hold notifications
// until the check has been steady for flapWindow.
const (
	flapChanges = 4
	flapWindow  = 30 * time.Minute
)

// Engine schedules the checks and keeps their state.
type Engine struct {
	Store    *store.Store
	Notifier *notify.Notifier
	Now      func() time.Time

	mu      sync.Mutex
	checks  map[int64]*entry
	windows []store.Window
	asleep  map[string]string // container → Gatehouse sleep state
	ctx     context.Context
	sem     chan struct{} // limits probes running at once
}

type entry struct {
	check  store.Check
	state  store.State
	cancel context.CancelFunc
}

func NewEngine(s *store.Store, n *notify.Notifier) *Engine {
	return &Engine{Store: s, Notifier: n, Now: time.Now, checks: map[int64]*entry{}, sem: make(chan struct{}, 16)}
}

// Start loads the checks and starts running them until ctx ends.
func (e *Engine) Start(ctx context.Context) error {
	list, err := e.Store.Checks(ctx)
	if err != nil {
		return err
	}
	states, err := e.Store.States(ctx)
	if err != nil {
		return err
	}
	windows, err := e.Store.Windows(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.ctx = ctx
	e.windows = windows
	for _, c := range list {
		c.Defaults()
		st, ok := states[c.ID]
		if !ok {
			st = store.State{Status: store.Unknown, Since: e.Now()}
		}
		e.checks[c.ID] = &entry{check: c, state: st}
		e.startLocked(c.ID)
	}
	e.mu.Unlock()
	go e.watchHeartbeats(ctx)
	return nil
}

// startLocked starts a check's loop; e.mu must be held.
func (e *Engine) startLocked(id int64) {
	en := e.checks[id]
	if en.cancel != nil {
		en.cancel()
		en.cancel = nil
	}
	if e.ctx == nil || en.check.Type == store.Push || en.check.Paused {
		return
	}
	ctx, cancel := context.WithCancel(e.ctx)
	en.cancel = cancel
	interval := time.Duration(en.check.Interval) * time.Second
	go func() {
		// Spread the first runs out so a restart doesn't fire them all at once.
		delay := time.Duration(rand.Int64N(int64(min(interval, 15*time.Second)) + 1))
		timer := time.NewTimer(delay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			e.run(ctx, id)
			timer.Reset(interval)
		}
	}()
}

// run probes a check once and records the outcome, unless it's in a
// maintenance window.
func (e *Engine) run(ctx context.Context, id int64) {
	e.mu.Lock()
	en, ok := e.checks[id]
	if !ok {
		e.mu.Unlock()
		return
	}
	c := en.check
	maint := e.inMaintenanceLocked(c, e.Now())
	sleepState, sleeping := "", false
	if c.Type == store.Docker {
		sleepState, sleeping = e.asleep[c.Target]
	}
	e.mu.Unlock()
	if maint {
		return
	}
	if sleeping {
		// Stopped on purpose; Docker would report it exited.
		e.Record(c.ID, asleep(e.Now(), sleepState))
		return
	}
	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	out := Probe(ctx, c)
	<-e.sem
	if ctx.Err() != nil {
		return // stopped or edited meanwhile
	}
	e.Record(c.ID, out)
}

// RunNow probes a check right away and records the result.
func (e *Engine) RunNow(ctx context.Context, id int64) (Outcome, error) {
	e.mu.Lock()
	en, ok := e.checks[id]
	e.mu.Unlock()
	if !ok {
		return Outcome{}, store.ErrNotFound
	}
	if en.check.Type == store.Push {
		return Outcome{}, fmt.Errorf("heartbeats are checked when they ping")
	}
	out := Probe(ctx, en.check)
	e.Record(id, out)
	return out, nil
}

// Record stores an outcome and moves the check's state along.
func (e *Engine) Record(id int64, out Outcome) {
	now := e.Now()
	ctx := context.Background()
	if out.Asleep {
		e.recordAsleep(ctx, id, out, now)
		return
	}
	res := store.Result{Time: now, OK: out.OK, Latency: ms(out.Latency), Message: out.Message}
	if err := e.Store.AddResult(ctx, id, res); err != nil {
		slog.Warn("could not save a result", "check", id, "err", err)
	}
	e.mu.Lock()
	en, ok := e.checks[id]
	if !ok {
		e.mu.Unlock()
		return
	}
	c := en.check
	st := &en.state
	st.Last, st.Latency, st.Message, st.Asleep = now, res.Latency, out.Message, false
	ev := e.apply(c, st, out.OK, out.Message, now)
	if !out.CertExpires.IsZero() {
		ev.notes = append(ev.notes, certNotes(c, st, out.CertExpires, now)...)
	}
	saved := *st
	e.mu.Unlock()

	if ev.opened {
		_ = e.Store.OpenIncident(ctx, id, now, out.Message)
	}
	if ev.closed {
		_ = e.Store.CloseIncident(ctx, id, now)
	}
	if err := e.Store.SaveState(ctx, id, saved); err != nil {
		slog.Warn("could not save state", "check", id, "err", err)
	}
	e.send(c, ev.notes)
}

// recordAsleep notes that the app is asleep. Nothing goes in the history
// and the up/down state machine doesn't move, so there's no alert and no
// dent in uptime.
func (e *Engine) recordAsleep(ctx context.Context, id int64, out Outcome, now time.Time) {
	e.mu.Lock()
	en, ok := e.checks[id]
	if !ok {
		e.mu.Unlock()
		return
	}
	st := &en.state
	st.Last, st.Message, st.Asleep, st.Fails = now, out.Message, true, 0
	saved := *st
	e.mu.Unlock()
	if err := e.Store.SaveState(ctx, id, saved); err != nil {
		slog.Warn("could not save state", "check", id, "err", err)
	}
}

// SetAsleep replaces the containers Gatehouse has put to sleep (container
// name → its state), for Docker checks.
func (e *Engine) SetAsleep(containers map[string]string) {
	e.mu.Lock()
	e.asleep = containers
	e.mu.Unlock()
}

type events struct {
	notes          []notify.Note
	opened, closed bool // incident
}

// apply moves a check's state along after a result; e.mu must be held.
func (e *Engine) apply(c store.Check, st *store.State, ok bool, msg string, now time.Time) events {
	var ev events
	prev, prevSince := st.Status, st.Since
	next := prev
	if ok {
		st.Fails = 0
		next = store.Up
	} else {
		st.Fails++
		switch {
		case st.Fails > c.Retries:
			next = store.Down
		case prev != store.Down:
			next = store.Pending
		}
	}
	if next != prev {
		st.Status, st.Since = next, now
	}
	if next == store.Down && prev != store.Down {
		ev.opened = true
	}
	if prev == store.Down && next == store.Up {
		ev.closed = true
	}

	// Only up/down changes count as transitions, and only after the
	// first notification-worthy status.
	changed := (next == store.Down && st.Notified != store.Down) || (next == store.Up && st.Notified == store.Down)
	if changed {
		st.Transitions = append(st.Transitions, now)
	}
	st.Transitions = slices.DeleteFunc(st.Transitions, func(t time.Time) bool { return now.Sub(t) > flapWindow })

	switch {
	case !st.Flapping && len(st.Transitions) >= flapChanges:
		st.Flapping = true
		ev.notes = append(ev.notes, notify.Note{
			Type:  store.Warning,
			Title: c.Name + " is flapping",
			Body:  fmt.Sprintf("It changed between up and down %d times in %d min. Notifications for it are held until it settles.", len(st.Transitions), int(flapWindow.Minutes())),
		})
		st.Notified = next
	case st.Flapping:
		if len(st.Transitions) == 0 && (next == store.Up || next == store.Down) {
			st.Flapping = false
			st.Notified = next
			title := c.Name + " has settled: up"
			typ := store.Success
			if next == store.Down {
				title, typ = c.Name+" has settled: down", store.Failure
			}
			ev.notes = append(ev.notes, notify.Note{Type: typ, Title: title, Body: msg})
		} else if changed {
			st.Notified = next
		}
	case changed && next == store.Down:
		st.Notified = store.Down
		ev.notes = append(ev.notes, notify.Note{Type: store.Failure, Title: c.Name + " is down", Body: describe(c, msg)})
	case changed && next == store.Up:
		st.Notified = store.Up
		body := "After " + Human(now.Sub(downSince(prev, prevSince, now))) + "."
		ev.notes = append(ev.notes, notify.Note{Type: store.Success, Title: c.Name + " is back up", Body: body})
	case next == store.Up && st.Notified == "":
		st.Notified = store.Up
	}
	return ev
}

func downSince(prev string, since, now time.Time) time.Time {
	if prev == store.Down && !since.IsZero() {
		return since
	}
	return now
}

func describe(c store.Check, msg string) string {
	target := c.Target
	if c.Type == store.Push {
		target = "heartbeat"
	}
	if target == "" {
		return msg
	}
	return msg + "\n" + target
}

// certNotes warns as a certificate nears expiry: at CertDays, 7, 3 and 1
// days left, and when it has expired; and says when it was renewed.
func certNotes(c store.Check, st *store.State, expires, now time.Time) []notify.Note {
	st.CertExpires = expires
	if c.CertDays < 0 {
		st.CertNotified = 0
		return nil
	}
	days := int(expires.Sub(now).Hours() / 24)
	if expires.Before(now) {
		days = -1
	}
	if days > c.CertDays {
		if st.CertNotified != 0 {
			st.CertNotified = 0
			return []notify.Note{{
				Type:  store.Success,
				Title: "Certificate for " + c.Name + " renewed",
				Body:  "Valid until " + expires.Format("2 Jan 2006") + ".",
			}}
		}
		return nil
	}
	// Thresholds are stored +1 so 0 can mean "none yet".
	level := 0
	for _, t := range []int{-1, 1, 3, 7, c.CertDays} {
		if days <= t {
			level = t + 2
			break
		}
	}
	if st.CertNotified != 0 && level >= st.CertNotified {
		return nil
	}
	st.CertNotified = level
	n := notify.Note{Type: store.Warning, Title: fmt.Sprintf("Certificate for %s expires in %d days", c.Name, days)}
	switch {
	case days < 0:
		n.Type, n.Title = store.Failure, "Certificate for "+c.Name+" has expired"
	case days == 0:
		n.Type, n.Title = store.Failure, "Certificate for "+c.Name+" expires today"
	case days == 1:
		n.Type, n.Title = store.Failure, "Certificate for "+c.Name+" expires tomorrow"
	}
	n.Body = "Expires " + expires.Format("2 Jan 2006 15:04 MST") + ".\n" + c.Target
	return []notify.Note{n}
}

func (e *Engine) send(c store.Check, notes []notify.Note) {
	if c.Mute || e.Notifier == nil {
		return
	}
	source := "check"
	tags := []string{"check"}
	if c.Type == store.Push {
		source, tags = "heartbeat", []string{"heartbeat"}
	}
	tags = append(tags, c.Tags...)
	for _, n := range notes {
		n.Source, n.Tags = source, tags
		go func() {
			if _, err := e.Notifier.Notify(context.Background(), n); err != nil {
				slog.Warn("notify", "err", err)
			}
		}()
	}
}

// Upsert starts (or restarts) a saved check with its new settings.
func (e *Engine) Upsert(c store.Check) {
	c.Defaults()
	e.mu.Lock()
	defer e.mu.Unlock()
	en, ok := e.checks[c.ID]
	if !ok {
		en = &entry{state: store.State{Status: store.Unknown, Since: e.Now()}}
		e.checks[c.ID] = en
	} else if en.check.Type != c.Type || en.check.Target != c.Target {
		// A different thing to watch: forget the old state.
		en.state = store.State{Status: store.Unknown, Since: e.Now(), LastPing: en.state.LastPing}
	}
	if c.Paused != en.check.Paused && !c.Paused {
		en.state.Fails = 0
	}
	en.check = c
	e.startLocked(c.ID)
}

// Remove stops a check.
func (e *Engine) Remove(id int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if en, ok := e.checks[id]; ok {
		if en.cancel != nil {
			en.cancel()
		}
		delete(e.checks, id)
	}
}

// SetWindows replaces the maintenance windows.
func (e *Engine) SetWindows(ws []store.Window) {
	e.mu.Lock()
	e.windows = ws
	e.mu.Unlock()
}

func (e *Engine) inMaintenanceLocked(c store.Check, now time.Time) bool {
	for _, w := range e.windows {
		if w.Covers(c) && w.Active(now) {
			return true
		}
	}
	return false
}

// View is a check with what's known about it now.
type View struct {
	Check  store.Check `json:"check"`
	State  store.State `json:"state"`
	Status string      `json:"status"` // State.Status, or paused / maintenance
}

// Views lists every check with its state, by name.
func (e *Engine) Views() []View {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.Now()
	out := make([]View, 0, len(e.checks))
	for _, en := range e.checks {
		out = append(out, e.viewLocked(en, now))
	}
	slices.SortFunc(out, func(a, b View) int {
		if a.Check.Group != b.Check.Group {
			return strings.Compare(a.Check.Group, b.Check.Group)
		}
		return strings.Compare(strings.ToLower(a.Check.Name), strings.ToLower(b.Check.Name))
	})
	return out
}

func (e *Engine) View(id int64) (View, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	en, ok := e.checks[id]
	if !ok {
		return View{}, false
	}
	return e.viewLocked(en, e.Now()), true
}

func (e *Engine) viewLocked(en *entry, now time.Time) View {
	v := View{Check: en.check, State: en.state, Status: en.state.Status}
	v.State.Transitions = nil
	switch {
	case en.check.Paused:
		v.Status = store.Paused
	case e.inMaintenanceLocked(en.check, now):
		v.Status = store.Maintenance
	case en.check.Type == store.Push && !en.state.Started.IsZero() && en.state.Status != store.Down:
		v.Status = store.Running
	case en.state.Asleep:
		v.Status = store.Asleep
	}
	return v
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// Human formats a duration like "4 min" or "2.5 h".
func Human(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}
