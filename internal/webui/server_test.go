package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"srunkeeper/internal/config"
	"srunkeeper/internal/keeper"
)

func TestConfigAPIAuthMaskingAndSave(t *testing.T) {
	dir := t.TempDir()
	c := config.Defaults()
	c.User.Accounts = map[string]string{"a": "secret"}
	if err := config.Save(dir, c); err != nil {
		t.Fatal(err)
	}
	k := keeper.New(c)
	h := newHandler(k, dir, "token", "127.0.0.1:1234")
	request := func(method, path, body, token, origin, host string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
		r.Header.Set("X-SrunKeeper-Token", token)
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/config", "", "", "", "127.0.0.1:1234"); w.Code != 401 {
		t.Fatal("unauthenticated config access")
	}
	if w := request("GET", "/api/config", "", "token", "http://evil.test", "127.0.0.1:1234"); w.Code != 403 {
		t.Fatal("cross-origin request allowed")
	}
	if w := request("GET", "/api/config", "", "token", "", "evil.test"); w.Code != 403 {
		t.Fatal("foreign host allowed")
	}
	w := request("GET", "/api/config", "", "token", "", "127.0.0.1:1234")
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal("password exposed")
	}
	var masked config.Config
	if json.Unmarshal(w.Body.Bytes(), &masked) != nil {
		t.Fatal("invalid config JSON")
	}
	masked.System.CheckIntervalSeconds = 10
	body, _ := json.Marshal(masked)
	if w = request("POST", "/api/config", string(body), "token", "", "127.0.0.1:1234"); w.Code != 200 {
		t.Fatalf("save failed: %s", w.Body.String())
	}
	got, err := config.Load(dir)
	if err != nil || got.User.Accounts["a"] != "secret" || k.Snapshot().Interval != 10 {
		t.Fatal("password preservation or apply failed")
	}
	masked.User.Devices = []config.Device{{IP: "not-an-ip", Description: "bad", Account: "a"}}
	body, _ = json.Marshal(masked)
	if w = request("POST", "/api/config", string(body), "token", "", "127.0.0.1:1234"); w.Code != 400 {
		t.Fatal("invalid config saved")
	}
}
func TestInitDoesNotOverwriteAndStaticAssets(t *testing.T) {
	dir := t.TempDir()
	if err := InitConfig(dir); err != nil {
		t.Fatal(err)
	}
	c, _ := config.Load(dir)
	c.System.CheckIntervalSeconds = 90
	if err := config.Save(dir, c); err != nil {
		t.Fatal(err)
	}
	if err := InitConfig(dir); err != nil {
		t.Fatal(err)
	}
	got, _ := config.Load(dir)
	if got.System.CheckIntervalSeconds != 90 {
		t.Fatal("init overwrote config")
	}
	h := newHandler(keeper.New(c), dir, "t", "localhost")
	for _, path := range []string{"/", "/app.js", "/style.css", "/logo.png", "/pixel-font.ttf", "/pixel-font-license.txt"} {
		r := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("asset missing %s", path)
		}
	}
}

func TestIndependentSectionSaves(t *testing.T) {
	dir := t.TempDir()
	c := config.Defaults()
	c.User.Accounts = map[string]string{"a": "secret"}
	c.User.Devices = []config.Device{{IP: "192.168.1.2", Description: "computer", Account: "a"}}
	if err := config.Save(dir, c); err != nil {
		t.Fatal(err)
	}
	schoolBefore, _ := os.ReadFile(filepath.Join(dir, "school.json"))
	systemBefore, _ := os.ReadFile(filepath.Join(dir, "system.json"))
	h := newHandler(keeper.New(c), dir, "token", "localhost")
	post := func(section, body string) int {
		r := httptest.NewRequest("POST", "http://localhost/api/config/"+section, strings.NewReader(body))
		r.Header.Set("X-SrunKeeper-Token", "token")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := post("accounts", `{"account":{"a":"","b":"new-password"}}`); code != 200 {
		t.Fatalf("account save: %d", code)
	}
	got, err := config.Load(dir)
	if err != nil || got.User.Accounts["a"] != "secret" || len(got.User.Devices) != 1 {
		t.Fatal("account save lost password or devices")
	}
	schoolAfter, _ := os.ReadFile(filepath.Join(dir, "school.json"))
	systemAfter, _ := os.ReadFile(filepath.Join(dir, "system.json"))
	if string(schoolBefore) != string(schoolAfter) || string(systemBefore) != string(systemAfter) {
		t.Fatal("account save touched other files")
	}
	if code := post("devices", `{"device":[{"ip":"192.168.1.3","description":"phone","account":"b"}]}`); code != 200 {
		t.Fatalf("device save: %d", code)
	}
	got, _ = config.Load(dir)
	if got.User.Accounts["b"] != "new-password" || got.User.Devices[0].Account != "b" {
		t.Fatal("device save changed accounts")
	}
	if code := post("accounts", `{"account":{"a":""}}`); code != 400 {
		t.Fatal("removed referenced account")
	}
	if code := post("devices", `{"device":[],"account":{}}`); code != 400 {
		t.Fatal("device endpoint accepted account changes")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "user.json"))
	body, _ := json.Marshal(struct {
		School config.School `json:"school"`
		System config.System `json:"system"`
	}{c.School, config.System{CheckIntervalSeconds: 30}})
	if code := post("settings", string(body)); code != 200 {
		t.Fatalf("settings save: %d", code)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "user.json"))
	if string(before) != string(after) {
		t.Fatal("settings save touched user file")
	}
}

type waitingAuth struct{ entered chan struct{} }

func (a *waitingAuth) Check(ctx context.Context, ip string) (bool, error) {
	a.entered <- struct{}{}
	<-ctx.Done()
	return false, ctx.Err()
}
func (a *waitingAuth) Login(context.Context, string, string, string) error { return nil }
func TestServiceStartStopAndRestart(t *testing.T) {
	c := config.Defaults()
	c.User.Devices = []config.Device{{IP: "192.168.1.2", Description: "test", Account: "a"}}
	auth := &waitingAuth{entered: make(chan struct{}, 2)}
	k := keeper.NewWithFactory(c, func(config.School) keeper.Auth { return auth })
	control := &serviceControl{k: k, ctx: context.Background()}
	control.start()
	defer control.stop()
	select {
	case <-auth.entered:
	case <-time.After(time.Second):
		t.Fatal("service did not start")
	}
	control.start()
	select {
	case <-auth.entered:
		t.Fatal("duplicate service started")
	default:
	}
	control.stop()
	if k.Snapshot().Running || control.active() {
		t.Fatal("service did not stop")
	}
	if len(k.Snapshot().Logs["192.168.1.2"]) < 2 {
		t.Fatal("service logs missing")
	}
	control.start()
	select {
	case <-auth.entered:
	case <-time.After(time.Second):
		t.Fatal("service did not restart")
	}
	control.stop()
}
