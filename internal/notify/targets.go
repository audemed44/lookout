// Package notify delivers notifications: from Lookout's own checks and
// speedtests, and from other apps posting Apprise-style to /notify.
//
// Targets are written as Apprise URLs (tgram://, ntfy://, discord://,
// json://), so they can be copied from an Apprise config, and may refer to
// environment variables (${TELEGRAM_URL}) so secrets stay in the stack's
// .env.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// Message is what gets delivered.
type Message struct {
	Title string
	Body  string
	Type  string // info, success, warning, failure
}

// Sender delivers to one kind of service. Adding a service is a new
// Sender and a case in Parse.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

var client = &http.Client{Timeout: 20 * time.Second}

// Endpoints the senders call; tests point them at fakes.
var (
	TelegramAPI = "https://api.telegram.org"
	DiscordAPI  = "https://discord.com/api/webhooks"
)

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Expand replaces ${VAR} with the environment variable's value.
func Expand(s string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(m[2 : len(m)-1])
	})
}

// UsesEnv reports whether the URL comes from the environment, so it's
// safe to show and export.
func UsesEnv(raw string) bool { return envRef.MatchString(raw) }

// MissingEnv lists variables the URL refers to that aren't set.
func MissingEnv(raw string) []string {
	var out []string
	for _, m := range envRef.FindAllStringSubmatch(raw, -1) {
		if os.Getenv(m[1]) == "" {
			out = append(out, m[1])
		}
	}
	return out
}

// Schemes are the target URL schemes Lookout understands.
var Schemes = []string{"tgram", "ntfy", "ntfys", "discord", "json", "jsons"}

// Scheme returns the URL's scheme, or "" if it has none.
func Scheme(raw string) string {
	s, _, ok := strings.Cut(strings.TrimSpace(raw), "://")
	if !ok {
		return ""
	}
	return strings.ToLower(s)
}

// Parse turns a target URL (after Expand) into a Sender.
func Parse(raw string) (Sender, error) {
	raw = strings.TrimSpace(raw)
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return nil, fmt.Errorf("not a URL: expected e.g. tgram://<bot token>/<chat id>")
	}
	rest, query, _ := strings.Cut(rest, "?")
	params, _ := url.ParseQuery(query)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	switch strings.ToLower(scheme) {
	case "tgram":
		// tgram://<bot token>/<chat id>[/<chat id>…]; a chat may be
		// <chat id>:<topic id> for a forum topic.
		if len(parts) < 2 || parts[0] == "" {
			return nil, fmt.Errorf("telegram: expected tgram://<bot token>/<chat id>")
		}
		return &telegram{token: parts[0], chats: parts[1:]}, nil
	case "ntfy", "ntfys":
		// ntfy://<topic> (ntfy.sh), ntfy[s]://[user:pass@]<host>/<topic>
		u, err := url.Parse(scheme + "://" + rest)
		if err != nil {
			return nil, fmt.Errorf("ntfy: %v", err)
		}
		n := &ntfy{base: "https://ntfy.sh", topic: strings.Trim(u.Path, "/")}
		if n.topic == "" {
			n.topic = u.Host
		} else {
			proto := "http"
			if scheme == "ntfys" {
				proto = "https"
			}
			n.base = proto + "://" + u.Host
		}
		if u.User != nil {
			n.user = u.User.Username()
			n.pass, _ = u.User.Password()
		}
		n.token = params.Get("token")
		if n.topic == "" {
			return nil, fmt.Errorf("ntfy: expected ntfy://<host>/<topic>")
		}
		return n, nil
	case "discord":
		if len(parts) < 2 {
			return nil, fmt.Errorf("discord: expected discord://<webhook id>/<webhook token>")
		}
		return &discord{id: parts[0], token: parts[1]}, nil
	case "json", "jsons":
		proto := "http"
		if scheme == "jsons" {
			proto = "https"
		}
		u := proto + "://" + rest
		if query != "" {
			u += "?" + query
		}
		if _, err := url.ParseRequestURI(u); err != nil {
			return nil, fmt.Errorf("json: %v", err)
		}
		return &webhook{url: u}, nil
	}
	return nil, fmt.Errorf("unsupported service %q (supported: %s)", scheme, strings.Join(Schemes, ", "))
}

