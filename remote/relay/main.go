// relay 是部署在云服务器上的中转进程：既托管网页，也在本地 hub 和浏览器之间搬帧。
//
// 它是「哑管道」——不理解会话，不缓存状态。会话的唯一真相源在本地 hub 那台机器上。
//
// 编译成单文件 Linux 二进制：
//
//	GOOS=linux GOARCH=amd64 go build -o hub-relay ./remote/relay
//
// 注意：必须先构建前端（`task relay:build` 会做），否则 remote/ui/dist 是空的。
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/uncleyumo/go-terminal-hub/internal/buildinfo"
	"github.com/uncleyumo/go-terminal-hub/remote/protocol"
	"github.com/uncleyumo/go-terminal-hub/remote/relaydb"
	"github.com/uncleyumo/go-terminal-hub/remote/ui"
)

type server struct {
	auth       *authenticator
	relay      *relay
	db         *sql.DB
	acceptOpts *websocket.AcceptOptions
}

func main() {
	var (
		addr      = flag.String("addr", "127.0.0.1:8080", "监听地址。默认只听本机；要对公网开放必须显式写 0.0.0.0:8080")
		dbPath    = flag.String("db", relaydb.DefaultPath, "SQLite 数据库文件路径。必须和 hubkey 用的是同一个文件")
		cookieTLS = flag.Bool("cookie-secure", true, "给 cookie 加 Secure 标记。本地用 http 调试时设 false")
		trustFwd  = flag.Bool("trust-proxy", false, "信任 X-Forwarded-For。放在反代后面时打开")
		origins   = flag.String("origin", "", "允许的 WebSocket Origin，逗号分隔。反代重写 Host 后需要填公网域名")
		anyOrigin = flag.Bool("allow-all-origins", false, "不校验 Origin。仅用于本地调试")
		level     = flag.String("log-level", "info", "日志级别：debug/info/warn/error")
	)
	flag.Parse()

	initLogger(*level)

	db, err := relaydb.Open(*dbPath)
	if err != nil {
		// 起不来就说清楚是哪个文件：最常见的失败就是路径写错或目录没权限。
		slog.Error("open relay database failed", "db", *dbPath, "err", err)
		os.Exit(1)
	}
	defer db.Close()

	s := &server{
		auth:  newAuthenticator(db, *cookieTLS, *trustFwd),
		relay: newRelay(),
		db:    db,
		acceptOpts: &websocket.AcceptOptions{
			InsecureSkipVerify: *anyOrigin,
		},
	}
	if *origins != "" {
		for _, o := range strings.Split(*origins, ",") {
			if o = strings.TrimSpace(o); o != "" {
				s.acceptOpts.OriginPatterns = append(s.acceptOpts.OriginPatterns, o)
			}
		}
	}
	if *anyOrigin {
		slog.Warn("origin checks are disabled; do not use this on a public address")
	}

	httpServer := &http.Server{
		Handler:           s.mux(),
		Addr:              *addr,
		ReadHeaderTimeout: 10 * time.Second,
		// 不设 WriteTimeout：WebSocket 是长连接，写了会被中途掐断。
		// 空闲连接由 conn 自己的 ping/pong 兜住。
	}

	// 启动就报版本、构建时间、数据库路径。这台机器上可能同时躺着好几个 hub-relay，
	// 排查时第一件要确认的事就是「现在跑的是哪一个、连的是哪个库」——
	// 「签证成功了但连不上」十有八九是这两个对不上。
	info := buildinfo.Info()
	slog.Info("relay starting",
		"version", orUnknown(info.Version),
		"built", orUnknown(info.BuildTime),
		"addr", *addr,
		"db", *dbPath,
	)
	slog.Info("relay listening", "addr", *addr)
	if err := httpServer.ListenAndServe(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// mux 把路由单独拆出来，测试里可以直接挂在 httptest.Server 上。
func (s *server) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/login", s.auth.handleLogin)
	mux.HandleFunc("/api/logout", s.auth.handleLogout)
	mux.HandleFunc("/api/me", s.auth.handleMe)
	mux.HandleFunc("/ws/agent", s.handleAgent)
	mux.HandleFunc("/ws/browser", s.handleBrowser)
	mux.HandleFunc("/", s.handleStatic)
	return mux
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Info()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": orUnknown(info.Version),
		"built":   orUnknown(info.BuildTime),
	})
}

