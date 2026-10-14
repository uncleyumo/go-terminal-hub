package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"strings"
	"sync"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/hub"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
	"github.com/uncleyumo/go-terminal-hub/internal/remote"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// RemoteService 管「手机远程接管」这条链路。
//
// 生命周期用 Wails 自带的那套：app.Run() 里会跑 ServiceStartup，
// 退出时挨个叫 ServiceShutdown——不用自己造钩子。
type RemoteService struct {
	mu    sync.Mutex
	agent *remote.Agent
}

// ServiceStartup 在应用起来之后跑（这时 application.Get() 已经有效），
// 按配置决定要不要把桥拨起来。
func (x *RemoteService) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	x.mu.Lock()
	if x.agent == nil {
		x.agent = remote.NewAgent(hub.GetHub(), x.onStatusChanged)
	}
	agent := x.agent
	x.mu.Unlock()

	agent.Apply(x.loadConfig())
	return nil
}

// ServiceShutdown 断开连接，等它在途的帧写完再退——
// 否则浏览器会停在半截输出上，看不出是结束了还是断了。
func (x *RemoteService) ServiceShutdown() error {
	x.mu.Lock()
	agent := x.agent
	x.mu.Unlock()

	if agent != nil {
		agent.Stop()
	}
	return nil
}

// Reload 在设置保存后重新读配置。启用/停用、换地址、换令牌都走这一条路。
func (x *RemoteService) Reload() {
	x.mu.Lock()
	agent := x.agent
	x.mu.Unlock()

	if agent == nil {
		// ServiceStartup 还没跑（比如设置面板在启动阶段被调用），忽略即可
		return
	}
	agent.Apply(x.loadConfig())
}

// GetRemoteStatus 给桌面 UI 显示连接状态用。
func (x *RemoteService) GetRemoteStatus() remote.Status {
	x.mu.Lock()
	agent := x.agent
	x.mu.Unlock()

	if agent == nil {
		return remote.Status{State: remote.StateDisabled}
	}
	return agent.Status()
}

// ProbeResult 是「测试连接」的结果。
//
// Message 只在失败时有内容（后端报的错原文），成功时由前端出本地化文案。
type ProbeResult struct {
	OK      bool   `json:"ok"`
	Already bool   `json:"already"`
	Message string `json:"message"`
}

// TestConnection 拨一次 relay，看地址和 key 对不对。不落盘、不启用，就是一次试拨。
func (x *RemoteService) TestConnection(url, key string) ProbeResult {
	url = strings.TrimSpace(url)
	key = strings.TrimSpace(key)

	x.mu.Lock()
	agent := x.agent
	x.mu.Unlock()

	// ⚠️ 已经连着同一个地址时不能再拨：relay 上 agent 只有一个位置，
	// 新连接会把正在用的那条顶掉（关闭码 4002），本机要退避 60 秒才回来。
	// 这种情况直接拿现成的状态当结果——连接活着，就说明地址和 key 都对。
	if agent != nil {
		cfg, st := agent.Config(), agent.Status()
		if cfg.Enabled && st.State == remote.StateConnected && sameTarget(cfg.URL, url) {
			return ProbeResult{OK: true, Already: true}
		}
	}

	if err := remote.Probe(context.Background(), url, key); err != nil {
		return ProbeResult{Message: err.Error()}
	}
	return ProbeResult{OK: true}
}

// sameTarget 比较两个地址是不是同一个。
//
// 逐字比会漏：用户可能把 https:// 和 wss:// 当两个地址看，其实拨的是同一台机器。
// 归一化之后还比不了（比如地址写错了）就退化成逐字比。
func sameTarget(a, b string) bool {
	na, errA := remote.NormalizeURL(a)
	nb, errB := remote.NormalizeURL(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return na == nb
}

// GenerateSecret 生成登陆密钥，交给最终用户（手机）登录用。
//
// 32 字节随机再 base64——这个密钥的持有者能把任意按键送进本机终端，
// 强度不能靠人自己想。
func (x *RemoteService) GenerateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (x *RemoteService) loadConfig() remote.Config {
	storeInstance, err := store.GetStore()
	if err != nil {
		slog.Error("failed to load store for remote settings", "err", err)
		return remote.Config{}
	}
	s := storeInstance.GetSettings().Remote
	return remote.Config{
		Enabled:      s.Enabled,
		URL:          s.URL,
		Key:          s.Key,
		Secret:       s.Secret,
		AutoTakeover: s.AutoTakeover,
	}
}

// onStatusChanged 把状态变化推给前端。
//
// 注意事件类型要和 main.go 里 RegisterEvent 的完全一致，
// 否则 wails 会把它 Cancel 掉，前端一条也收不到。
func (x *RemoteService) onStatusChanged(s remote.Status) {
	app := application.Get()
	if app == nil {
		return
	}
	app.Event.Emit("remote:status", s)
}
