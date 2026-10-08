package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/uncleyumo/go-terminal-hub/remote/protocol"
)

const testToken = "test-token-0123456789abcdef"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := &server{
		auth:       newAuthenticator(testToken, false, false),
		relay:      newRelay(),
		acceptOpts: &websocket.AcceptOptions{},
	}
	srv := httptest.NewServer(s.mux())
	t.Cleanup(srv.Close)
	return srv
}

func login(t *testing.T, srv *httptest.Server, token string) []*http.Cookie {
	t.Helper()
	body := strings.NewReader(`{"token":"` + token + `"}`)
	resp, err := http.Post(srv.URL+"/api/login", "application/json", body)
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}
	return resp.Cookies()
}

func dial(t *testing.T, srv *httptest.Server, path string, cookies []*http.Cookie, header http.Header) (*websocket.Conn, error) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	h := http.Header{}
	for _, c := range cookies {
		h.Add("Cookie", c.Name+"="+c.Value)
	}
	for k, vs := range header {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	ws, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{HTTPHeader: h})
	return ws, err
}

func mustDial(t *testing.T, srv *httptest.Server, path string, cookies []*http.Cookie, header http.Header) *websocket.Conn {
	t.Helper()
	ws, err := dial(t, srv, path, cookies, header)
	if err != nil {
		t.Fatalf("dial %s failed: %v", path, err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	return ws
}

func dialAgent(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	return mustDial(t, srv, "/ws/agent", nil, http.Header{"Authorization": []string{"Bearer " + testToken}})
}

func dialBrowser(t *testing.T, srv *httptest.Server, cookies []*http.Cookie) *websocket.Conn {
	t.Helper()
	return mustDial(t, srv, "/ws/browser", cookies, nil)
}

// readFrame 读一帧，超时就失败。
func readFrame(t *testing.T, ws *websocket.Conn, timeout time.Duration) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	typ, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	return typ, data
}

// readBinary 读到第一个二进制帧为止，跳过途中的控制消息。
func readBinary(t *testing.T, ws *websocket.Conn, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		typ, data := readFrame(t, ws, time.Until(deadline))
		if typ == websocket.MessageBinary {
			return data
		}
	}
	t.Fatal("did not receive a binary frame in time")
	return nil
}

// readControl 一直读到指定的控制消息类型，途中跳过别的。
func readControl(t *testing.T, ws *websocket.Conn, want protocol.MsgType, timeout time.Duration) protocol.Message {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		typ, data := readFrame(t, ws, time.Until(deadline))
		if typ != websocket.MessageText {
			continue
		}
		var m protocol.Message
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("bad control frame: %v", err)
		}
		if m.Type == want {
			return m
		}
	}
	t.Fatalf("did not receive %q within %s", want, timeout)
	return protocol.Message{}
}

// expectClose 断言连接以指定关闭码结束。
func expectClose(t *testing.T, ws *websocket.Conn, code int, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		_, _, err := ws.Read(ctx)
		if err != nil {
			if got := websocket.CloseStatus(err); int(got) != code {
				t.Fatalf("关闭码 = %d, want %d (err=%v)", got, code, err)
			}
			return
		}
	}
}

// --- 鉴权 -------------------------------------------------------------------

func TestAgentRejectsBadToken(t *testing.T) {
	srv := newTestServer(t)
	if _, err := dial(t, srv, "/ws/agent", nil, http.Header{"Authorization": []string{"Bearer wrong"}}); err == nil {
		t.Fatal("错误令牌应该被拒绝")
	}
	if _, err := dial(t, srv, "/ws/agent", nil, nil); err == nil {
		t.Fatal("没有令牌应该被拒绝")
	}
}