func post(ctx context.Context, u string, body []byte, contentType string, header map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		// Don't repeat the URL: it may hold a token.
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return fmt.Errorf("could not reach %s: %v", req.URL.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("%s answered HTTP %d: %s", req.URL.Host, resp.StatusCode, bytes.TrimSpace(msg))
	}
	return nil
}

func emoji(t string) string {
	switch t {
	case "success":
		return "✅"
	case "warning":
		return "⚠️"
	case "failure":
		return "🔴"
	}
	return "ℹ️"
}

type telegram struct {
	token string
	chats []string
}

func (t *telegram) Send(ctx context.Context, m Message) error {
	text := html.EscapeString(m.Body)
	if m.Title != "" {
		text = emoji(m.Type) + " <b>" + html.EscapeString(m.Title) + "</b>"
		if m.Body != "" && m.Body != m.Title {
			text += "\n" + html.EscapeString(m.Body)
		}
	}
	var errs []string
	for _, chat := range t.chats {
		payload := map[string]any{"text": text, "parse_mode": "HTML", "disable_web_page_preview": true}
		id, topic, ok := strings.Cut(chat, ":")
		payload["chat_id"] = id
		if ok {
			payload["message_thread_id"] = topic
		}
		raw, _ := json.Marshal(payload)
		if err := post(ctx, TelegramAPI+"/bot"+t.token+"/sendMessage", raw, "application/json", nil); err != nil {
			errs = append(errs, strings.ReplaceAll(err.Error(), t.token, "…"))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("telegram: %s", strings.Join(errs, "; "))
	}
	return nil
}

type ntfy struct {
	base, topic, user, pass, token string
}

func (n *ntfy) Send(ctx context.Context, m Message) error {
	prio := map[string]int{"failure": 4, "warning": 4, "success": 3, "info": 3}[m.Type]
	payload := map[string]any{"topic": n.topic, "title": m.Title, "message": m.Body, "priority": max(prio, 3)}
	if tag := map[string]string{"failure": "rotating_light", "warning": "warning", "success": "white_check_mark"}[m.Type]; tag != "" {
		payload["tags"] = []string{tag}
	}
	raw, _ := json.Marshal(payload)
	header := map[string]string{}
	if n.token != "" {
		header["Authorization"] = "Bearer " + n.token
	} else if n.user != "" {
		req, _ := http.NewRequest("GET", "/", nil)
		req.SetBasicAuth(n.user, n.pass)
		header["Authorization"] = req.Header.Get("Authorization")
	}
	if err := post(ctx, n.base, raw, "application/json", header); err != nil {
		return fmt.Errorf("ntfy: %w", err)
	}
	return nil
}

type discord struct {
	id, token string
}

func (d *discord) Send(ctx context.Context, m Message) error {
	color := map[string]int{"failure": 0xff3b30, "warning": 0xf59e0b, "success": 0x22c55e}[m.Type]
	if color == 0 {
		color = 0x2563ff
	}
	payload := map[string]any{"embeds": []map[string]any{{"title": m.Title, "description": m.Body, "color": color}}}
	raw, _ := json.Marshal(payload)
	if err := post(ctx, DiscordAPI+"/"+d.id+"/"+d.token, raw, "application/json", nil); err != nil {
		return fmt.Errorf("discord: %s", strings.ReplaceAll(err.Error(), d.token, "…"))
	}
	return nil
}

// webhook posts Apprise's JSON notification format.
type webhook struct {
	url string
}

func (w *webhook) Send(ctx context.Context, m Message) error {
	raw, _ := json.Marshal(map[string]string{"version": "1.0", "title": m.Title, "message": m.Body, "type": m.Type})
	if err := post(ctx, w.url, raw, "application/json", nil); err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	return nil
}
