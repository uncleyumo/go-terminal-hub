package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/hub"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
	"github.com/uncleyumo/go-terminal-hub/remote/protocol"
)

// store 是 sync.Once 单例，整个测试进程只有一个 data 目录。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "remote-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("GO_TERMINAL_HUB_DIR", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// peer 扮演「relay + 浏览器」两侧。
//
// 桥只跟一条 WebSocket 对话，所以测试里不需要真的起一个 relay：
// 这个 server 接受 agent 拨入，然后在同一条连接上直接收发协议帧，
// 等于同时当了中继和浏览器。relay 自己的行为另有测试覆盖。
type peer struct {
	ws *websocket.Conn
	t  *testing.T
}

func startPeer(t *testing.T) (*httptest.Server, <-chan *peer) {
	t.Helper()
	conns := make(chan *peer, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		ws.SetReadLimit(4 << 20)
		conns <- &peer{ws: ws, t: t}
	}))
	t.Cleanup(srv.Close)
	return srv, conns
}

func (p *peer) send(m protocol.Message) {
	p.t.Helper()
	data, err := protocol.Encode(m)
	if err != nil {
		p.t.Fatal(err)
	}
	if err := p.ws.Write(context.Background(), websocket.MessageText, data); err != nil {
		p.t.Fatalf("send %s: %v", m.Type, err)
	}
}

func (p *peer) sendBinary(b []byte) {
	p.t.Helper()
	if err := p.ws.Write(context.Background(), websocket.MessageBinary, b); err != nil {
		p.t.Fatalf("send binary: %v", err)
	}
}

// recv 读到指定类型的控制消息，途中的二进制帧交给 onBinary（可为 nil）。
func (p *peer) recv(want protocol.MsgType, timeout time.Duration, onBinary func([]byte)) protocol.Message {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		typ, data, err := p.ws.Read(ctx)
		if err != nil {
			p.t.Fatalf("等待 %q 时读失败: %v", want, err)
		}
		if typ == websocket.MessageBinary {
			if onBinary != nil {
				onBinary(data)
			}
			continue
		}
		var m protocol.Message
		if err := json.Unmarshal(data, &m); err != nil {
			p.t.Fatalf("控制帧解析失败: %v", err)
		}
		if m.Type == want {
			return m
		}
	}
}

// collectOutput 收二进制帧，直到看到 needle 或者超时。
func (p *peer) collectOutput(needle string, timeout time.Duration) string {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	var sb strings.Builder

	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Until(deadline))
		typ, data, err := p.ws.Read(ctx)
		cancel()
		if err != nil {
			break
		}
		if typ != websocket.MessageBinary {
			continue
		}
		sb.Write(data)
		if strings.Contains(sb.String(), needle) {
			return sb.String()
		}
	}
	p.t.Fatalf("在 %s 内没等到 %q，已收到 %q", timeout, needle, sb.String())
	return ""
}

const testSessionID = "01930000-0000-7000-8000-0000000000aa"

// newTestSession 在 store 里建一个常驻 cmd.exe 会话并启动它。
// 常驻是为了能往 stdin 写东西验证输入链路。
func newTestSession(t *testing.T) *hub.Hub {
	t.Helper()

	storeInst, err := store.GetStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := storeInst.UpdateDataJson(nil); err != nil {
		t.Fatal(err)
	}
	if err := storeInst.Add(store.DataStore{
		ID:   testSessionID,
		Name: "remote test",
		Kind: "terminal-cmd",
		Mode: "terminal",
		Cols: 80,
		Rows: 25,
	}); err != nil {
		t.Fatal(err)
	}

	h := hub.NewHub()
	if err := h.StartSession(testSessionID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Stop(testSessionID) })
	return h
}

// setup 起一个测试 server，把 agent 拨上去，返回对端。
func setup(t *testing.T, h *hub.Hub, cfg Config) *peer {
	t.Helper()

	srv, conns := startPeer(t)
	agent := NewAgent(h, nil)
	t.Cleanup(agent.Stop)

	cfg.Enabled = true
	cfg.URL = "ws" + strings.TrimPrefix(srv.URL, "http")
	cfg.Token = "irrelevant-in-this-test"
	agent.Apply(cfg)

	select {
	case p := <-conns:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内没等到 agent 拨入")
		return nil
	}
}

// --- 测试 -------------------------------------------------------------------

