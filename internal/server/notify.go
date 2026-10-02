package server

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/lookout/internal/checks"
	"github.com/audemed44/lookout/internal/notify"
	"github.com/audemed44/lookout/internal/store"
)

// notifyHandler takes Apprise API's stateful notify call, so apps that
// posted to apprise-api only change the host in their URL:
//
//	POST /notify/<key>  {"title": "…", "body": "…", "type": "failure", "tag": "…"}
//
// The key must be one added under Notifications → Senders. Without a key
// the Lookout token is required instead. Form-encoded bodies work too.
func (s *Server) notifyHandler(w http.ResponseWriter, r *http.Request) {
	source := "api"
	var tags []string
	if key := r.PathValue("key"); key != "" {
		snd, err := s.Store.UseSender(r.Context(), key)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "unknown notify key: add it in Lookout under Notifications → Senders")
			return
		}
		if err != nil {
			storeError(w, err)
			return
		}
		source, tags = snd.Name, snd.Tags
	} else if !s.authenticated(r) {
		writeError(w, http.StatusUnauthorized, "post to /notify/<key>, or send the Lookout token")
		return
	}

	var p struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Type  string `json:"type"`
		Tag   any    `json:"tag"` // "a, b" or ["a", "b"]
		Tags  any    `json:"tags"`
	}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	switch ct {
	case "application/json", "":
		raw, err := io.ReadAll(r.Body)
		if err != nil || (len(raw) > 0 && json.Unmarshal(raw, &p) != nil) {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	case "application/x-www-form-urlencoded", "multipart/form-data":
		if err := r.ParseMultipartForm(64 << 10); err != nil && !errors.Is(err, http.ErrNotMultipart) {
			writeError(w, http.StatusBadRequest, "invalid form")
			return
		}
		p.Title, p.Body, p.Type, p.Tag = r.FormValue("title"), r.FormValue("body"), r.FormValue("type"), r.FormValue("tag")
	default:
		writeError(w, http.StatusUnsupportedMediaType, "send JSON or a form")
		return
	}
	tags = append(slices.Clone(tags), tagList(p.Tag)...)
	tags = append(tags, tagList(p.Tags)...)
	rec, err := s.Notifier.Notify(r.Context(), notify.Note{
		Title: p.Title, Body: p.Body, Type: strings.ToLower(p.Type), Tags: tags, Source: source,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch rec.Status {
	case store.NoRoute:
		// Apprise's answer when nothing is configured under the key.
		w.WriteHeader(http.StatusNoContent)
	case store.Failed:
		writeJSON(w, http.StatusFailedDependency, map[string]any{"error": "delivery failed", "id": rec.ID, "status": rec.Status, "detail": json.RawMessage(rec.Detail)})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"id": rec.ID, "status": rec.Status})
	}
}

func tagList(v any) []string {
	switch t := v.(type) {
	case string:
		return notify.ParseTags(t)
	case []any:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, notify.ParseTags(s)...)
			}
		}
		return out
	}
	return nil
}

// targetView hides URLs typed into the UI (they may hold tokens); ones
// that come from the environment are shown as they are.
type targetView struct {
	store.Target
	Scheme  string   `json:"scheme"`
	Masked  bool     `json:"masked"`
	Missing []string `json:"missing,omitempty"`
}

func viewTarget(t store.Target) targetView {
	v := targetView{Target: t, Scheme: notify.Scheme(notify.Expand(t.URL)), Missing: notify.MissingEnv(t.URL)}
	if !notify.UsesEnv(t.URL) {
		v.URL, v.Masked = notify.Scheme(t.URL)+"://…", true
	}
	return v
}

