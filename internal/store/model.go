package store

import "time"

// Check types.
const (
	HTTP   = "http"
	TCP    = "tcp"
	Ping   = "ping"
	DNS    = "dns"
	Docker = "docker"
	TLS    = "tls"
	Push   = "push"
)

var CheckTypes = []string{HTTP, TCP, Ping, DNS, Docker, TLS, Push}

// Check is one thing Lookout watches. Durations are in seconds so the YAML
// export stays plain numbers.
type Check struct {
	ID          int64    `json:"id" yaml:"-"`
	Name        string   `json:"name" yaml:"name"`
	Type        string   `json:"type" yaml:"type"`
	Target      string   `json:"target,omitempty" yaml:"target,omitempty"`
	Group       string   `json:"group,omitempty" yaml:"group,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Tags        []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Paused      bool     `json:"paused,omitempty" yaml:"paused,omitempty"`
	// Mute keeps the check running without sending notifications.
	Mute bool `json:"mute,omitempty" yaml:"mute,omitempty"`

	Interval int `json:"interval,omitempty" yaml:"interval,omitempty"` // seconds between runs
	Timeout  int `json:"timeout,omitempty" yaml:"timeout,omitempty"`   // seconds
	// Retries is how many failures in a row are tolerated before "down".
	Retries int `json:"retries,omitempty" yaml:"retries,omitempty"`

	// HTTP
	Method string `json:"method,omitempty" yaml:"method,omitempty"`
	// Status is the accepted status codes: "200-299", "200-399,401".
	Status        string `json:"status,omitempty" yaml:"status,omitempty"`
	Keyword       string `json:"keyword,omitempty" yaml:"keyword,omitempty"`
	InvertKeyword bool   `json:"invert_keyword,omitempty" yaml:"invert_keyword,omitempty"`
	JSONPath      string `json:"json_path,omitempty" yaml:"json_path,omitempty"`
	// Expect is the value at JSONPath, or a DNS answer to look for.
	Expect    string `json:"expect,omitempty" yaml:"expect,omitempty"`
	IgnoreTLS bool   `json:"ignore_tls,omitempty" yaml:"ignore_tls,omitempty"`

	// CertDays warns this many days before an HTTPS or TLS certificate
	// expires (default 14; -1 turns it off).
	CertDays int `json:"cert_days,omitempty" yaml:"cert_days,omitempty"`

	// DNS
	Record   string `json:"record,omitempty" yaml:"record,omitempty"`     // A, AAAA, CNAME, MX, TXT, NS
	Resolver string `json:"resolver,omitempty" yaml:"resolver,omitempty"` // host[:port]; empty uses the system's

	// Push heartbeats: the job pings /ping/<token> at least every Period
	// seconds; Grace is extra slack before it counts as late.
	Token  string `json:"token,omitempty" yaml:"token,omitempty"`
	Period int    `json:"period,omitempty" yaml:"period,omitempty"`
	Grace  int    `json:"grace,omitempty" yaml:"grace,omitempty"`

	// Source is where the check came from: "" (made here), "npm" or "kuma";
	// SourceKey identifies it there (the domain, the Kuma monitor ID).
	Source    string `json:"source,omitempty" yaml:"source,omitempty"`
	SourceKey string `json:"source_key,omitempty" yaml:"source_key,omitempty"`
}

// Defaults fills in unset fields.
func (c *Check) Defaults() {
	if c.Type == Push {
		c.Interval = 0 // heartbeats are pinged, not polled
	} else if c.Interval <= 0 {
		c.Interval = 60
	}
	if c.Timeout <= 0 {
		c.Timeout = 10
	}
	if c.Timeout > c.Interval && c.Type != Push {
		c.Timeout = c.Interval
	}
	if c.Type == HTTP {
		if c.Method == "" {
			c.Method = "GET"
		}
		if c.Status == "" {
			c.Status = "200-299"
		}
	}
	if c.Type == DNS && c.Record == "" {
		c.Record = "A"
	}
	if c.Type == Push {
		if c.Period <= 0 {
			c.Period = 3600
		}
		if c.Grace <= 0 {
			c.Grace = max(60, c.Period/10)
		}
	}
	if c.CertDays == 0 {
		c.CertDays = 14
	}
}

// Statuses.
const (
	Up          = "up"
	Down        = "down"
	Pending     = "pending" // failing, but not yet more than Retries times
	Paused      = "paused"
	Maintenance = "maintenance"
	Unknown     = "unknown" // not checked yet
	Running     = "running" // a heartbeat job that pinged /start
)

// State is what Lookout knows about a check right now. It's kept in memory
// and saved on every change, so a restart picks up where it left off.
type State struct {
	Status string    `json:"status"`
	Since  time.Time `json:"since"`
	// Fails counts failures in a row.
	Fails   int       `json:"fails"`
	Last    time.Time `json:"last,omitzero"`
	Latency float64   `json:"latency"` // ms
	Message string    `json:"message,omitempty"`

	CertExpires time.Time `json:"cert_expires,omitzero"`
	// CertNotified is the smallest "days left" threshold already notified.
	CertNotified int `json:"cert_notified,omitempty"`

	// Heartbeats.
	LastPing time.Time `json:"last_ping,omitzero"`
	Started  time.Time `json:"started,omitzero"`

	// Flapping is set while the check keeps changing between up and down;
	// notifications are held until it settles.
	Flapping    bool        `json:"flapping,omitempty"`
	Transitions []time.Time `json:"transitions,omitempty"`
	// Notified is the last status a notification was sent for.
	Notified string `json:"notified,omitempty"`
}

type Result struct {
	Time    time.Time `json:"time"`
	OK      bool      `json:"ok"`
	Latency float64   `json:"latency"` // ms
	Message string    `json:"message,omitempty"`
}

type Incident struct {
	ID      int64     `json:"id"`
	CheckID int64     `json:"check_id"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitzero"`
	Cause   string    `json:"cause"`
}

