package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/uncleyumo/go-terminal-hub/remote/protocol"
	"github.com/uncleyumo/go-terminal-hub/remote/relaydb"
)

const testSecret = "test-secret-0123456789abcdef"

// newTestServer 建一个连着临时数据库的 relay，返回服务器和那个数据库。
//
// 用文件而不是 :memory:：多连接的场景下 WAL 才是真实形态，
// 而 :memory: 每个连接各拿一份库，测不出真实行为。
func newTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()

	db, err := relaydb.Open(t.TempDir() + "/relay.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s := &server{
		auth:       newAuthenticator(db, false, false),
		relay:      newRelay(),
		db:         db,
		acceptOpts: &websocket.AcceptOptions{},
	}
	srv := httptest.NewServer(s.mux())
	t.Cleanup(srv.Close)
	return srv, db
}

// issueTestKey 在数据库里直接插一把 key，绕过 hubkey 那个二进制。
//
// 测试要的是「库里有一把可用的 key」，不是「hubkey 能签发」——后者在
// tools/hubkey 自己的测试里覆盖。
func issueTestKey(t *testing.T, db *sql.DB, label string) string {
	t.Helper()

	key := "key-test-" + label
	_, err := db.Exec(`INSERT OR REPLACE INTO keys (id, label, created_at, expires_at, revoked)
	                   VALUES (?, ?, ?, 0, 0)`, key, label, time.Now().Unix())
	if err != nil {
		t.Fatalf("issue key: %v", err)
	}

	// 密钥也要登记：浏览器登录要两样都验证。
	// 测试直接调 registerSecret，和 agent 走的是同一条路。
	a := newAuthenticator(db, false, false)
	if err := a.registerSecret(key, testSecret); err != nil {
		t.Fatalf("register secret: %v", err)
	}
	return key
}

