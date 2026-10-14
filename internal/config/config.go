package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type School struct {
	Description string `json:"description"`
	Host        string `json:"host"`
	ACID        string `json:"ac_id"`
	N           string `json:"n"`
	Type        string `json:"type"`
	EncVer      string `json:"enc_ver"`
}
type Device struct {
	IP          string `json:"ip"`
	Description string `json:"description"`
	Account     string `json:"account"`
}
type User struct {
	Accounts map[string]string `json:"account"`
	Devices  []Device          `json:"device"`
}
type System struct {
	CheckIntervalSeconds int `json:"check_interval_seconds"`
}
type Config struct {
	School School `json:"school"`
	User   User   `json:"user"`
	System System `json:"system"`
}

func Defaults() Config {
	return Config{
		School: School{Host: "http://192.168.112.30", ACID: "0", N: "200", Type: "1", EncVer: "srun_bx1"},
		User:   User{Accounts: map[string]string{}, Devices: []Device{}},
		System: System{CheckIntervalSeconds: 60},
	}
}

func decode(path string, dst any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("%s: 配置格式错误: %w", filepath.Base(path), err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%s: 只允许一个 JSON 对象", filepath.Base(path))
	}
	return nil
}

func Load(dir string) (Config, error) {
	c := Defaults()
	for _, part := range []struct {
		name string
		dst  any
	}{{"school.json", &c.School}, {"user.json", &c.User}, {"system.json", &c.System}} {
		if err := decode(filepath.Join(dir, part.name), part.dst); err != nil {
			return c, err
		}
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	u, err := url.Parse(c.School.Host)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("school.json: host 必须是 http/https 认证服务器根地址")
	}
	if c.School.ACID == "" || c.School.N == "" || c.School.Type == "" || c.School.EncVer != "srun_bx1" {
		return errors.New("school.json: 认证参数不能为空，目前仅支持 srun_bx1")
	}
	if c.System.CheckIntervalSeconds < 1 || c.System.CheckIntervalSeconds > 86400 {
		return errors.New("system.json: 检测间隔必须为 1–86400 秒")
	}
	seen := map[string]bool{}
	for name, pass := range c.User.Accounts {
		if strings.TrimSpace(name) == "" || pass == "" {
			return errors.New("user.json: 账号和密码不能为空")
		}
	}
	for _, dev := range c.User.Devices {
		addr, err := netip.ParseAddr(dev.IP)
		if err != nil || addr.Zone() != "" || addr.IsUnspecified() || addr.IsMulticast() {
			return fmt.Errorf("user.json: 无效设备 IP %q", dev.IP)
		}
		key := addr.Unmap().String()
		if seen[key] {
			return fmt.Errorf("user.json: 设备 IP 重复 %s", dev.IP)
		}
		seen[key] = true
		if strings.TrimSpace(dev.Description) == "" {
			return fmt.Errorf("user.json: %s 缺少 description", dev.IP)
		}
		if _, ok := c.User.Accounts[dev.Account]; !ok {
			return fmt.Errorf("user.json: %s 引用了不存在的账号", dev.IP)
		}
	}
	return nil
}

// Save writes each layer atomically. The GUI requires stopping the process to edit
// files externally; all GUI writes are serialized by its handler.
func Save(dir string, c Config) error {
	return SaveSections(dir, c)
}

// SaveSections validates the whole configuration but writes only selected files.
func SaveSections(dir string, c Config, names ...string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for _, part := range []struct {
		name string
		data any
	}{{"school.json", c.School}, {"user.json", c.User}, {"system.json", c.System}} {
		if len(names) > 0 {
			selected := false
			for _, name := range names {
				if name == part.name {
					selected = true
				}
			}
			if !selected {
				continue
			}
		}
		b, err := json.MarshalIndent(part.data, "", "  ")
		if err != nil {
			return err
		}
		b = append(b, '\n')
		f, err := os.CreateTemp(dir, ".config-*.tmp")
		if err != nil {
			return err
		}
		name := f.Name()
		if err = f.Chmod(0600); err == nil {
			_, err = f.Write(b)
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(name, filepath.Join(dir, part.name))
		}
		if err != nil {
			os.Remove(name)
			return err
		}
	}
	return nil
}