// Window is a maintenance window: checks it covers aren't run and don't
// notify while it's on. Either a one-off (Start–End) or repeating daily or
// weekly from From for Minutes.
type Window struct {
	ID       int64     `json:"id" yaml:"-"`
	Name     string    `json:"name" yaml:"name"`
	Enabled  bool      `json:"enabled" yaml:"enabled"`
	Checks   []string  `json:"checks,omitempty" yaml:"checks,omitempty"` // check names; empty with no tags means all
	Tags     []string  `json:"tags,omitempty" yaml:"tags,omitempty"`
	Start    time.Time `json:"start,omitzero" yaml:"start,omitempty"`
	End      time.Time `json:"end,omitzero" yaml:"end,omitempty"`
	Repeat   string    `json:"repeat,omitempty" yaml:"repeat,omitempty"`     // "", daily, weekly
	Weekdays []int     `json:"weekdays,omitempty" yaml:"weekdays,omitempty"` // 0 = Sunday
	From     string    `json:"from,omitempty" yaml:"from,omitempty"`         // HH:MM local time
	Minutes  int       `json:"minutes,omitempty" yaml:"minutes,omitempty"`
}

// Active reports whether the window is on at t.
func (w Window) Active(t time.Time) bool {
	if !w.Enabled {
		return false
	}
	if w.Repeat == "" {
		return !w.Start.IsZero() && !t.Before(w.Start) && (w.End.IsZero() || t.Before(w.End))
	}
	from, err := time.ParseInLocation("15:04", w.From, t.Location())
	if err != nil || w.Minutes <= 0 {
		return false
	}
	// A window may run past midnight, so look at today's and yesterday's.
	for _, back := range []int{0, -1} {
		day := t.AddDate(0, 0, back)
		start := time.Date(day.Year(), day.Month(), day.Day(), from.Hour(), from.Minute(), 0, 0, t.Location())
		if w.Repeat == "weekly" && !containsInt(w.Weekdays, int(start.Weekday())) {
			continue
		}
		if !t.Before(start) && t.Before(start.Add(time.Duration(w.Minutes)*time.Minute)) {
			return true
		}
	}
	return false
}

