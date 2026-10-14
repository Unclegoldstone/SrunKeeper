package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRoundTripAndValidation(t *testing.T) {
	c := Defaults()
	c.User = User{map[string]string{"student": "secret"}, []Device{{"192.168.1.2", "电脑", "student"}}}
	dir := t.TempDir()
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.User.Accounts["student"] != "secret" || got.User.Devices[0] != c.User.Devices[0] {
		t.Fatal("round trip changed user config")
	}
	got.User.Devices = append(got.User.Devices, got.User.Devices[0])
	if got.Validate() == nil {
		t.Fatal("duplicate IP accepted")
	}
	got = c
	got.User.Devices = []Device{{"192.168.1.3", "手机", "missing"}}
	if got.Validate() == nil {
		t.Fatal("unknown account accepted")
	}
	got = Defaults()
	got.System.CheckIntervalSeconds = 0
	if got.Validate() == nil {
		t.Fatal("zero interval accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "system.json"), []byte(`{"check_interval_seconds":60,"typo":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("unknown field accepted")
	}
}