// orUnknown 把没注入的版本号显示成 unknown，而不是一个空串。
// 空串在日志里看着像「这一项忘了打印」，unknown 才说明是构建时没给。
func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// describeClient 给连接打一个能认出来的标签。
//
// 排查「谁在反复连」时，光看 IP 没用——反代后面全是 127.0.0.1。
// User-Agent 能一眼区分 iPhone Safari / 微信内置浏览器 / 桌面 Chrome，
// 而「微信内置浏览器」恰恰是最容易反复丢 cookie 的那一类。
func describeClient(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	if ua == "" {
		return "(no user-agent)"
	}
	if len(ua) > 90 {
		ua = ua[:90] + "…"
	}
	return ua
}

// handleAgent 接受本地 hub 拨进来的连接。
//
// agent 是 Go 程序，能设 Authorization 头，所以走 Bearer 而不是 cookie。
// 那个头里带的是**使用 key**，不是登陆密钥——密钥只在浏览器登录时出现。
func (s *server) handleAgent(w http.ResponseWriter, r *http.Request) {
	key := bearerToken(r)
	switch st := s.auth.checkKey(key); st {
	case KeyOK:
	case KeyNotFound:
		slog.Warn("agent rejected: unknown key", "ip", s.auth.clientIP(r))
		http.Error(w, "unknown key", http.StatusUnauthorized)
		return
	case KeyExpired:
		slog.Warn("agent rejected: expired key", "ip", s.auth.clientIP(r))
		http.Error(w, "key expired", http.StatusUnauthorized)
		return
	case KeyRevoked:
		slog.Warn("agent rejected: revoked key", "ip", s.auth.clientIP(r))
		http.Error(w, "key revoked", http.StatusUnauthorized)
		return
	}

	ws, err := websocket.Accept(w, r, s.acceptOpts)
	if err != nil {
		slog.Debug("agent upgrade failed", "err", err)
		return
	}

	// 槽位必须先建再装连接：后面每一步都要靠它找到对的那一套。
	// 超过并发上限时这里会是 nil，拒掉而不是抢别人的位置。
	sl := s.relay.getOrCreateSlot(key)
	if sl == nil {
		slog.Error("slot limit reached, rejecting agent", "key", key)
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		return
	}

	// 用 Background 而不是 r.Context()：handler 返回时 r.Context() 会被取消，
	// 而那时写协程可能还在发队列里剩下的帧（关闭帧、kicked 通知）。
	// 连接的生命周期由写协程自己管，不由 HTTP 请求管。
	c := newConn(context.Background(), ws, "agent")
	c.key = key
	old := s.relay.setAgent(key, c)
	if old != nil {
		// hub 重启后会带着同样的 key 重新拨进来，把僵死的旧连接顶掉。
		// agent 收到 4002 应当退避一段时间再重连，免得两个桌面实例互相顶。
		old.close(protocol.CloseAgentReplaced, "replaced by a newer agent")
	}
	slog.Info("agent connected",
		"key", key, "ip", s.auth.clientIP(r), "client", describeClient(r))

	s.relay.greetAgent(key, c)

	// 读循环里要拦一道 register_secret：那条是登记密钥的，不该透传给浏览器。
	c.readLoop(func(typ websocket.MessageType, data []byte) bool {
		if typ == websocket.MessageText {
			if m, err := protocol.Decode(data); err == nil && m.Type == protocol.TypeRegisterSecret {
				s.handleRegisterSecret(key, c, m)
				return true
			}
		}
		return s.relay.onAgentFrame(key, typ, data)
	})
	s.relay.clearAgent(key, c)
	slog.Info("agent disconnected", "key", key)
}

// handleRegisterSecret 处理桌面端登记登陆密钥。
//
// 密钥只在这一条消息里明文出现。存库之前先哈希，日志里一个字都不打。
func (s *server) handleRegisterSecret(key string, c *conn, m protocol.Message) {
	if err := s.auth.registerSecret(key, m.Secret); err != nil {
		slog.Warn("register_secret rejected", "key", key, "err", err)
		c.sendJSON(protocol.Message{
			Type: protocol.TypeRegisterRejected,
			Key:  key,
			Msg:  err.Error(),
		})
		return
	}
	slog.Info("secret registered", "key", key)
	c.sendJSON(protocol.Message{Type: protocol.TypeRegistered, Key: key})
}