func TestBrowserRequiresSessionCookie(t *testing.T) {
	srv := newTestServer(t)
	if _, err := dial(t, srv, "/ws/browser", nil, nil); err == nil {
		t.Fatal("没有会话 cookie 应该被拒绝")
	}
	if _, err := dial(t, srv, "/ws/browser", []*http.Cookie{{Name: cookieSession, Value: "forged"}}, nil); err == nil {
		t.Fatal("伪造的会话 cookie 应该被拒绝")
	}
}

func TestLoginRateLimit(t *testing.T) {
	srv := newTestServer(t)
	var last int
	for i := 0; i < loginMaxAttempts+2; i++ {
		body := strings.NewReader(`{"token":"wrong"}`)
		resp, err := http.Post(srv.URL+"/api/login", "application/json", body)
		if err != nil {
			t.Fatal(err)
		}
		last = resp.StatusCode
		resp.Body.Close()
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("连续错误登录后状态码 = %d, want 429", last)
	}
}

// --- 转发 -------------------------------------------------------------------

func TestForwardsBothDirections(t *testing.T) {
	srv := newTestServer(t)
	agent := dialAgent(t, srv)
	browser := dialBrowser(t, srv, login(t, srv, testToken))

	// agent → browser
	if err := agent.Write(context.Background(), websocket.MessageBinary, []byte("from agent")); err != nil {
		t.Fatal(err)
	}
	if got := readBinary(t, browser, 2*time.Second); string(got) != "from agent" {
		t.Errorf("浏览器收到 %q, want %q", got, "from agent")
	}

	// browser → agent
	if err := browser.Write(context.Background(), websocket.MessageBinary, []byte("from browser")); err != nil {
		t.Fatal(err)
	}
	if got := readBinary(t, agent, 2*time.Second); string(got) != "from browser" {
		t.Errorf("agent 收到 %q, want %q", got, "from browser")
	}

	// 文本帧的控制消息也要原样透传
	if err := agent.Write(context.Background(), websocket.MessageText, []byte(`{"t":"sessions"}`)); err != nil {
		t.Fatal(err)
	}
	readControl(t, browser, protocol.TypeSessions, 2*time.Second)
}

// 大帧必须能过：coder/websocket 默认读上限 32768 字节，
// 终端一次爆发（dir /s）轻易超过，不放开会以 message too big 断连。
func TestAcceptsOversizedFrame(t *testing.T) {
	srv := newTestServer(t)
	agent := dialAgent(t, srv)
	browser := dialBrowser(t, srv, login(t, srv, testToken))

	// 测试客户端自己的读上限也要放开，否则量的是客户端而不是 relay
	browser.SetReadLimit(1 << 20)

	big := make([]byte, 200<<10)
	for i := range big {
		big[i] = byte('a' + i%26)
	}
	if err := agent.Write(context.Background(), websocket.MessageBinary, big); err != nil {
		t.Fatal(err)
	}
	data := readBinary(t, browser, 5*time.Second)
	if len(data) != len(big) {
		t.Fatalf("收到的长度 = %d, want %d", len(data), len(big))
	}
	if string(data) != string(big) {
		t.Error("大帧内容不一致")
	}
}

// --- 上下线通知 -------------------------------------------------------------

func TestBrowserToldWhenAgentDrops(t *testing.T) {
	srv := newTestServer(t)
	agent := dialAgent(t, srv)
	browser := dialBrowser(t, srv, login(t, srv, testToken))

	readControl(t, browser, protocol.TypeAgentOnline, 2*time.Second)

	_ = agent.CloseNow()
	readControl(t, browser, protocol.TypeAgentOffline, 2*time.Second)
}

// ⚠️ 这条不能少：不发的话，手机一关页面，桌面端会永远卡在旁观模式
// （租约还写着 web，而没人能把它改回来）。
func TestAgentToldWhenBrowserDrops(t *testing.T) {
	srv := newTestServer(t)
	agent := dialAgent(t, srv)
	browser := dialBrowser(t, srv, login(t, srv, testToken))

	readControl(t, agent, protocol.TypeBrowserOnline, 2*time.Second)

	_ = browser.CloseNow()
	readControl(t, agent, protocol.TypeBrowserOffline, 2*time.Second)
}

