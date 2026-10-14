package keeper

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"srunkeeper/internal/config"
)

type fakeAuth struct {
	mu     sync.Mutex
	online map[string]bool
	logins []string
	fail   string
}

func (f *fakeAuth) Check(ctx context.Context, ip string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ip == f.fail {
		return false, errors.New("connection failed")
	}
	return f.online[ip], nil
}
func (f *fakeAuth) Login(ctx context.Context, u, p, ip string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins = append(f.logins, ip)
	f.online[ip] = true
	return nil
}
func TestOnlyOfflineDevicesLoginAndFailuresAreIsolated(t *testing.T) {
	c := config.Defaults()
	c.User.Accounts = map[string]string{"a": "p"}
	c.User.Devices = []config.Device{{IP: "192.168.1.1", Description: "online", Account: "a"}, {IP: "192.168.1.2", Description: "broken", Account: "a"}, {IP: "192.168.1.3", Description: "offline", Account: "a"}}
	f := &fakeAuth{online: map[string]bool{"192.168.1.1": true}, fail: "192.168.1.2"}
	k := NewWithFactory(c, func(config.School) Auth { return f })
	k.Cycle(context.Background())
	k.Cycle(context.Background())
	if len(f.logins) != 1 || f.logins[0] != "192.168.1.3" {
		t.Fatalf("unexpected logins: %v", f.logins)
	}
	s := k.Snapshot()
	if s.Devices[0].State != "online" || s.Devices[1].State != "error" || s.Devices[2].State != "online" {
		t.Fatalf("incorrect states: %+v", s)
	}
}
func TestRunCancellationAndManualTrigger(t *testing.T) {
	c := config.Defaults()
	f := &fakeAuth{online: map[string]bool{}}
	k := NewWithFactory(c, func(config.School) Auth { return f })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { k.Run(ctx); close(done) }()
	k.Trigger()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("keeper did not stop")
	}
	if k.Snapshot().Running {
		t.Fatal("keeper still marked running")
	}
}

type blockingAuth struct{ entered chan struct{} }

func (f *blockingAuth) Check(ctx context.Context, ip string) (bool, error) {
	select {
	case f.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return false, ctx.Err()
}
func (f *blockingAuth) Login(context.Context, string, string, string) error {
	return errors.New("must not login after cancellation")
}
func TestConfigChangeCancelsCycleAndOverlapsAreSkipped(t *testing.T) {
	c := config.Defaults()
	c.User.Accounts = map[string]string{"a": "p"}
	c.User.Devices = []config.Device{{IP: "192.168.1.2", Description: "test", Account: "a"}}
	f := &blockingAuth{entered: make(chan struct{}, 2)}
	k := NewWithFactory(c, func(config.School) Auth { return f })
	done := make(chan struct{})
	go func() { k.Cycle(context.Background()); close(done) }()
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("cycle never started")
	}
	k.Cycle(context.Background())
	select {
	case <-f.entered:
		t.Fatal("overlapping cycle started")
	default:
	}
	c.User.Devices = nil
	k.Update(c)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("configuration update did not cancel cycle")
	}
	if s := k.Snapshot(); len(s.Devices) != 0 || s.Busy {
		t.Fatalf("stale cycle state: %+v", s)
	}
}

func TestDeviceLogFormatRetentionAndSnapshotIsolation(t *testing.T) {
	c := config.Defaults()
	d := config.Device{IP: "192.168.1.2", Description: "电脑", Account: "a"}
	c.User.Devices = []config.Device{d}
	k := New(c)
	for i := 0; i < 105; i++ {
		k.record(d, "info", "已在线")
	}
	snap := k.Snapshot()
	entries := snap.Logs[d.IP]
	if len(entries) != 100 {
		t.Fatal("log retention is not bounded")
	}
	pattern := `^\[\d{4}/\d{2}/\d{2}\] \[\d{2}:\d{2}:\d{2}\] \[电脑\] \[192\.168\.1\.2\] : \[已在线\]$`
	if !regexp.MustCompile(pattern).MatchString(entries[0].Line) {
		t.Fatalf("wrong log format: %s", entries[0].Line)
	}
	entries[0].Line = "changed"
	if k.Snapshot().Logs[d.IP][0].Line == "changed" {
		t.Fatal("snapshot mutated internal logs")
	}
}