func (s *Server) listTargets(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Targets(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	out := make([]targetView, len(list))
	for i, t := range list {
		out[i] = viewTarget(t)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) saveTarget(w http.ResponseWriter, r *http.Request) {
	var t store.Target
	if !readJSON(w, r, 8<<10, &t) {
		return
	}
	t.ID = 0
	t.Name, t.URL = strings.TrimSpace(t.Name), strings.TrimSpace(t.URL)
	if r.PathValue("id") != "" {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		old, err := s.Store.Target(r.Context(), id)
		if err != nil {
			storeError(w, err)
			return
		}
		t.ID = id
		if t.URL == "" || t.URL == viewTarget(old).URL {
			t.URL = old.URL // unchanged: the form shows the masked one
		}
	}
	if t.Name == "" || t.URL == "" {
		writeError(w, http.StatusBadRequest, "a name and a URL are required")
		return
	}
	if !notify.UsesEnv(t.URL) || len(notify.MissingEnv(t.URL)) == 0 {
		if _, err := notify.Parse(notify.Expand(t.URL)); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := s.Store.SaveTarget(r.Context(), &t); err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, viewTarget(t))
}

func (s *Server) deleteTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteTarget(r.Context(), id); err != nil {
		storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := s.Store.Target(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	if err := s.Notifier.Test(r.Context(), t); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Sent a test to " + t.Name})
}

func (s *Server) listRoutes(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Routes(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) saveRoute(w http.ResponseWriter, r *http.Request) {
	var rt store.Route
	if !readJSON(w, r, 8<<10, &rt) {
		return
	}
	rt.ID = 0
	if r.PathValue("id") != "" {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		rt.ID = id
	}
	rt.Name = strings.TrimSpace(rt.Name)
	if rt.Name == "" {
		writeError(w, http.StatusBadRequest, "a name is required")
		return
	}
	if rt.MinType != "" && !slices.Contains(notify.Types, rt.MinType) {
		writeError(w, http.StatusBadRequest, "min_type must be info, success, warning or failure")
		return
	}
	if len(rt.Targets) == 0 {
		writeError(w, http.StatusBadRequest, "pick at least one target")
		return
	}
	rt.Tags = notify.ParseTags(strings.Join(rt.Tags, ","))
	if err := s.Store.SaveRoute(r.Context(), &rt); err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rt)
}

func (s *Server) deleteRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteRoute(r.Context(), id); err != nil {
		storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listSenders(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Senders(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// validKey keeps keys usable in a URL path; Apprise keys are similar.
func validKey(k string) bool {
	if len(k) < 4 || len(k) > 128 {
		return false
	}
	for _, c := range k {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (s *Server) saveSender(w http.ResponseWriter, r *http.Request) {
	var snd store.Sender
	if !readJSON(w, r, 4<<10, &snd) {
		return
	}
	snd.Name = strings.TrimSpace(snd.Name)
	if snd.Name == "" {
		writeError(w, http.StatusBadRequest, "a name is required")
		return
	}
	if snd.Key == "" {
		snd.Key = checks.NewToken()
	}
	if !validKey(snd.Key) {
		writeError(w, http.StatusBadRequest, "a key is 4–128 letters, digits, - or _")
		return
	}
	snd.Tags = notify.ParseTags(strings.Join(snd.Tags, ","))
	snd.Created = time.Now()
	if err := s.Store.SaveSender(r.Context(), snd); err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snd)
}

func (s *Server) deleteSender(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteSender(r.Context(), r.PathValue("key")); err != nil {
		storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := s.Store.Notifications(r.Context(), before, limit)
	if err != nil {
		storeError(w, err)
		return
	}
	queued, _ := s.Store.QueuedCount(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "queued": queued})
}

func (s *Server) flushQueue(w http.ResponseWriter, r *http.Request) {
	s.Notifier.FlushAll(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// ping takes a heartbeat: /ping/<token>, /ping/<token>/start,
// /ping/<token>/fail or /ping/<token>/<exit code>. A POST body is kept as
// the message.
func (s *Server) ping(w http.ResponseWriter, r *http.Request) {
	var body string
	if r.Method == http.MethodPost {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 10<<10))
		body = string(raw)
		if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "application/x-www-form-urlencoded" {
			if v, err := url.ParseQuery(body); err == nil && v.Has("msg") {
				body = v.Get("msg")
			}
		}
	}
	if msg := r.URL.Query().Get("msg"); msg != "" && body == "" {
		body = msg
	}
	err := s.Engine.Ping(r.PathValue("token"), r.PathValue("kind"), body)
	if errors.Is(err, checks.ErrUnknownToken) {
		http.Error(w, "unknown ping token", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "OK\n")
}
