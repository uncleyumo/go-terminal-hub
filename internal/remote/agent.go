// Package remote 是本地 hub 拨向云中转（relay）的那一端。
//
// 方向是反的：hub 主动拨出去，所以家里这台机器不用公网 IP、不用端口映射、
// 不用内网穿透。会话的唯一真相源仍然在这里——relay 只是搬帧。
package remote

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/hub"
	"github.com/uncleyumo/go-terminal-hub/remote/protocol"
)

const (
	// 与 relay 侧一致：默认 32768 的读上限会被一次终端爆发顶爆。
	readLimit = 4 << 20

	initialBackoff = 2 * time.Second
	maxBackoff     = 30 * time.Second

	// 被另一个实例顶掉之后要退避久一点，否则两个桌面实例会互相顶成死循环。
	replacedBackoff = 60 * time.Second

	// 连上并且稳定跑了这么久，就把退避重置回起点。
	stableAfter = time.Minute
)

// 状态取值。前端拿它决定显示什么。
const (
	StateDisabled   = "disabled"
	StateConnecting = "connecting"
	StateConnected  = "connected"
	StateError      = "error"
)

// Config 是远程接管的配置，落在 settings.json 里。
type Config struct {
	Enabled      bool   `json:"enabled"`
	URL          string `json:"url"`
	Token        string `json:"token"`
	AutoTakeover bool   `json:"autoTakeover"`
}

// Status 是给桌面 UI 看的状态快照。
type Status struct {
	State         string `json:"state"`
	ErrMsg        string `json:"errMsg"`
	BrowserOnline bool   `json:"browserOnline"`
	URL           string `json:"url"`
}

// Agent 管一条到 relay 的连接，负责拨号、鉴权、重连，并把 hub 的生命周期
// 事件转给当前这条连接。
//
// 它是注册给 hub 的那个 Tap：Tap 是全局的，而连接会被反复重建，
// 所以由 Agent 当稳定的那一方，转发给「当前」的 bridge。
type Agent struct {
	hub      *hub.Hub
	onStatus func(Status)

	mu     sync.Mutex
	cfg    Config
	status Status
	cancel context.CancelFunc
	done   chan struct{}
	bridge *bridge
}

func NewAgent(h *hub.Hub, onStatus func(Status)) *Agent {
	a := &Agent{
		hub:      h,
		onStatus: onStatus,
		status:   Status{State: StateDisabled},
	}
	h.SetTap(a)
	return a
}

// Apply 按新配置重启连接。停用、换地址、换令牌都走这一条路。
func (a *Agent) Apply(cfg Config) {
	a.stopAndWait()

	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()

	if !cfg.Enabled || cfg.URL == "" || cfg.Token == "" {
		a.setStatus(Status{State: StateDisabled, URL: cfg.URL})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	a.mu.Lock()
	a.cancel, a.done = cancel, done
	a.mu.Unlock()

	go a.run(ctx, done, cfg)
}

// Stop 断开并停掉重连。进程退出时调用。
func (a *Agent) Stop() {
	a.stopAndWait()
	a.mu.Lock()
	a.status = Status{State: StateDisabled}
	a.mu.Unlock()
}

func (a *Agent) stopAndWait() {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.cancel, a.done = nil, nil
	a.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		slog.Warn("remote agent did not stop in time")
	}
}

func (a *Agent) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

func (a *Agent) setStatus(s Status) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
	if a.onStatus != nil {
		a.onStatus(s)
	}
}

// currentBridge 返回当前这条连接的桥，没有就返回 nil。
func (a *Agent) currentBridge() *bridge {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bridge
}

// --- hub.Tap 的实现 ---------------------------------------------------------
//
// hub 不能 import 这个包，所以由这里实现接口、反过来注册进去。

func (a *Agent) OnStarted(id string) {
	if b := a.currentBridge(); b != nil {
		b.onSessionStarted(id)
	}
}

func (a *Agent) OnExited(id string, res executor.ExitResult) {
	if b := a.currentBridge(); b != nil {
		b.onSessionExited(id, res)
	}
}

func (a *Agent) OnLease(id, controller string, cols, rows uint16) {
	if b := a.currentBridge(); b != nil {
		b.onLeaseChanged(id, controller, cols, rows)
	}
}

// --- 连接循环 ---------------------------------------------------------------

func (a *Agent) run(ctx context.Context, done chan struct{}, cfg Config) {
	defer close(done)

	backoff := initialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		a.setStatus(Status{State: StateConnecting, URL: cfg.URL})

		start := time.Now()
		err := a.connectOnce(ctx, cfg)
		if ctx.Err() != nil {
			return
		}

		next := backoff
		if time.Since(start) > stableAfter {
			// 稳定跑过一段，说明上次的失败已经过去了
			next = initialBackoff
		}
		if websocket.CloseStatus(err) == protocol.CloseAgentReplaced {
			next = replacedBackoff
			slog.Warn("another instance took over the relay slot; backing off")
		} else if next < maxBackoff {
			next *= 2
			if next > maxBackoff {
				next = maxBackoff
			}
		}
		backoff = next

		a.setStatus(Status{State: StateError, ErrMsg: errText(err), URL: cfg.URL})

		wait := backoff + time.Duration(rand.Int63n(int64(backoff/4+1)))
		slog.Info("remote bridge reconnecting", "in", wait.Round(time.Second), "err", errText(err))

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (a *Agent) connectOnce(ctx context.Context, cfg Config) error {
	ws, _, err := websocket.Dial(ctx, cfg.URL, &websocket.DialOptions{
		// agent 是 Go 程序，能设请求头，所以用 Bearer；浏览器不能，走 cookie。
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + cfg.Token}},
	})
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(readLimit)

	b := newBridge(ctx, a, ws, cfg)
	a.mu.Lock()
	a.bridge = b
	a.mu.Unlock()

	a.setStatus(Status{State: StateConnected, URL: cfg.URL})
	slog.Info("remote bridge connected", "url", cfg.URL)

	err = b.run()

	a.mu.Lock()
	if a.bridge == b {
		a.bridge = nil
	}
	a.mu.Unlock()

	// 连接断了就没人能控制了，把所有 web 租约还给桌面——
	// 否则终端会卡在「控制方是手机，但手机不在了」的状态。
	b.releaseAllWebLeases()
	return err
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return ""
	}
	return err.Error()
}
