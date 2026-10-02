package speedtest

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/audemed44/lookout/internal/store"
)

func TestJudge(t *testing.T) {
	cfg := store.SpeedtestSettings{MinDownload: 100, MinUpload: 50, MaxPing: 20}
	if _, ok := Judge(store.Speedtest{Download: 180, Upload: 190, Ping: 5}, cfg); ok {
		t.Error("alerted on a good result")
	}
	n, ok := Judge(store.Speedtest{Download: 80, Upload: 190, Ping: 30, Server: "X"}, cfg)
	if !ok || n.Title != "Slow internet" || !strings.Contains(n.Body, "download 80 Mbit/s (under 100)") || !strings.Contains(n.Body, "ping 30 ms") {
		t.Errorf("slow: %+v", n)
	}
	if n, ok := Judge(store.Speedtest{Error: "no server"}, store.SpeedtestSettings{}); !ok || n.Title != "Speedtest failed" {
		t.Errorf("failed: %+v", n)
	}
	if _, ok := Judge(store.Speedtest{Download: 1}, store.SpeedtestSettings{}); ok {
		t.Error("alerted without thresholds")
	}
}

func TestRunner(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	calls := 0
	Measure = func(ctx context.Context, server string) (store.Speedtest, error) {
		calls++
		if calls == 2 {
			return store.Speedtest{}, errors.New("boom")
		}
		time.Sleep(50 * time.Millisecond)
		return store.Speedtest{Download: 179, Upload: 188, Ping: 5.4, Server: "Excitel"}, nil
	}
	defer func() { Measure = measure }()
	r := New(db, nil)
	wait := func() {
		for i := 0; i < 100; i++ {
			if running, _, _ := r.Status(); !running {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("still running")
	}
	if err := r.Start("manual"); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("manual"); err != ErrRunning {
		t.Errorf("second start: %v", err)
	}
	wait()
	_ = r.Start("scheduled")
	wait()
	list, _ := db.Speedtests(context.Background(), time.Time{})
	if len(list) != 2 || list[0].Download != 179 || list[0].Source != "manual" || list[1].Error != "boom" {
		t.Fatalf("results = %+v", list)
	}
	latest, _ := db.LatestSpeedtest(context.Background())
	if latest.ID != list[0].ID {
		t.Error("latest counts the failed run")
	}
}