// --- 踢人 -------------------------------------------------------------------

// 新浏览器顶掉旧的：旧的要先收到 kicked，再以 4001 关闭。
func TestNewBrowserKicksOld(t *testing.T) {
	srv := newTestServer(t)
	agent := dialAgent(t, srv)

	first := dialBrowser(t, srv, login(t, srv, testToken))
	second := dialBrowser(t, srv, login(t, srv, testToken))

	readControl(t, first, protocol.TypeKicked, 2*time.Second)
	expectClose(t, first, protocol.CloseKicked, 2*time.Second)

	// 新的必须还活着：它写的帧要能被 agent 收到
	if err := second.Write(context.Background(), websocket.MessageBinary, []byte("still here")); err != nil {
		t.Fatalf("新浏览器不该被影响: %v", err)
	}
	if got := readBinary(t, agent, 2*time.Second); string(got) != "still here" {
		t.Errorf("agent 收到 %q, want %q", got, "still here")
	}
}

// 防踢人循环：被顶掉的设备 5 秒内又自动回来，直接拒绝。
//
// 拦的是「自动重连」——两台设备谁也不让谁，无限对踢。
// ⚠️ 这里必须用**两个不同的 clientId**：同一个 clientId 是「自己换了条连接」，
// 走 4005 静默顶掉，根本不进这道闸。
func TestKickLoopRejected(t *testing.T) {
	srv := newTestServer(t)
	agent := dialAgent(t, srv)

	cookiesA := login(t, srv, testToken)
	cookiesB := login(t, srv, testToken) // 另一台设备

	a := dialBrowser(t, srv, cookiesA)
	b := dialBrowser(t, srv, cookiesB) // B 顶掉 A

	readControl(t, a, protocol.TypeKicked, 2*time.Second)
	expectClose(t, a, protocol.CloseKicked, 2*time.Second)

	// A 立刻自动回来 —— 应当被拒
	a2, err := dial(t, srv, "/ws/browser", cookiesA, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer a2.CloseNow()
	readControl(t, a2, protocol.TypeKicked, 2*time.Second)
	expectClose(t, a2, protocol.CloseKicked, 2*time.Second)

	// B 必须还是当前那个：它写的帧要能被 agent 收到
	if err := b.Write(context.Background(), websocket.MessageBinary, []byte("b still current")); err != nil {
		t.Fatal(err)
	}
	if got := readBinary(t, agent, 2*time.Second); string(got) != "b still current" {
		t.Errorf("agent 收到 %q, want %q", got, "b still current")
	}
}

// 人手动点的重连（?force=1）必须能拿到位置。
//
// 防踢循环那道闸拦的是「自动重连」。人点「在这里继续」的意思是
// 「我就要这个位置」，拦下来只会让人以为坏了。
func TestForceReconnectBypassesAntiLoop(t *testing.T) {
	srv := newTestServer(t)
	agent := dialAgent(t, srv)

	cookiesA := login(t, srv, testToken)
	cookiesB := login(t, srv, testToken)

	a := dialBrowser(t, srv, cookiesA)
	b := dialBrowser(t, srv, cookiesB) // B 顶掉 A
	readControl(t, a, protocol.TypeKicked, 2*time.Second)
	expectClose(t, a, protocol.CloseKicked, 2*time.Second)

	// A 自动回来 —— 被拒
	a2, err := dial(t, srv, "/ws/browser", cookiesA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a2.CloseNow()
	expectClose(t, a2, protocol.CloseKicked, 2*time.Second)

	// A 带 force=1 回来 —— 放行，并且把 B 顶掉
	a3 := mustDial(t, srv, "/ws/browser?force=1", cookiesA, nil)
	readControl(t, b, protocol.TypeKicked, 2*time.Second)

	// a3 现在是当前那个：它写的帧要能被 agent 收到
	if err := a3.Write(context.Background(), websocket.MessageBinary, []byte("a3 is current")); err != nil {
		t.Fatal(err)
	}
	if got := readBinary(t, agent, 2*time.Second); string(got) != "a3 is current" {
		t.Errorf("agent 收到 %q, want %q", got, "a3 is current")
	}
}

// 同一个浏览器换了一条连接（刷新页面、网络抖动、旧 socket 还没回收），
// 要用 4005 静默顶掉，**不能**用 4001。
//
// 4001 的意思是「别的设备抢走了」，客户端收到会停下来等用户手动点；
// 而刷新页面这种事用户没做错什么。更要紧的是：如果两边都重连，
// 就会来回顶成死循环——日志里连接成对出现就是这么来的。
func TestSameClientSupersededNotKicked(t *testing.T) {
	srv := newTestServer(t)
	dialAgent(t, srv)

	cookies := login(t, srv, testToken)
	first := dialBrowser(t, srv, cookies)
	second := dialBrowser(t, srv, cookies) // 同 clientId

	// 旧的那条应当收到 4005，而不是 4001，也不该先收到一条 kicked 通知
	expectClose(t, first, protocol.CloseSuperseded, 2*time.Second)

	// 新的那条活着
	if err := second.Write(context.Background(), websocket.MessageBinary, []byte("alive")); err != nil {
		t.Fatalf("新连接不该被影响: %v", err)
	}
}

// 但换成另一台设备（clientId 不同）时，还是要走 4001 + kicked 通知：
// 用户得知道「被别处顶掉了」，而不是莫名其妙断了。
func TestDifferentClientStillKicked(t *testing.T) {
	srv := newTestServer(t)
	dialAgent(t, srv)

	first := dialBrowser(t, srv, login(t, srv, testToken))
	second := dialBrowser(t, srv, login(t, srv, testToken)) // 另一个 clientId

	readControl(t, first, protocol.TypeKicked, 2*time.Second)
	expectClose(t, first, protocol.CloseKicked, 2*time.Second)

	if err := second.Write(context.Background(), websocket.MessageBinary, []byte("alive")); err != nil {
		t.Fatalf("新连接不该被影响: %v", err)
	}
}

// 没带 cookie 的浏览器（clientID 为空）不能被当成「某个被踢过的身份」而误伤。
func TestEmptyClientIDNotBlockedByAntiLoop(t *testing.T) {
	srv := newTestServer(t)
	dialAgent(t, srv)

	// 造一个没有 hub_client cookie 的会话：手动塞一个有效 sid
	authSrv := newTestServer(t)
	_ = authSrv
	// 直接走 /api/login 拿到的 cookie 一定带 hub_client，
	// 所以这里改从 relay 内部状态构造：先踢一个空 clientID 的连接
	r := newRelay()
	old := &conn{label: "browser", clientID: ""}
	r.browser = old
	r.markKicked("")

	// 空 clientID 再连上来，不该被判成踢人循环
	if _, rejected := r.setBrowser(&conn{label: "browser", clientID: ""}, "", false); rejected {
		t.Error("空 clientID 不该被防踢循环拦下")
	}
}

// --- 静态与健康检查 ---------------------------------------------------------

func TestHealthz(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestMeRequiresSession(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/me")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("未登录时 /api/me = %d, want 401", resp.StatusCode)
	}
}

// --- 会话替换 agent ---------------------------------------------------------

// hub 重启后会带着同样的令牌重新拨进来，把僵死的旧连接顶掉。
func TestNewAgentReplacesOld(t *testing.T) {
	srv := newTestServer(t)
	first := dialAgent(t, srv)
	second := dialAgent(t, srv)

	expectClose(t, first, protocol.CloseAgentReplaced, 2*time.Second)

	if err := second.Write(context.Background(), websocket.MessageBinary, []byte("x")); err != nil {
		t.Fatalf("新 agent 不该被影响: %v", err)
	}
}
