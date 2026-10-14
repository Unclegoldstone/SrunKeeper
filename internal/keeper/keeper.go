package keeper

import (
	"context"
	"fmt"
	"sync"
	"time"

	"srunkeeper/internal/config"
	"srunkeeper/internal/srun"
)

type Auth interface {
	Check(context.Context, string) (bool, error)
	Login(context.Context, string, string, string) error
}
type Status struct {
	IP          string    `json:"ip"`
	Description string    `json:"description"`
	Account     string    `json:"account"`
	State       string    `json:"state"`
	Message     string    `json:"message"`
	CheckedAt   time.Time `json:"checked_at"`
}
type Entry struct {
	Line    string    `json:"line"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type Snapshot struct {
	Logs     map[string][]Entry `json:"logs"`
	Devices  []Status           `json:"devices"`
	Running  bool               `json:"running"`
	Busy     bool               `json:"busy"`
	Interval int                `json:"interval"`
}
type Keeper struct {
	mu            sync.RWMutex
	cfg           config.Config
	logs          map[string][]Entry
	states        []Status
	busy, running bool
	wake          chan struct{}
	cancelCycle   context.CancelFunc
	factory       func(config.School) Auth
}

func New(c config.Config) *Keeper {
	return NewWithFactory(c, func(s config.School) Auth { return srun.New(s) })
}
func NewWithFactory(c config.Config, f func(config.School) Auth) *Keeper {
	k := &Keeper{wake: make(chan struct{}, 1), factory: f}
	k.Update(c)
	return k
}
func (k *Keeper) Update(c config.Config) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cancelCycle != nil {
		k.cancelCycle()
	}
	logs := make(map[string][]Entry, len(c.User.Devices))
	for _, d := range c.User.Devices {
		logs[d.IP] = k.logs[d.IP]
	}
	k.logs = logs
	k.cfg = c
	k.states = make([]Status, 0, len(c.User.Devices))
	for _, d := range c.User.Devices {
		k.states = append(k.states, Status{IP: d.IP, Description: d.Description, Account: d.Account, State: "pending", Message: "等待检测"})
	}
}
func (k *Keeper) Snapshot() Snapshot {
	k.mu.RLock()
	defer k.mu.RUnlock()
	logs := make(map[string][]Entry, len(k.logs))
	for ip, entries := range k.logs {
		logs[ip] = append([]Entry{}, entries...)
	}
	return Snapshot{Devices: append([]Status{}, k.states...), Running: k.running, Busy: k.busy, Interval: k.cfg.System.CheckIntervalSeconds, Logs: logs}
}
func (k *Keeper) Trigger() {
	select {
	case k.wake <- struct{}{}:
	default:
	}
}
func (k *Keeper) Run(ctx context.Context) {
	k.mu.Lock()
	if k.running {
		k.mu.Unlock()
		return
	}
	k.running = true
	devices := append([]config.Device{}, k.cfg.User.Devices...)
	k.mu.Unlock()
	for _, d := range devices {
		k.record(d, "info", "自动重连服务已启动")
	}
	defer func() {
		k.mu.Lock()
		k.running = false
		devices := append([]config.Device{}, k.cfg.User.Devices...)
		k.mu.Unlock()
		for _, d := range devices {
			k.record(d, "info", "自动重连服务已停止")
		}
	}()
	for {
		k.Cycle(ctx)
		k.mu.RLock()
		interval := k.cfg.System.CheckIntervalSeconds
		k.mu.RUnlock()
		timer := time.NewTimer(time.Duration(interval) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-k.wake:
			timer.Stop()
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}
	}
}
func (k *Keeper) Cycle(ctx context.Context) {
	k.mu.Lock()
	if k.busy {
		k.mu.Unlock()
		return
	}
	k.busy = true
	c := k.cfg
	ctx, cancel := context.WithCancel(ctx)
	k.cancelCycle = cancel
	k.mu.Unlock()
	defer func() { cancel(); k.mu.Lock(); k.busy = false; k.cancelCycle = nil; k.mu.Unlock() }()
	auth := k.factory(c.School)
	if closer, ok := auth.(interface{ Close() }); ok {
		defer closer.Close()
	}
	for i, d := range c.User.Devices {
		if ctx.Err() != nil {
			return
		}
		state, message := "online", "已在线"
		k.record(d, "info", "开始检查在线状态")
		online, err := auth.Check(ctx, d.IP)
		if err != nil {
			state, message = "error", err.Error()
		} else if !online {
			k.record(d, "warn", "设备未在线，正在提交认证")
			err = auth.Login(ctx, d.Account, c.User.Accounts[d.Account], d.IP)
			if err == nil {
				online, err = auth.Check(ctx, d.IP)
				if err == nil && !online {
					state, message = "pending", "认证已提交，等待在线状态确认"
				} else if err == nil {
					message = "登录成功，已确认在线"
				}
			}
			if err != nil {
				state, message = "error", err.Error()
			}
		}
		if ctx.Err() != nil {
			return
		}
		k.mu.Lock()
		// A GUI configuration save may replace the device list during this cycle.
		if i < len(k.states) && k.cfg.User.Devices[i] == d && k.cfg.School == c.School && k.cfg.User.Accounts[d.Account] == c.User.Accounts[d.Account] {
			k.states[i] = Status{d.IP, d.Description, d.Account, state, message, time.Now()}
		}
		k.mu.Unlock()
		level := "info"
		if state == "error" {
			level = "error"
		}
		k.record(d, level, message)
	}
}

func (k *Keeper) record(d config.Device, level, message string) {
	now := time.Now()
	line := fmt.Sprintf("[%s] [%s] [%s] [%s] : [%s]", now.Format("2006/01/02"), now.Format("15:04:05"), d.Description, d.IP, message)
	k.mu.Lock()
	if entries, ok := k.logs[d.IP]; ok {
		entries = append(entries, Entry{Time: now, Level: level, Message: message, Line: line})
		if len(entries) > 100 {
			entries = append([]Entry{}, entries[len(entries)-100:]...)
		}
		k.logs[d.IP] = entries
	}
	k.mu.Unlock()
	fmt.Println(line)
}