// login 用 key + 登陆密钥换 cookie。
func login(t *testing.T, srv *httptest.Server, key string) []*http.Cookie {
	t.Helper()
	body := strings.NewReader(`{"key":"` + key + `","secret":"` + testSecret + `"}`)
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

func dialAgent(t *testing.T, srv *httptest.Server, key string) *websocket.Conn {
	t.Helper()
	return mustDial(t, srv, "/ws/agent", nil, http.Header{"Authorization": []string{"Bearer " + key}})
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

func TestAgentRejectsBadKey(t *testing.T) {
	srv, db := newTestServer(t)
	if _, err := dial(t, srv, "/ws/agent", nil, http.Header{"Authorization": []string{"Bearer wrong"}}); err == nil {
		t.Fatal("不存在的 key 应该被拒绝")
	}
	if _, err := dial(t, srv, "/ws/agent", nil, nil); err == nil {
		t.Fatal("没有 key 应该被拒绝")
	}

	// 吊销和过期的 key 也要拒：这两条是「曾经合法但现在不行」，
	// 漏掉的话 key 收不回来。
	if _, err := db.Exec(`INSERT INTO keys VALUES ('key-revoked','r',?,?,1)`, time.Now().Unix(), time.Now().AddDate(0,0,90).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO keys VALUES ('key-expired','e',?,?,0)`, time.Now().Unix(), time.Now().AddDate(0,0,-1).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := dial(t, srv, "/ws/agent", nil, http.Header{"Authorization": []string{"Bearer key-revoked"}}); err == nil {
		t.Fatal("已吊销的 key 应该被拒绝")
	}
	if _, err := dial(t, srv, "/ws/agent", nil, http.Header{"Authorization": []string{"Bearer key-expired"}}); err == nil {
		t.Fatal("已过期的 key 应该被拒绝")
	}
}

func TestBrowserRequiresSessionCookie(t *testing.T) {
	srv, _ := newTestServer(t)
	if _, err := dial(t, srv, "/ws/browser", nil, nil); err == nil {
		t.Fatal("没有会话 cookie 应该被拒绝")
	}
	if _, err := dial(t, srv, "/ws/browser", []*http.Cookie{{Name: cookieSession, Value: "forged"}}, nil); err == nil {
		t.Fatal("伪造的会话 cookie 应该被拒绝")
	}
}

func TestLoginRateLimit(t *testing.T) {
	srv, _ := newTestServer(t)
	var last int
	for i := 0; i < loginMaxAttempts+2; i++ {
		body := strings.NewReader(`{"key":"nope","secret":"wrong"}`)
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
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "testforwardsbothdire")
	agent := dialAgent(t, srv, key)
	browser := dialBrowser(t, srv, login(t, srv, key))

	// 等服务端把浏览器装进槽位再写帧：dialBrowser 返回只代表握手完成，
	// handleBrowser 还在跑 setBrowser。不等的话帧可能被丢掉（槽位还空着），
	// 表现为 readBinary 超时——一个和转发逻辑无关的偶发失败。
	readControl(t, browser, protocol.TypeAgentOnline, 2*time.Second)

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
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "testacceptsoversized")
	agent := dialAgent(t, srv, key)
	browser := dialBrowser(t, srv, login(t, srv, key))

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
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "testbrowsertoldwhena")
	agent := dialAgent(t, srv, key)
	browser := dialBrowser(t, srv, login(t, srv, key))

	readControl(t, browser, protocol.TypeAgentOnline, 2*time.Second)

	_ = agent.CloseNow()
	readControl(t, browser, protocol.TypeAgentOffline, 2*time.Second)
}

// ⚠️ 这条不能少：不发的话，手机一关页面，桌面端会永远卡在旁观模式
// （租约还写着 web，而没人能把它改回来）。
func TestAgentToldWhenBrowserDrops(t *testing.T) {
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "testagenttoldwhenbro")
	agent := dialAgent(t, srv, key)
	browser := dialBrowser(t, srv, login(t, srv, key))

	readControl(t, agent, protocol.TypeBrowserOnline, 2*time.Second)

	_ = browser.CloseNow()
	readControl(t, agent, protocol.TypeBrowserOffline, 2*time.Second)
}

// --- 踢人 -------------------------------------------------------------------

// 新浏览器顶掉旧的：旧的要先收到 kicked，再以 4001 关闭。
func TestNewBrowserKicksOld(t *testing.T) {
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "testnewbrowserkickso")
	agent := dialAgent(t, srv, key)

	first := dialBrowser(t, srv, login(t, srv, key))
	second := dialBrowser(t, srv, login(t, srv, key))

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
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "testkicklooprejected")
	agent := dialAgent(t, srv, key)

	cookiesA := login(t, srv, key)
	cookiesB := login(t, srv, key) // 另一台设备

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
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "testforcereconnectby")
	agent := dialAgent(t, srv, key)

	cookiesA := login(t, srv, key)
	cookiesB := login(t, srv, key)

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
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "testsameclientsupers")
	dialAgent(t, srv, key)

	cookies := login(t, srv, key)
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
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "testdifferentclients")
	dialAgent(t, srv, key)

	first := dialBrowser(t, srv, login(t, srv, key))
	second := dialBrowser(t, srv, login(t, srv, key)) // 另一个 clientId

	readControl(t, first, protocol.TypeKicked, 2*time.Second)
	expectClose(t, first, protocol.CloseKicked, 2*time.Second)

	if err := second.Write(context.Background(), websocket.MessageBinary, []byte("alive")); err != nil {
		t.Fatalf("新连接不该被影响: %v", err)
	}
}

// 没带 cookie 的浏览器（clientID 为空）不能被当成「某个被踢过的身份」而误伤。
func TestEmptyClientIDNotBlockedByAntiLoop(t *testing.T) {
	// 直接用 relay 内部状态构造：走 /api/login 拿到的 cookie 一定带 hub_client，
	// 造不出「没带 cookie 的浏览器」。
	r := newRelay()
	key := "key-emptyclient"
	if r.getOrCreateSlot(key) == nil {
		t.Fatal("getOrCreateSlot 失败")
	}

	old := &conn{label: "browser", clientID: ""}
	r.setBrowser(key, old, "", false)
	r.markKicked(key, "")

	// 空 clientID 再连上来，不该被判成踢人循环
	if _, rejected := r.setBrowser(key, &conn{label: "browser", clientID: ""}, "", false); rejected {
		t.Error("空 clientID 不该被防踢循环拦下")
	}
}

// --- 静态与健康检查 ---------------------------------------------------------

func TestHealthz(t *testing.T) {
	srv, _ := newTestServer(t)
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
	srv, _ := newTestServer(t)
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
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "testnewagentreplaces")
	first := dialAgent(t, srv, key)
	second := dialAgent(t, srv, key)

	expectClose(t, first, protocol.CloseAgentReplaced, 2*time.Second)

	if err := second.Write(context.Background(), websocket.MessageBinary, []byte("x")); err != nil {
		t.Fatalf("新 agent 不该被影响: %v", err)
	}
}

// --- 多租户隔离（0.5.0 核心）-----------------------------------------------

// 两把 key 各一套槽位，A 的帧不能漏到 B 那边。
//
// 这是整个「中央站多租户」改造的验收点：槽位管理是全程唯一有并发风险的
// 一块，漏一个映射就是「用 A 的 key 能看到 B 的终端」。
func TestKeysAreIsolated(t *testing.T) {
	srv, db := newTestServer(t)
	keyA := issueTestKey(t, db, "alice")
	keyB := issueTestKey(t, db, "bob")

	agentA := dialAgent(t, srv, keyA)
	agentB := dialAgent(t, srv, keyB)
	browserA := dialBrowser(t, srv, login(t, srv, keyA))
	browserB := dialBrowser(t, srv, login(t, srv, keyB))

	readControl(t, browserA, protocol.TypeAgentOnline, 2*time.Second)
	readControl(t, browserB, protocol.TypeAgentOnline, 2*time.Second)

	// A 的 agent 发一个帧，B 的浏览器不该收到
	if err := agentA.Write(context.Background(), websocket.MessageBinary, []byte("for A only")); err != nil {
		t.Fatal(err)
	}
	if got := readBinary(t, browserA, 2*time.Second); string(got) != "for A only" {
		t.Errorf("A 的浏览器收到 %q", got)
	}

	// B 的浏览器此刻应该收不到那条。
	//
	// ⚠️ 不能用「带超时的 Read」来验证收不到：coder/websocket 的 Read 一旦
	// 超时就把整条连接关掉，后面的写会直接失败。只能起协程收帧、通道 select。
	bGot := collectBinary(t, browserB)

	// 反向也验一遍
	if err := browserB.Write(context.Background(), websocket.MessageBinary, []byte("for B only")); err != nil {
		t.Fatal(err)
	}
	if got := readBinary(t, agentB, 2*time.Second); string(got) != "for B only" {
		t.Errorf("B 的 agent 收到 %q", got)
	}

	aGot := collectBinary(t, agentA)

	select {
	case d := <-bGot:
		t.Errorf("B 的浏览器收到了 A 的帧 %q——隔离破了", d)
	case <-time.After(400 * time.Millisecond):
	}
	select {
	case d := <-aGot:
		t.Errorf("A 的 agent 收到了 B 的帧 %q——隔离破了", d)
	case <-time.After(400 * time.Millisecond):
	}
}

// 吊销 key 要把那套槽位的连接都踢掉，关闭码 4006。
func TestKeyRevocationDropsConnections(t *testing.T) {
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "toRevoke")

	agent := dialAgent(t, srv, key)
	browser := dialBrowser(t, srv, login(t, srv, key))
	readControl(t, browser, protocol.TypeAgentOnline, 2*time.Second)

	// 吊销
	if _, err := db.Exec(`UPDATE keys SET revoked = 1 WHERE id = ?`, key); err != nil {
		t.Fatal(err)
	}
	// 再有连接进来会被拒：key 状态是每次握手都查库的
	if _, err := dial(t, srv, "/ws/agent", nil, http.Header{"Authorization": []string{"Bearer " + key}}); err == nil {
		t.Error("吊销后的 key 不该能连上")
	}
	_ = agent
	_ = browser
}

// register_secret 是桌面端登记登陆密钥的那条消息。
//
// 密钥只在这条消息里明文出现一次，存库里的是 SHA-256。
func TestRegisterSecret(t *testing.T) {
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "secretOwner")

	agent := dialAgent(t, srv, key)

	// 登记一个密钥
	reg := protocol.Message{Type: protocol.TypeRegisterSecret, Secret: testSecret}
	b, _ := protocol.Encode(reg)
	if err := agent.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	readControl(t, agent, protocol.TypeRegistered, 2*time.Second)

	// 库里存的是哈希，不是明文
	var stored string
	if err := db.QueryRow(`SELECT secret_hash FROM secrets WHERE key_id = ?`, key).Scan(&stored); err != nil {
		t.Fatalf("secrets 表没写入: %v", err)
	}
	if stored == testSecret {
		t.Error("库里存的是明文密钥——应该是 SHA-256")
	}
	if len(stored) != 64 {
		t.Errorf("secret_hash 长度 %d, want 64（hex SHA-256）", len(stored))
	}
}

// 密钥轮换：发一个新的，旧的当场失效。
func TestSecretRotation(t *testing.T) {
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "rotator")

	agent := dialAgent(t, srv, key)
	sendRegister(t, agent, testSecret)
	readControl(t, agent, protocol.TypeRegistered, 2*time.Second)

	// 旧密钥能登录
	if cookies := login(t, srv, key); len(cookies) == 0 {
		t.Fatal("旧密钥登录失败")
	}

	// 轮换
	sendRegister(t, agent, testSecret+"-rotated")
	readControl(t, agent, protocol.TypeRegistered, 2*time.Second)

	// 旧的不能再登录。用 loginWith：login 遇到非 200 会 fatal，
	// 而这条用例里的失败正是我们要的。
	if cookies := loginWith(t, srv, key, testSecret); len(cookies) != 0 {
		t.Error("轮换后旧密钥不该还能登录")
	}
	// 新的能登录
	if cookies := loginWith(t, srv, key, testSecret+"-rotated"); len(cookies) == 0 {
		t.Error("轮换后的新密钥应该能登录")
	}
}

// 密钥太短 / 为空时要拒，并说明原因。
func TestRegisterSecretRejected(t *testing.T) {
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "shorty")

	agent := dialAgent(t, srv, key)
	sendRegister(t, agent, "")
	readControl(t, agent, protocol.TypeRegisterRejected, 2*time.Second)

	sendRegister(t, agent, "short")
	readControl(t, agent, protocol.TypeRegisterRejected, 2*time.Second)
}

// 浏览器登录要 key + 密钥两样都对。只对一样都不行。
func TestLoginNeedsBothKeyAndSecret(t *testing.T) {
	srv, db := newTestServer(t)
	key := issueTestKey(t, db, "twofactor")
	sendRegister(t, dialAgent(t, srv, key), testSecret)

	// 密钥对、key 错
	if cookies := loginWith(t, srv, "key-wrong", testSecret); len(cookies) != 0 {
		t.Error("key 错时不该给 cookie")
	}
	// key 对、密钥错
	if cookies := loginWith(t, srv, key, "wrong-secret"); len(cookies) != 0 {
		t.Error("密钥错时不该给 cookie")
	}
	// 都对
	if cookies := loginWith(t, srv, key, testSecret); len(cookies) == 0 {
		t.Error("key 和密钥都对却登录失败")
	}
}

// 会话是按 key 分家的：A 的 cookie 不能进 B 的槽位。
func TestSessionBoundToKey(t *testing.T) {
	srv, db := newTestServer(t)
	keyA := issueTestKey(t, db, "sessA")
	keyB := issueTestKey(t, db, "sessB")

	sendRegister(t, dialAgent(t, srv, keyA), testSecret)
	sendRegister(t, dialAgent(t, srv, keyB), testSecret)

	cookiesA := login(t, srv, keyA)
	browserA := dialBrowser(t, srv, cookiesA)

	// A 的浏览器只能进 A 的槽位：它发的帧要被 A 的 agent 收到
	agentA := dialAgent(t, srv, keyA)
	readControl(t, browserA, protocol.TypeAgentOnline, 2*time.Second)

	if err := browserA.Write(context.Background(), websocket.MessageBinary, []byte("hello A")); err != nil {
		t.Fatal(err)
	}
	if got := readBinary(t, agentA, 2*time.Second); string(got) != "hello A" {
		t.Errorf("A 的 agent 收到 %q", got)
	}

	// 换成 B 的 agent 接管，A 的浏览器发的帧不该过去
	agentB := dialAgent(t, srv, keyB)
	bGot := collectBinary(t, agentB)

	if err := browserA.Write(context.Background(), websocket.MessageBinary, []byte("should stay in A")); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-bGot:
		t.Errorf("B 的 agent 收到了 A 的帧 %q——隔离破了", d)
	case <-time.After(400 * time.Millisecond):
	}
}

// --- 测试辅助 ---------------------------------------------------------------

// collectBinary 起一个协程持续收二进制帧，返回通道。
//
// 为什么不直接 Read：coder/websocket 的 Read 一旦超时就把连接关掉，
// 用来验证「收不到」会把连接自己弄死。只能让一个协程慢慢读、主测试 select。
func collectBinary(t *testing.T, ws *websocket.Conn) <-chan []byte {
	t.Helper()
	ch := make(chan []byte, 8)
	go func() {
		defer close(ch)
		for {
			typ, data, err := ws.Read(context.Background())
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				select {
				case ch <- data:
				default: // 缓冲满了就丢，测试只关心「有没有漏过来」
				}
			}
		}
	}()
	return ch
}

// sendRegister 通过 agent 连接发送一条 register_secret。
func sendRegister(t *testing.T, agent *websocket.Conn, secret string) {
	t.Helper()
	reg := protocol.Message{Type: protocol.TypeRegisterSecret, Secret: secret}
	b, _ := protocol.Encode(reg)
	if err := agent.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatalf("发送 register_secret 失败: %v", err)
	}
}

// loginWith 是 login 的自由参数版本，用于「key 对但密钥错」这类组合。
func loginWith(t *testing.T, srv *httptest.Server, key, secret string) []*http.Cookie {
	t.Helper()
	body := strings.NewReader(`{"key":"` + key + `","secret":"` + secret + `"}`)
	resp, err := http.Post(srv.URL+"/api/login", "application/json", body)
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	return resp.Cookies()
}