// 全链路：列会话 → 绑定（带历史回放）→ 接管 → 输入 → 交还。
func TestBridgeEndToEnd(t *testing.T) {
	h := newTestSession(t)
	p := setup(t, h, Config{})

	// 1. 列会话：要有名称（来自 store），不能只有 UUID
	p.send(protocol.Message{Type: protocol.TypeList})
	sessions := p.recv(protocol.TypeSessions, 5*time.Second, nil)
	if len(sessions.Sessions) != 1 {
		t.Fatalf("会话数 = %d, want 1", len(sessions.Sessions))
	}
	if sessions.Sessions[0].Name != "remote test" {
		t.Errorf("会话名 = %q, want %q", sessions.Sessions[0].Name, "remote test")
	}
	if !sessions.Sessions[0].Running {
		t.Error("会话应当在运行")
	}

	// 2. 绑定：应当收到 attached，紧接着是二进制回放
	var replay []byte
	p.send(protocol.Message{Type: protocol.TypeAttach, ID: testSessionID})
	attached := p.recv(protocol.TypeAttached, 5*time.Second, func(b []byte) { replay = append(replay, b...) })
	if attached.ID != testSessionID {
		t.Errorf("attached.ID = %q, want %q", attached.ID, testSessionID)
	}
	if attached.Controller != hub.SourceDesktop {
		t.Errorf("初始控制方 = %q, want %q", attached.Controller, hub.SourceDesktop)
	}

	// 3. 旁观时输入必须无效
	p.sendBinary([]byte("echo SHOULD_NOT_APPEAR\r"))
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(string(replay), "SHOULD_NOT_APPEAR") {
		t.Error("旁观方的输入不该进到终端里")
	}

	// 4. 报尺寸（记下但不下发），然后接管
	p.send(protocol.Message{Type: protocol.TypeResize, Cols: 48, Rows: 20})
	p.send(protocol.Message{Type: protocol.TypeTakeover})

	lease := p.recv(protocol.TypeLease, 5*time.Second, nil)
	if lease.Controller != hub.SourceWeb {
		t.Fatalf("接管后控制方 = %q, want %q", lease.Controller, hub.SourceWeb)
	}
	if lease.Cols != 48 || lease.Rows != 20 {
		t.Errorf("接管后尺寸 = %dx%d, want 48x20", lease.Cols, lease.Rows)
	}

	// PTY 真的切到了手机尺寸
	if cols, rows, ok := h.Size(testSessionID); !ok || cols != 48 || rows != 20 {
		t.Errorf("hub 记录的尺寸 = %dx%d (ok=%v), want 48x20", cols, rows, ok)
	}

	// 5. 接管后输入要能到终端里
	p.sendBinary([]byte("echo HELLO_FROM_PHONE\r"))
	if got := p.collectOutput("HELLO_FROM_PHONE", 5*time.Second); !strings.Contains(got, "HELLO_FROM_PHONE") {
		t.Errorf("终端输出里没看到输入的痕迹：%q", got)
	}

	// 6. 交还控制权：PTY 切回桌面尺寸
	p.send(protocol.Message{Type: protocol.TypeRelease})
	lease = p.recv(protocol.TypeLease, 5*time.Second, nil)
	if lease.Controller != hub.SourceDesktop {
		t.Fatalf("交还后控制方 = %q, want %q", lease.Controller, hub.SourceDesktop)
	}
	if lease.Cols != 80 || lease.Rows != 25 {
		t.Errorf("交还后尺寸 = %dx%d, want 80x25", lease.Cols, lease.Rows)
	}
}