// Covers reports whether the window applies to the check.
func (w Window) Covers(c Check) bool {
	if len(w.Checks) == 0 && len(w.Tags) == 0 {
		return true
	}
	for _, n := range w.Checks {
		if n == c.Name {
			return true
		}
	}
	for _, t := range w.Tags {
		for _, ct := range c.Tags {
			if t == ct {
				return true
			}
		}
	}
	return false
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Notification types, as Apprise names them.
const (
	Info    = "info"
	Success = "success"
	Warning = "warning"
	Failure = "failure"
)

// TypeRank orders notification types by severity.
func TypeRank(t string) int {
	switch t {
	case Success:
		return 1
	case Warning:
		return 2
	case Failure:
		return 3
	}
	return 0
}

type Target struct {
	ID      int64  `json:"id" yaml:"-"`
	Name    string `json:"name" yaml:"name"`
	URL     string `json:"url" yaml:"url,omitempty"`
	Enabled bool   `json:"enabled" yaml:"enabled"`
}

// Route sends matching notifications to some targets. A notification
// matches when it has one of Tags (or Tags is empty) and is at least MinType.
type Route struct {
	ID      int64    `json:"id" yaml:"-"`
	Name    string   `json:"name" yaml:"name"`
	Tags    []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	MinType string   `json:"min_type,omitempty" yaml:"min_type,omitempty"`
	Targets []string `json:"targets" yaml:"targets"` // target names
	// Digest collects matches and sends them together at the digest time.
	Digest bool `json:"digest,omitempty" yaml:"digest,omitempty"`
	// Stop skips the routes after this one when it matches.
	Stop bool `json:"stop,omitempty" yaml:"stop,omitempty"`
}

// Sender is a key other apps post to (/notify/<key>), like an Apprise key.
type Sender struct {
	Key      string    `json:"key"`
	Name     string    `json:"name"`
	Tags     []string  `json:"tags"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used,omitzero"`
}

// Notification statuses.
const (
	Sent       = "sent"
	Failed     = "failed"
	Partial    = "partial"
	Queued     = "queued"
	Suppressed = "suppressed"
	NoRoute    = "no_route"
)

type Notification struct {
	ID     int64     `json:"id"`
	Time   time.Time `json:"time"`
	Source string    `json:"source"`
	Type   string    `json:"type"`
	Title  string    `json:"title"`
	Body   string    `json:"body"`
	Tags   []string  `json:"tags"`
	Status string    `json:"status"`
	Detail string    `json:"detail,omitempty"`
}

type Speedtest struct {
	ID       int64     `json:"id"`
	Time     time.Time `json:"time"`
	Source   string    `json:"source"` // scheduled, manual, import
	Server   string    `json:"server,omitempty"`
	ServerID string    `json:"server_id,omitempty"`
	ISP      string    `json:"isp,omitempty"`
	Ping     float64   `json:"ping"`     // ms
	Jitter   float64   `json:"jitter"`   // ms
	Download float64   `json:"download"` // Mbit/s
	Upload   float64   `json:"upload"`   // Mbit/s
	Error    string    `json:"error,omitempty"`
}

// Settings are everything configured in the UI apart from checks, windows
// and notification targets.
type Settings struct {
	Notify     NotifySettings    `json:"notify" yaml:"notify"`
	Speedtest  SpeedtestSettings `json:"speedtest" yaml:"speedtest"`
	Discovery  DiscoverySettings `json:"discovery" yaml:"discovery"`
	StatusPage StatusPage        `json:"status_page" yaml:"status_page"`
	Retention  Retention         `json:"retention" yaml:"retention"`
}

type NotifySettings struct {
	// DedupeMinutes drops a notification identical to one sent this
	// recently (0 turns it off).
	DedupeMinutes int `json:"dedupe_minutes" yaml:"dedupe_minutes"`
	// Quiet hours hold everything but failures until they end.
	Quiet    bool   `json:"quiet" yaml:"quiet"`
	QuietOn  string `json:"quiet_from" yaml:"quiet_from"` // HH:MM
	QuietOff string `json:"quiet_to" yaml:"quiet_to"`
	// DigestAt is when digest routes are sent, HH:MM.
	DigestAt string `json:"digest_at" yaml:"digest_at"`
}

type SpeedtestSettings struct {
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	Schedule string `json:"schedule" yaml:"schedule"` // cron
	ServerID string `json:"server_id,omitempty" yaml:"server_id,omitempty"`
	// Alert below these (0 turns each off).
	MinDownload float64 `json:"min_download,omitempty" yaml:"min_download,omitempty"`
	MinUpload   float64 `json:"min_upload,omitempty" yaml:"min_upload,omitempty"`
	MaxPing     float64 `json:"max_ping,omitempty" yaml:"max_ping,omitempty"`
}

type DiscoverySettings struct {
	// Auto syncs every Every minutes when the proxy is configured.
	Auto     bool     `json:"auto" yaml:"auto"`
	Every    int      `json:"every" yaml:"every"`
	Tags     []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Interval int      `json:"interval" yaml:"interval"` // for the checks it creates
	// Ignored domains don't get a check, even when the proxy has them.
	Ignored []string `json:"ignored,omitempty" yaml:"ignored,omitempty"`
}

type StatusPage struct {
	Enabled bool     `json:"enabled" yaml:"enabled"`
	Title   string   `json:"title,omitempty" yaml:"title,omitempty"`
	Tags    []string `json:"tags,omitempty" yaml:"tags,omitempty"` // empty shows every check
}

type Retention struct {
	RawHours         int `json:"raw_hours" yaml:"raw_hours"`
	RollupDays       int `json:"rollup_days" yaml:"rollup_days"`
	NotificationDays int `json:"notification_days" yaml:"notification_days"`
}

func DefaultSettings() Settings {
	return Settings{
		Notify:    NotifySettings{DedupeMinutes: 10, QuietOn: "23:00", QuietOff: "07:00", DigestAt: "09:00"},
		Speedtest: SpeedtestSettings{Enabled: true, Schedule: "8 */8 * * *"},
		Discovery: DiscoverySettings{Every: 60, Interval: 60, Tags: []string{"proxy"}},
		Retention: Retention{RawHours: 48, RollupDays: 400, NotificationDays: 90},
	}
}

// Normalize fills in zero values with defaults.
func (s *Settings) Normalize() {
	d := DefaultSettings()
	if s.Notify.DedupeMinutes < 0 {
		s.Notify.DedupeMinutes = 0
	}
	if s.Notify.QuietOn == "" {
		s.Notify.QuietOn = d.Notify.QuietOn
	}
	if s.Notify.QuietOff == "" {
		s.Notify.QuietOff = d.Notify.QuietOff
	}
	if s.Notify.DigestAt == "" {
		s.Notify.DigestAt = d.Notify.DigestAt
	}
	if s.Speedtest.Schedule == "" {
		s.Speedtest.Schedule = d.Speedtest.Schedule
	}
	if s.Discovery.Every <= 0 {
		s.Discovery.Every = d.Discovery.Every
	}
	if s.Discovery.Interval <= 0 {
		s.Discovery.Interval = d.Discovery.Interval
	}
	if s.Retention.RawHours <= 0 {
		s.Retention.RawHours = d.Retention.RawHours
	}
	if s.Retention.RollupDays <= 0 {
		s.Retention.RollupDays = d.Retention.RollupDays
	}
	if s.Retention.NotificationDays <= 0 {
		s.Retention.NotificationDays = d.Retention.NotificationDays
	}
}
