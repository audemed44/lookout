package config

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/audemed44/lookout/internal/store"
)

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, c := range []store.Check{
		{Name: "web", Type: store.HTTP, Target: "https://example.com", Tags: []string{"Web"}},
		{Name: "backup", Type: store.Push},
	} {
		if err := Validate(&c); err != nil {
			t.Fatal(err)
		}
		_ = db.SaveCheck(ctx, &c)
	}
	_ = db.SaveTarget(ctx, &store.Target{Name: "Phone", URL: "${TELEGRAM_URL}", Enabled: true})
	_ = db.SaveTarget(ctx, &store.Target{Name: "Secret", URL: "tgram://123:SECRET/1", Enabled: true})
	_ = db.SaveRoute(ctx, &store.Route{Name: "all", Targets: []string{"Phone"}})

	out, err := Export(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	y := string(out)
	if strings.Contains(y, "SECRET") || !strings.Contains(y, "${TELEGRAM_URL}") || !strings.Contains(y, "- web") {
		t.Fatalf("export:\n%s", y)
	}

	// Into an empty database: the typed URL can't come along.
	db2, _ := store.Open(filepath.Join(t.TempDir(), "t2.db"))
	defer db2.Close()
	rep, ch, err := Import(ctx, db2, out, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Saved) != 2 || len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "Secret") {
		t.Fatalf("import report = %+v", rep)
	}
	targets, _ := db2.Targets(ctx)
	if len(targets) != 1 || targets[0].URL != "${TELEGRAM_URL}" {
		t.Errorf("targets = %+v", targets)
	}

	// Back into the original with replace: same names update, the typed
	// URL is kept, and a check missing from the file is removed.
	edited := strings.Replace(y, "- name: backup", "- name: nightly", 1)
	rep, ch, err = Import(ctx, db, []byte(edited), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Removed) != 1 || len(rep.Added) != 1 || rep.Added[0] != "check nightly" {
		t.Fatalf("replace report = %+v %+v", rep, ch)
	}
	targets, _ = db.Targets(ctx)
	if len(targets) != 2 || targets[1].URL != "tgram://123:SECRET/1" {
		t.Errorf("targets after replace = %+v", targets)
	}
}

func TestValidate(t *testing.T) {
	bad := []store.Check{
		{Type: store.HTTP, Target: "https://x"},
		{Name: "x", Type: "smtp", Target: "x"},
		{Name: "x", Type: store.HTTP, Target: "example.com"},
		{Name: "x", Type: store.TCP, Target: "example.com"},
		{Name: "x", Type: store.HTTP, Target: "https://x", Interval: 5},
		{Name: "x", Type: store.Ping},
	}
	for _, c := range bad {
		if err := Validate(&c); err == nil {
			t.Errorf("%+v: expected an error", c)
		}
	}
	c := store.Check{Name: " hb ", Type: store.Push, Tags: []string{"A", "a", " "}}
	if err := Validate(&c); err != nil || c.Token == "" || c.Name != "hb" || len(c.Tags) != 1 {
		t.Errorf("push = %+v, %v", c, err)
	}
}