// 浏览器断开（relay 会插一条 browser_offline）时，web 租约必须还给桌面。
// 少了这一步，桌面会永远卡在旁观模式：租约还写着 web，而没人能把它改回来。
func TestBrowserOfflineReleasesLease(t *testing.T) {
	h := newTestSession(t)
	p := setup(t, h, Config{})

	p.send(protocol.Message{Type: protocol.TypeAttach, ID: testSessionID})
	p.recv(protocol.TypeAttached, 5*time.Second, nil)
	p.send(protocol.Message{Type: protocol.TypeResize, Cols: 48, Rows: 20})
	p.send(protocol.Message{Type: protocol.TypeTakeover})
	p.recv(protocol.TypeLease, 5*time.Second, nil)

	if who, _ := h.Controller(testSessionID); who != hub.SourceWeb {
		t.Fatalf("接管后控制方 = %q, want web", who)
	}

	p.send(protocol.Message{Type: protocol.TypeBrowserOffline})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if who, _ := h.Controller(testSessionID); who == hub.SourceDesktop {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	who, _ := h.Controller(testSessionID)
	t.Fatalf("browser_offline 之后控制方仍是 %q，want desktop", who)
}

// 开了「连接后自动接管」时，attach 之后第一次报尺寸就接管。
func TestAutoTakeoverOnFirstResize(t *testing.T) {
	h := newTestSession(t)
	p := setup(t, h, Config{AutoTakeover: true})

	p.send(protocol.Message{Type: protocol.TypeAttach, ID: testSessionID})
	p.recv(protocol.TypeAttached, 5*time.Second, nil)

	p.send(protocol.Message{Type: protocol.TypeResize, Cols: 48, Rows: 20})

	lease := p.recv(protocol.TypeLease, 5*time.Second, nil)
	if lease.Controller != hub.SourceWeb {
		t.Errorf("自动接管后控制方 = %q, want web", lease.Controller)
	}
}

// 自动接管只在 attach 后第一次报尺寸时生效，之后不再抢——
// 否则桌面点「收回控制」后，手机一改字号就会把控制权抢回去。
func TestAutoTakeoverDoesNotStealBack(t *testing.T) {
	h := newTestSession(t)
	p := setup(t, h, Config{AutoTakeover: true})

	p.send(protocol.Message{Type: protocol.TypeAttach, ID: testSessionID})
	p.recv(protocol.TypeAttached, 5*time.Second, nil)
	p.send(protocol.Message{Type: protocol.TypeResize, Cols: 48, Rows: 20})
	p.recv(protocol.TypeLease, 5*time.Second, nil)

	// 桌面收回控制
	if err := h.SetController(testSessionID, hub.SourceDesktop, 80, 25); err != nil {
		t.Fatal(err)
	}
	p.recv(protocol.TypeLease, 5*time.Second, nil)

	// 手机再报一次尺寸（比如用户调了字号），不该把控制权抢回去
	p.send(protocol.Message{Type: protocol.TypeResize, Cols: 40, Rows: 18})
	time.Sleep(500 * time.Millisecond)

	if who, _ := h.Controller(testSessionID); who != hub.SourceDesktop {
		t.Errorf("控制方 = %q, want desktop —— 自动接管不该反复抢", who)
	}
}

// 解绑之后不该再收到输出。
func TestDetachStopsOutput(t *testing.T) {
	h := newTestSession(t)
	p := setup(t, h, Config{})

	p.send(protocol.Message{Type: protocol.TypeAttach, ID: testSessionID})
	p.recv(protocol.TypeAttached, 5*time.Second, nil)
	p.send(protocol.Message{Type: protocol.TypeDetach})

	if err := h.SetController(testSessionID, hub.SourceWeb, 48, 20); err != nil {
		t.Fatal(err)
	}
	_, _ = h.WriteFrom(testSessionID, hub.SourceWeb, []byte("echo AFTER_DETACH\r"))

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	for {
		typ, data, err := p.ws.Read(ctx)
		if err != nil {
			return // 超时了，说明确实没有输出，符合预期
		}
		if typ == websocket.MessageBinary && strings.Contains(string(data), "AFTER_DETACH") {
			t.Fatal("解绑之后不该再收到该会话的输出")
		}
	}
}

// nextControl 读下一条控制消息，不挑类型。
// recv 会把不匹配的消息跳过，所以断言「不该回的那条没回」得用它。
func (p *peer) nextControl(timeout time.Duration) (protocol.Message, bool) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		typ, data, err := p.ws.Read(ctx)
		if err != nil {
			return protocol.Message{}, false
		}
		if typ == websocket.MessageBinary {
			continue
		}
		var m protocol.Message
		if err := json.Unmarshal(data, &m); err != nil {
			p.t.Fatalf("控制帧解析失败: %v", err)
		}
		return m, true
	}
}

// assertNoErrorUntil 一直读到 barrier 为止，中间夹着 error 就算失败。
//
// ⚠️ 别用「等一会儿看有没有 error」的写法：coder/websocket 的 Read 一旦
// 超时就**把连接关掉**，读超时等于自杀，后面的断言全跑不了。
// 所以这里发一条必然有回应的消息当屏障——回错的话它一定排在屏障前面。
func (p *peer) assertNoErrorUntil(barrier protocol.MsgType, timeout time.Duration) {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m, ok := p.nextControl(time.Until(deadline))
		if !ok {
			p.t.Fatalf("没等到 %q 就断了", barrier)
		}
		if m.Type == protocol.TypeError {
			p.t.Fatalf("回错了: %s", m.Msg)
		}
		if m.Type == barrier {
			return
		}
	}
	p.t.Fatalf("没等到 %q", barrier)
}

// repaint 不该回错——旁观时也不该。
//
// 这个测试盯的是**接线**：TypeRepaint 没接进 switch 的话会落到 default 分支
// 回一句「unknown message type」，手机上一发就弹错误。而旁观时发是正常操作
// （画面可能是旧的，用户想看新的），后端按租约忽略就行，不是异常。
func TestRepaintDoesNotError(t *testing.T) {
	h := newTestSession(t)
	p := setup(t, h, Config{})

	p.send(protocol.Message{Type: protocol.TypeAttach, ID: testSessionID})
	p.recv(protocol.TypeAttached, 5*time.Second, nil)

	// 控制方还是桌面，这时候发 repaint
	p.send(protocol.Message{Type: protocol.TypeRepaint})
	p.send(protocol.Message{Type: protocol.TypeList})
	p.assertNoErrorUntil(protocol.TypeSessions, 5*time.Second)

	// 接管之后再发一次，这次是真的会去抖 PTY
	p.send(protocol.Message{Type: protocol.TypeTakeover})
	p.recv(protocol.TypeLease, 5*time.Second, nil)
	p.send(protocol.Message{Type: protocol.TypeRepaint})
	p.send(protocol.Message{Type: protocol.TypeList})
	p.assertNoErrorUntil(protocol.TypeSessions, 5*time.Second)
}
