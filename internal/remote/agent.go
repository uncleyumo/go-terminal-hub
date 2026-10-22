// Package remote 是本地 hub 拨向云中转（relay）的那一端。
//
// 方向是反的：hub 主动拨出去，所以家里这台机器不用公网 IP、不用端口映射、
// 不用内网穿透。会话的唯一真相源仍然在这里——relay 只是搬帧。
package remote

import (
	"context"
	"errors"
	"fmt"
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
//
// Key 和 Secret 是两层身份，不能混：
//
//	Key    使用 key，中心站签发的凭证，只在 Authorization 头里出现，
//	       永远不给浏览器用户看。
//	Secret 登陆密钥，由本机生成后登记到中心站，最终用户（手机）拿它登录。
type Config struct {
	Enabled      bool   `json:"enabled"`
	URL          string `json:"url"`
	Key          string `json:"key"`
	Secret       string `json:"secret"`
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

	// browserOnline 记「浏览器端有没有人连着 relay」。
	//
	// 单独放一个字段，不塞在 status 里：status 会被 setStatus 整个换掉
	// （连接中、已连接、出错），换的时候不该把这个标记一起抹掉。
	browserOnline bool
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

	if !cfg.Enabled || cfg.URL == "" || cfg.Key == "" {
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

// Config 返回当前正在用的配置。
func (a *Agent) Config() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

func (a *Agent) setStatus(s Status) {
	a.mu.Lock()
	// 浏览器在不在是另一件事，别被这次状态替换顺手抹掉。
	s.BrowserOnline = a.browserOnline
	a.status = s
	a.mu.Unlock()
	if a.onStatus != nil {
		a.onStatus(s)
	}
}

// setBrowserOnline 记下浏览器端的在线状态，并把新状态推给桌面界面。
//
// 没人关心「是不是第一次变成这个值」以外的事，所以值没变就不通知。
func (a *Agent) setBrowserOnline(online bool) {
	a.mu.Lock()
	if a.browserOnline == online {
		a.mu.Unlock()
		return
	}
	a.browserOnline = online
	s := a.status
	s.BrowserOnline = online
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
		// 那个头里带的是**使用 key**，不是登陆密钥。
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + cfg.Key}},
	})
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(readLimit)

	// 密钥登记要赶在任何会话转发之前：中心站必须先知道这个 key 的登陆密钥，
	// 手机端才登得进来。发完等一句 registered，失败要让人看得见原因。
	if cfg.Secret != "" {
		if err := registerSecret(ctx, ws, cfg.Secret); err != nil {
			slog.Warn("register secret failed", "err", err)
			return err
		}
	}

	b := newBridge(ctx, a, ws, cfg)
	a.mu.Lock()
	a.bridge = b
	a.mu.Unlock()

	// 这条新连接还没收到中心站的问候，不知道浏览器端有没有人。
	// 先按「没人」记着，收到 browser_online 再改。
	//
	// 不这么做的话：浏览器在 agent 掉线期间走了，就再也没人告诉 agent
	// 「人已经不在了」，桌面会一直显示着「远程端在线」。
	a.setBrowserOnline(false)

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

// registerSecret 把登陆密钥登记到中心站。
//
// 必须等回执：中心站可能拒（key 过期、已吊销、密钥太短），
// 那时要让人看见原因，而不是「连上了但手机登不进来」这种事后才发现的问题。
// 回执里的 Msg 直接是中心站给的人话原因。
func registerSecret(ctx context.Context, ws *websocket.Conn, secret string) error {
	msg, err := protocol.Encode(protocol.Message{
		Type:   protocol.TypeRegisterSecret,
		Secret: secret,
	})
	if err != nil {
		return err
	}
	if err := ws.Write(ctx, websocket.MessageText, msg); err != nil {
		return fmt.Errorf("发送 register_secret: %w", err)
	}

	// 等回执，最多 10 秒。中心站不回就当失败——宁可重试也不能假装登记成功。
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return fmt.Errorf("等 register_secret 回执: %w", err)
		}
		if typ != websocket.MessageText {
			continue
		}
		m, err := protocol.Decode(data)
		if err != nil {
			continue
		}
		switch m.Type {
		case protocol.TypeRegistered:
			return nil
		case protocol.TypeRegisterRejected:
			return fmt.Errorf("中心站拒绝了登陆密钥: %s", m.Msg)
		}
		// 别的控制消息先放过去，桥还没起，丢了也无所谓。
	}
}