// handleBrowser 接受手机/浏览器连进来的连接。
//
// 进来先问「这是谁」：cookie 里带着 key，所有后续操作都在那个 key 的槽位里做。
// 以前只有一套槽位，不需要问；现在不问就会串到别人的终端上。
func (s *server) handleBrowser(w http.ResponseWriter, r *http.Request) {
	key, ok := s.auth.authed(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ws, err := websocket.Accept(w, r, s.acceptOpts)
	if err != nil {
		slog.Debug("browser upgrade failed", "err", err)
		return
	}

	// 同 agent：不能用 r.Context()，否则「被顶掉」那条路径上 handler 一返回，
	// 还没发出去的关闭帧就被取消掉了。
	c := newConn(context.Background(), ws, "browser")
	c.key = key
	c.clientID = s.auth.clientID(r)

	// ?force=1 表示这是人手动点的重连，跳过防踢循环的闸。
	// 自动重连不带这个参数，所以两台设备还是踢不成死循环。
	force := r.URL.Query().Get("force") == "1"

	// 槽位不存在说明 agent 没连过（或已断开被清）。给一个空槽位，
	// 让浏览器至少能连上、看到「本地 hub 离线」——而不是一条莫名的失败。
	if s.relay.getSlot(key) == nil {
		if s.relay.getOrCreateSlot(key) == nil {
			slog.Error("slot limit reached, rejecting browser", "key", key)
			http.Error(w, "server busy", http.StatusServiceUnavailable)
			return
		}
	}

	old, rejected := s.relay.setBrowser(key, c, c.clientID, force)
	if rejected {
		// 这个 clientId 刚被踢过又立刻回来了，判定为踢人循环：
		// 关掉它，但不动当前那个浏览器 —— 否则两台手机会把对方无限踢下去。
		slog.Warn("rejecting browser: reconnect after kick", "key", key, "clientID", c.clientID)
		c.sendJSON(protocol.Message{Type: protocol.TypeKicked, Msg: "another device is already connected"})
		c.close(protocol.CloseKicked, "reconnect too soon after being kicked")
		return
	}

	if old != nil {
		// 同一个浏览器（hub_client cookie 一样）换了一条连接：页面重载、
		// 网络抖动、或者旧 socket 还没被回收新的就来了。
		//
		// 这跟「另一台设备抢走了」是两回事，得分开处理：发 4001 的话客户端会
		// 停下来等用户手动点，可用户根本没做错什么；更糟的是如果两边都重连，
		// 就会来回顶成死循环。
		sameClient := c.clientID != "" && old.clientID == c.clientID
		if sameClient {
			slog.Info("superseding stale connection from the same browser", "key", key, "clientID", c.clientID)
			old.close(protocol.CloseSuperseded, "superseded by a newer connection from the same browser")
		} else {
			slog.Info("browser replaced by another device", "key", key)
			old.sendJSON(protocol.Message{Type: protocol.TypeKicked, Msg: "another device took over"})
			old.close(protocol.CloseKicked, "replaced by a newer connection")
			s.relay.markKicked(key, old.clientID)
		}
	}
	slog.Info("browser connected",
		"key", key,
		"ip", s.auth.clientIP(r),
		"client", describeClient(r),
		"replaced", old != nil,
		"force", force)

	// 换人时先给 agent 发 browser_offline 再发 online：让 agent 把 web 租约
	// 还给桌面。旧浏览器可能正握着控制权，不还的话终端会卡在没人能输入的状态。
	s.relay.notifyAgentBrowserChange(key, old != nil)
	s.relay.greetBrowser(key, c)

	c.readLoop(func(typ websocket.MessageType, data []byte) bool {
		return s.relay.onBrowserFrame(key, typ, data)
	})
	s.relay.clearBrowser(key, c)
	slog.Info("browser disconnected", "key", key)
}

// Go 的 mime 表里没有 .webmanifest（实测 go1.25.8，TypeByExtension 返回空串），
// http.FileServer 只好嗅探内容，嗅出来是 text/plain。
// 而浏览器只把 JSON 类型的 manifest 当 manifest——类型不对就当作没写，
// 「添加到主屏幕」退回普通网页，而且只在控制台留一条警告，页面上看不出异常。
func init() {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// handleStatic 提供网页本体。
//
// 前端不用路由（两个界面用 v-if 切），所以找不到的路径统一回 index.html 就够。
func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(ui.Dist, "dist")
	if err != nil {
		http.Error(w, "ui assets unavailable", http.StatusInternalServerError)
		return
	}
	fileServer := http.FileServerFS(sub)

	if r.URL.Path == "/" {
		fileServer.ServeHTTP(w, r)
		return
	}
	if _, err := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
		r = r.Clone(r.Context())
		r.URL.Path = "/"
	}
	fileServer.ServeHTTP(w, r)
}

func initLogger(level string) {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(level)); err != nil {
		lv = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lv})))
}
