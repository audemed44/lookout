// Package config exports Lookout's setup to YAML and imports it back, so
// it can be kept in git (e.g. next to the stack in the Hoist repo).
//
// Target URLs are only exported when they come from the environment
// (${TELEGRAM_URL}); a URL typed into the UI may hold a token, so it stays
// in the database and an imported target without a URL keeps the one it has.
package config

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/audemed44/lookout/internal/checks"
	"github.com/audemed44/lookout/internal/notify"
	"github.com/audemed44/lookout/internal/store"
)

type File struct {
	Checks      []store.Check   `yaml:"checks"`
	Maintenance []store.Window  `yaml:"maintenance,omitempty"`
	Targets     []store.Target  `yaml:"targets,omitempty"`
	Routes      []store.Route   `yaml:"routes,omitempty"`
	Settings    *store.Settings `yaml:"settings,omitempty"`
}

const header = `# Lookout configuration, exported from the UI.
# Import it under Settings → Configuration. Notification target URLs are
# only included when they come from the environment (${VAR}).
`

func Export(ctx context.Context, s *store.Store) ([]byte, error) {
	var f File
	var err error
	if f.Checks, err = s.Checks(ctx); err != nil {
		return nil, err
	}
	if f.Maintenance, err = s.Windows(ctx); err != nil {
		return nil, err
	}
	if f.Targets, err = s.Targets(ctx); err != nil {
		return nil, err
	}
	for i := range f.Targets {
		if !notify.UsesEnv(f.Targets[i].URL) {
			f.Targets[i].URL = ""
		}
	}
	if f.Routes, err = s.Routes(ctx); err != nil {
		return nil, err
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	f.Settings = &settings
	var buf bytes.Buffer
	buf.WriteString(header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Report says what an import changed.
type Report struct {
	Added    []string `json:"added"`
	Updated  []string `json:"updated"`
	Removed  []string `json:"removed"`
	Warnings []string `json:"warnings"`
}

// Changes are the checks the scheduler needs to pick up after an import.
type Changes struct {
	Saved   []store.Check
	Removed []int64
}

// Import merges a file into the setup, matching everything by name. With
// replace, checks, windows, targets and routes the file doesn't list are
// removed.
func Import(ctx context.Context, s *store.Store, data []byte, replace bool) (Report, Changes, error) {
	rep := Report{Added: []string{}, Updated: []string{}, Removed: []string{}, Warnings: []string{}}
	var ch Changes
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return rep, ch, fmt.Errorf("invalid YAML: %v", err)
	}
	for i, c := range f.Checks {
		if err := Validate(&c); err != nil {
			return rep, ch, fmt.Errorf("check %d (%s): %v", i+1, c.Name, err)
		}
		f.Checks[i] = c
	}

	existing, err := s.Checks(ctx)
	if err != nil {
		return rep, ch, err
	}
	byName := map[string]store.Check{}
	for _, c := range existing {
		byName[c.Name] = c
	}
	keep := map[string]bool{}
	for _, c := range f.Checks {
		keep[c.Name] = true
		if old, ok := byName[c.Name]; ok {
			c.ID = old.ID
			rep.Updated = append(rep.Updated, "check "+c.Name)
		} else {
			rep.Added = append(rep.Added, "check "+c.Name)
		}
		if err := s.SaveCheck(ctx, &c); err != nil {
			return rep, ch, err
		}
		ch.Saved = append(ch.Saved, c)
	}
	if replace {
		for _, c := range existing {
			if !keep[c.Name] {
				if err := s.DeleteCheck(ctx, c.ID); err != nil {
					return rep, ch, err
				}
				ch.Removed = append(ch.Removed, c.ID)
				rep.Removed = append(rep.Removed, "check "+c.Name)
			}
		}
	}

	windows, err := s.Windows(ctx)
	if err != nil {
		return rep, ch, err
	}
	wByName := map[string]store.Window{}
	for _, w := range windows {
		wByName[w.Name] = w
	}
	keep = map[string]bool{}
	for _, w := range f.Maintenance {
		keep[w.Name] = true
		w.ID = wByName[w.Name].ID
		if err := s.SaveWindow(ctx, &w); err != nil {
			return rep, ch, err
		}
	}
	if replace {
		for _, w := range windows {
			if !keep[w.Name] {
				_ = s.DeleteWindow(ctx, w.ID)
				rep.Removed = append(rep.Removed, "maintenance "+w.Name)
			}
		}
	}

	targets, err := s.Targets(ctx)
	if err != nil {
		return rep, ch, err
	}
	tByName := map[string]store.Target{}
	for _, t := range targets {
		tByName[t.Name] = t
	}
	keep = map[string]bool{}
	for _, t := range f.Targets {
		keep[t.Name] = true
		old, ok := tByName[t.Name]
		if t.URL == "" {
			if !ok {
				rep.Warnings = append(rep.Warnings, "target "+t.Name+" has no URL: add it in the UI")
				continue
			}
			t.URL = old.URL
		}
		if _, err := notify.Parse(notify.Expand(t.URL)); err != nil && !notify.UsesEnv(t.URL) {
			rep.Warnings = append(rep.Warnings, "target "+t.Name+": "+err.Error())
			continue
		}
		t.ID = old.ID
		if err := s.SaveTarget(ctx, &t); err != nil {
			return rep, ch, err
		}
	}
	if replace {
		for _, t := range targets {
			if !keep[t.Name] {
				_ = s.DeleteTarget(ctx, t.ID)
				rep.Removed = append(rep.Removed, "target "+t.Name)
			}
		}
	}

	routes, err := s.Routes(ctx)
	if err != nil {
		return rep, ch, err
	}
	rByName := map[string]store.Route{}
	for _, r := range routes {
		rByName[r.Name] = r
	}
	keep = map[string]bool{}
	for _, r := range f.Routes {
		keep[r.Name] = true
		r.ID = rByName[r.Name].ID
		if err := s.SaveRoute(ctx, &r); err != nil {
			return rep, ch, err
		}
	}
	if replace {
		for _, r := range routes {
			if !keep[r.Name] {
				_ = s.DeleteRoute(ctx, r.ID)
				rep.Removed = append(rep.Removed, "route "+r.Name)
			}
		}
	}

	if f.Settings != nil {
		if err := s.SaveSettings(ctx, *f.Settings); err != nil {
			return rep, ch, err
		}
	}
	return rep, ch, nil
}

// Validate checks a check's settings and fills in defaults.
func Validate(c *store.Check) error {
	c.Name = strings.TrimSpace(c.Name)
	c.Target = strings.TrimSpace(c.Target)
	if c.Name == "" {
		return fmt.Errorf("a name is required")
	}
	if !slices.Contains(store.CheckTypes, c.Type) {
		return fmt.Errorf("type must be one of %s", strings.Join(store.CheckTypes, ", "))
	}
	if c.Type != store.Push && c.Target == "" {
		return fmt.Errorf("a target is required")
	}
	switch c.Type {
	case store.HTTP:
		if !strings.HasPrefix(c.Target, "http://") && !strings.HasPrefix(c.Target, "https://") {
			return fmt.Errorf("the URL must start with http:// or https://")
		}
	case store.TCP:
		if !strings.Contains(c.Target, ":") {
			return fmt.Errorf("the target must be host:port")
		}
	case store.Push:
		if c.Token == "" {
			c.Token = checks.NewToken()
		}
	}
	if c.Interval != 0 && c.Interval < 10 && c.Type != store.Push {
		return fmt.Errorf("the interval must be at least 10 seconds")
	}
	if c.Retries < 0 || c.Retries > 20 {
		return fmt.Errorf("retries must be between 0 and 20")
	}
	c.Tags = cleanTags(c.Tags)
	c.Defaults()
	return nil
}

func cleanTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}
