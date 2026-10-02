package checks

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/lookout/internal/store"
)

// Heartbeats (push checks) work the other way round: a job pings
// /ping/<token> when it runs, and the check goes down when a ping is late.

// NewToken makes an unguessable token for a ping URL or a notify key.
func NewToken() string {
	b := make([]byte, 15)
	_, _ = rand.Read(b)
	return strings.ToLower(base32.StdEncoding.EncodeToString(b))
}

// Ping kinds.
const (
	PingOK    = ""
	PingStart = "start"
	PingFail  = "fail"
)

var ErrUnknownToken = fmt.Errorf("unknown ping token")

// Ping records a ping from a job. kind is "", start, fail, or an exit
// code (0 is success); body is an optional message, such as the job's
// last lines of output.
func (e *Engine) Ping(token, kind, body string) error {
	e.mu.Lock()
	var en *entry
	for _, x := range e.checks {
		if x.check.Type == store.Push && x.check.Token == token {
			en = x
			break
		}
	}
	if en == nil {
		e.mu.Unlock()
		return ErrUnknownToken
	}
	id := en.check.ID
	now := e.Now()
	if kind == PingStart {
		en.state.Started = now
		st := en.state
		e.mu.Unlock()
		return e.Store.SaveState(context.Background(), id, st)
	}
	ok := kind == PingOK
	if code, err := strconv.Atoi(kind); err == nil {
		ok = code == 0
		if !ok && body == "" {
			body = fmt.Sprintf("exit code %d", code)
		}
	} else if kind != PingOK && kind != PingFail {
		e.mu.Unlock()
		return fmt.Errorf("unknown ping kind %q", kind)
	}
	var took time.Duration
	if !en.state.Started.IsZero() {
		took = now.Sub(en.state.Started)
	}
	en.state.Started = time.Time{}
	en.state.LastPing = now
	e.mu.Unlock()

	body = strings.TrimSpace(body)
	if len(body) > 1000 {
		body = "…" + body[len(body)-999:]
	}
	if body == "" {
		body = "ping"
		if !ok {
			body = "the job reported a failure"
		}
	}
	e.Record(id, Outcome{OK: ok, Latency: took, Message: body})
	return nil
}

// watchHeartbeats marks heartbeats down when a ping is late, or when a job
// that pinged /start hasn't finished within the grace time.
func (e *Engine) watchHeartbeats(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		e.checkLate()
	}
}

func (e *Engine) checkLate() {
	now := e.Now()
	type late struct {
		id  int64
		msg string
	}
	var found []late
	e.mu.Lock()
	for id, en := range e.checks {
		c, st := en.check, &en.state
		if c.Type != store.Push || c.Paused || st.Status == store.Down || e.inMaintenanceLocked(c, now) {
			continue
		}
		grace := time.Duration(c.Grace) * time.Second
		if !st.Started.IsZero() && now.Sub(st.Started) > grace {
			found = append(found, late{id, "started at " + st.Started.Format("15:04") + " but hasn't finished"})
			st.Started = time.Time{}
			st.Fails = max(st.Fails, c.Retries)
			continue
		}
		ref := st.LastPing
		if ref.IsZero() {
			ref = st.Since // never pinged: count from when the check was made
		}
		if ref.IsZero() || !st.Started.IsZero() {
			continue
		}
		if now.Sub(ref) > time.Duration(c.Period)*time.Second+grace {
			msg := "no ping since " + ref.Format("2 Jan 15:04")
			if st.LastPing.IsZero() {
				msg = "no ping yet"
			}
			found = append(found, late{id, msg})
			// A late heartbeat is down straight away; retries are for
			// failure pings.
			st.Fails = max(st.Fails, c.Retries)
		}
	}
	e.mu.Unlock()
	for _, l := range found {
		e.Record(l.id, Outcome{Message: l.msg})
	}
}
