package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"srunkeeper/internal/config"
	"srunkeeper/internal/keeper"
)

//go:embed assets/*
var assets embed.FS

func InitConfig(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	c := config.Defaults()
	for _, p := range []struct {
		name string
		v    any
	}{{"school.json", c.School}, {"user.json", c.User}, {"system.json", c.System}} {
		f, err := os.OpenFile(filepath.Join(dir, p.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		err = enc.Encode(p.v)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
func Run(ctx context.Context, k *keeper.Keeper, dir string, open bool) error {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		listener.Close()
		return err
	}
	token := hex.EncodeToString(b)
	addr := listener.Addr().String()
	base := "http://" + addr
	runctx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{Handler: newHandlerContext(runctx, k, dir, token, addr), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}

	go func() {
		<-runctx.Done()
		stop, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		server.Shutdown(stop)
	}()
	url := base + "/#" + token
	fmt.Println("本地管理界面（仅本机访问）：", url)
	if open {
		if err := openBrowser(url); err != nil {
			log.Print("浏览器未能自动打开，请复制上方地址")
		}
	}
	err = server.Serve(listener)
	cancel()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
func newHandler(k *keeper.Keeper, dir, token, host string) http.Handler {
	return newHandlerContext(context.Background(), k, dir, token, host)
}

func newHandlerContext(ctx context.Context, k *keeper.Keeper, dir, token, host string) http.Handler {
	control := &serviceControl{k: k, ctx: ctx}
	if ctx.Done() != nil {
		go func() { <-ctx.Done(); control.stop() }()
	}
	var writes sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.Host != host {
			http.Error(w, "无效主机", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-SrunKeeper-Token")), []byte(token)) != 1 {
				http.Error(w, "请使用终端中显示的完整管理地址打开", http.StatusUnauthorized)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+host {
				http.Error(w, "无效来源", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			switch r.URL.Path {
			case "/api/status":
				if r.Method != http.MethodGet {
					w.WriteHeader(405)
					return
				}
				json.NewEncoder(w).Encode(k.Snapshot())
			case "/api/service/start", "/api/service/stop":
				if r.Method != http.MethodPost {
					w.WriteHeader(405)
					return
				}
				if r.URL.Path == "/api/service/start" {
					if len(k.Snapshot().Devices) == 0 {
						http.Error(w, "请先保存至少一台设备", 400)
						return
					}
					control.start()
				} else {
					control.stop()
				}
				json.NewEncoder(w).Encode(map[string]bool{"ok": true})
			case "/api/check":
				if r.Method != http.MethodPost {
					w.WriteHeader(405)
					return
				}
				if !control.active() {
					http.Error(w, "请先启动服务", 409)
					return
				}
				k.Trigger()
				w.WriteHeader(http.StatusAccepted)
				io.WriteString(w, `{"ok":true}`)
			case "/api/config", "/api/config/accounts", "/api/config/devices", "/api/config/settings":
				writes.Lock()
				defer writes.Unlock()
				c, err := config.Load(dir)
				if err != nil {
					http.Error(w, "无法读取配置，请检查本地文件", 500)
					return
				}
				switch r.Method {
				case http.MethodGet:
					if r.URL.Path != "/api/config" {
						w.WriteHeader(405)
						return
					}
					for name := range c.User.Accounts {
						c.User.Accounts[name] = ""
					}
					json.NewEncoder(w).Encode(c)
				case http.MethodPost:
					if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
						http.Error(w, "需要 JSON 请求", 415)
						return
					}
					r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
					next := c
					d := json.NewDecoder(r.Body)
					d.DisallowUnknownFields()
					var decodeErr error
					var files []string
					switch r.URL.Path {
					case "/api/config/accounts":
						var body struct {
							Account *map[string]string `json:"account"`
						}
						decodeErr = d.Decode(&body)
						if decodeErr == nil && body.Account != nil {
							next.User.Accounts = *body.Account
							files = []string{"user.json"}
						} else {
							decodeErr = fmt.Errorf("missing account")
						}
					case "/api/config/devices":
						var body struct {
							Device *[]config.Device `json:"device"`
						}
						decodeErr = d.Decode(&body)
						if decodeErr == nil && body.Device != nil {
							next.User.Devices = *body.Device
							files = []string{"user.json"}
						} else {
							decodeErr = fmt.Errorf("missing device")
						}
					case "/api/config/settings":
						var body struct {
							School *config.School `json:"school"`
							System *config.System `json:"system"`
						}
						decodeErr = d.Decode(&body)
						if decodeErr == nil && body.School != nil && body.System != nil {
							next.School = *body.School
							next.System = *body.System
							files = []string{"school.json", "system.json"}
						} else {
							decodeErr = fmt.Errorf("missing settings")
						}
					default:
						next = config.Config{}
						decodeErr = d.Decode(&next)
					}
					if decodeErr != nil {
						http.Error(w, "配置格式错误", 400)
						return
					}
					var extra any
					if d.Decode(&extra) != io.EOF {
						http.Error(w, "配置格式错误", 400)
						return
					}
					for name, pass := range next.User.Accounts {
						if pass == "" {
							next.User.Accounts[name] = c.User.Accounts[name]
						}
					}
					if err = next.Validate(); err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					if err = config.SaveSections(dir, next, files...); err != nil {
						http.Error(w, "写入配置失败，请检查目录权限", 500)
						return
					}
					k.Update(next)
					if control.active() {
						k.Trigger()
					}
					io.WriteString(w, `{"ok":true}`)
				default:
					w.WriteHeader(405)
				}
			default:
				http.NotFound(w, r)
			}
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		name := r.URL.Path
		if name == "/" {
			name = "/index.html"
		}
		if name != "/index.html" && name != "/app.js" && name != "/style.css" && name != "/logo.png" && name != "/pixel-font.ttf" && name != "/pixel-font-license.txt" {
			http.NotFound(w, r)
			return
		}
		body, err := assets.ReadFile("assets" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch name {
		case "/index.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		case "/app.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case "/pixel-font.ttf":
			w.Header().Set("Content-Type", "font/ttf")
		case "/pixel-font-license.txt":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		case "/logo.png":
			w.Header().Set("Content-Type", "image/png")
		case "/style.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		w.Write(body)
	})
}

// Serialize start/stop and wait for cancellation before allowing a restart.
type serviceControl struct {
	mu     sync.Mutex
	k      *keeper.Keeper
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *serviceControl) start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	done := s.done
	go func() { defer close(done); s.k.Run(ctx) }()
}
func (s *serviceControl) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil {
		return
	}
	s.cancel()
	<-s.done
	s.cancel = nil
	s.done = nil
}
func (s *serviceControl) active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel != nil && s.ctx.Err() == nil
}
