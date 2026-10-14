package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
	"github.com/uncleyumo/go-terminal-hub/internal/remote"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// store 是 sync.Once 单例，整个测试进程只有一个 data 目录。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "service-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("GO_TERMINAL_HUB_DIR", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fakeRelay 收下 agent 的拨入，然后挂着不动。
func fakeRelay(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer ws.CloseNow()
		// 一直读到连接断
		for {
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setRemoteSettings(t *testing.T, s store.RemoteSettings) {
	t.Helper()
	storeInst, err := store.GetStore()
	if err != nil {
		t.Fatal(err)
	}
	settings := storeInst.GetSettings()
	settings.Remote = s
	if err := storeInst.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
}

func waitForState(t *testing.T, svc *RemoteService, want string, timeout time.Duration) remote.Status {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last remote.Status
	for time.Now().Before(deadline) {
		last = svc.GetRemoteStatus()
		if last.State == want {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s 内状态没变成 %q，最后是 %+v", timeout, want, last)
	return last
}

// 启用之后桥要真的拨上去，状态变成 connected。
func TestServiceStartupConnects(t *testing.T) {
	srv := fakeRelay(t)
	setRemoteSettings(t, store.RemoteSettings{
		Enabled: true,
		URL:     "ws" + strings.TrimPrefix(srv.URL, "http"),
		Key:   "test-token-0123456789",
	})

	svc := &RemoteService{}
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	waitForState(t, svc, remote.StateConnected, 5*time.Second)
}

// 没启用的时候不该拨号，状态停在 disabled。
func TestServiceStartupDisabled(t *testing.T) {
	setRemoteSettings(t, store.RemoteSettings{Enabled: false})

	svc := &RemoteService{}
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	if got := svc.GetRemoteStatus().State; got != remote.StateDisabled {
		t.Errorf("状态 = %q, want %q", got, remote.StateDisabled)
	}
}

// 令牌为空时也不该拨：没有令牌的 relay 连接是没意义的。
func TestServiceStartupWithoutTokenStaysDisabled(t *testing.T) {
	setRemoteSettings(t, store.RemoteSettings{Enabled: true, URL: "ws://127.0.0.1:1/ws/agent", Key: ""})

	svc := &RemoteService{}
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	if got := svc.GetRemoteStatus().State; got != remote.StateDisabled {
		t.Errorf("状态 = %q, want %q", got, remote.StateDisabled)
	}
}

// Reload 让设置改动热生效，不用重启应用。
func TestReloadPicksUpNewSettings(t *testing.T) {
	srv := fakeRelay(t)
	setRemoteSettings(t, store.RemoteSettings{Enabled: false})

	svc := &RemoteService{}
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	if got := svc.GetRemoteStatus().State; got != remote.StateDisabled {
		t.Fatalf("起始状态 = %q, want disabled", got)
	}

	// 用户在地球图标里填好地址和令牌并保存
	setRemoteSettings(t, store.RemoteSettings{
		Enabled: true,
		URL:     "ws" + strings.TrimPrefix(srv.URL, "http"),
		Key:   "test-token-0123456789",
	})
	svc.Reload()

	waitForState(t, svc, remote.StateConnected, 5*time.Second)
}

// 关掉之后要断开，状态回到 disabled。
func TestReloadDisconnects(t *testing.T) {
	srv := fakeRelay(t)
	setRemoteSettings(t, store.RemoteSettings{
		Enabled: true,
		URL:     "ws" + strings.TrimPrefix(srv.URL, "http"),
		Key:   "test-token-0123456789",
	})

	svc := &RemoteService{}
	if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, svc, remote.StateConnected, 5*time.Second)

	setRemoteSettings(t, store.RemoteSettings{Enabled: false})
	svc.Reload()

	waitForState(t, svc, remote.StateDisabled, 5*time.Second)
}

// 令牌强度不能靠人自己想：这个令牌的持有者能把任意按键送进本机终端。
func TestGenerateSecretIsStrong(t *testing.T) {
	svc := &RemoteService{}

	a, err := svc.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}

	// 32 字节 base64（无填充）是 43 个字符
	if len(a) != 43 {
		t.Errorf("令牌长度 = %d, want 43（32 字节 base64）", len(a))
	}
	if a == b {
		t.Error("两次生成的令牌不该一样")
	}
}
