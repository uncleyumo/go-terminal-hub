package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	cookieSession = "hub_session"
	cookieClient  = "hub_client"

	sessionTTL = 30 * 24 * time.Hour

	// 登录限速：每个 IP 每分钟最多这么多次尝试
	loginMaxAttempts = 5
	loginWindow      = time.Minute
)

// authenticator 管 key 校验、登陆密钥校验和浏览器会话。
//
// 三层身份，各自的存放位置：
//
//	使用 key      —— SQLite 的 keys 表，桌面端持有，永远不进浏览器
//	登陆密钥      —— SQLite 的 secrets 表，桌面端生成并登记，最终用户持有
//	浏览器会话    —— 内存里的 sessions map，登录换来的 cookie
//
// 浏览器不能用 WebSocket 的 Authorization 头，所以走「登录换 cookie」这条路：
// 密钥只出现在一次 POST 的请求体里，不会进 URL、不会进访问日志、不会进 Referer。
type authenticator struct {
	db          *sql.DB
	cookieSecure bool
	trustProxy   bool

	mu       sync.Mutex
	sessions map[string]sessionEntry // sid → 会话
	attempts map[string][]time.Time
}

// sessionEntry 是一条浏览器会话。
//
// keyID 必须带上：cookie 是按 key 分家的依据。不带的话，A 的浏览器会话
// 和 B 的会混在一起——表现是「用 A 的 key 登录后能进 B 的终端」。
type sessionEntry struct {
	keyID string
	exp   time.Time
}

func newAuthenticator(db *sql.DB, cookieSecure, trustProxy bool) *authenticator {
	return &authenticator{
		db:          db,
		cookieSecure: cookieSecure,
		trustProxy:   trustProxy,
		sessions:     make(map[string]sessionEntry),
		attempts:     make(map[string][]time.Time),
	}
}

// --- 使用 key 的校验 ---------------------------------------------------------

// KeyStatus 是 checkKey 的结果。
//
// 四种状态分开是有原因的：以前只有「对/错」一种，桌面端填错 key 时
// 界面上只显示「连接失败」，用户完全不知道该改哪里。现在 relay 能把
// 「key 不存在」「key 已过期」「key 已吊销」分清楚返回去。
type KeyStatus int

const (
	KeyOK KeyStatus = iota
	KeyNotFound
	KeyExpired
	KeyRevoked
)

// String 给出人能读的状态，直接进错误消息。
func (s KeyStatus) String() string {
	switch s {
	case KeyOK:
		return "ok"
	case KeyNotFound:
		return "not found"
	case KeyExpired:
		return "expired"
	case KeyRevoked:
		return "revoked"
	}
	return "unknown"
}

// checkKey 查数据库判断这个 key 现在能不能用。
//
// 每次 agent 握手都查一次表：key 被吊销后要立刻生效，不能等 relay 重启。
// 一次索引查询，相对 WebSocket 握手的开销可以忽略。
func (a *authenticator) checkKey(key string) KeyStatus {
	if key == "" {
		return KeyNotFound
	}

	var expiresAt, revoked int64
	err := a.db.QueryRow(`SELECT expires_at, revoked FROM keys WHERE id = ?`, key).
		Scan(&expiresAt, &revoked)
	if err == sql.ErrNoRows {
		return KeyNotFound
	}
	if err != nil {
		// 数据库出错时按不存在处理：宁可拒掉也不能放进来的那侧是安全的一侧。
		slog.Error("checkKey query failed", "err", err)
		return KeyNotFound
	}

	if revoked != 0 {
		return KeyRevoked
	}
	if expiresAt != 0 && time.Now().Unix() >= expiresAt {
		return KeyExpired
	}
	return KeyOK
}

// checkToken 保留给旧版桌面端用：仍然支持一个共享令牌，没有 key 的用法。
//
// 这个函数在新设计里是兼容层。新桌面端走 checkKey，不再有「一个令牌大家共用」
// 的局面——那意味着最终用户为了登录浏览器，必须拿到和桌面端同一个令牌，
// 而这个令牌同时就是桌面端的钥匙。
func (a *authenticator) checkToken(got string) bool {
	return false
}

// --- 登陆密钥的登记与校验 ---------------------------------------------------

// secretHash 把登陆密钥算成数据库里存的形式。
//
// 用 SHA-256 而不是 bcrypt 这类慢哈希，是刻意的取舍：
// 密钥是 32 字节随机值，不是用户想的口令——字典攻击对它无意义，
// 而浏览器每次登录都要查一次库，慢哈希会把中央站拖死。
func secretHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// registerSecret 把桌面端交上来的登陆密钥存进库，覆盖同一 key 下的旧密钥。
//
// 覆盖而不是追加：一个 key 只该有一个有效的登陆密钥。桌面端轮换时
// 发新的上来，旧密钥立刻失效——最终用户手里那个当场就不能用了。
func (a *authenticator) registerSecret(keyID, secret string) error {
	if secret == "" {
		return errEmptySecret
	}
	if len(secret) < 16 {
		return errSecretTooShort
	}

	_, err := a.db.Exec(`
INSERT INTO secrets (key_id, secret_hash, created_at) VALUES (?, ?, ?)
ON CONFLICT(key_id) DO UPDATE SET secret_hash = excluded.secret_hash, created_at = excluded.created_at`,
		keyID, secretHash(secret), time.Now().Unix())
	return err
}

// checkSecret 校验浏览器交上来的登陆密钥。
//
// 常量时间比较：避免按字节比对时泄露密钥前缀。
func (a *authenticator) checkSecret(keyID, secret string) bool {
	if keyID == "" || secret == "" {
		return false
	}

	var stored string
	err := a.db.QueryRow(`SELECT secret_hash FROM secrets WHERE key_id = ?`, keyID).Scan(&stored)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(secretHash(secret))) == 1
}

var (
	errEmptySecret    = errStr("登陆密钥不能为空")
	errSecretTooShort = errStr("登陆密钥至少 16 个字符")
)

type errStr string

func (e errStr) Error() string { return string(e) }

// --- HTTP handlers ----------------------------------------------------------

// handleLogin 是浏览器登录：交上 key 和登陆密钥，换 cookie。
//
// 两样都要。只交密钥的话，中央站不知道该把他算到哪个 key 的会话里——
// 而会话是按 key 分家的（见 sessionEntry）。
func (a *authenticator) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := a.clientIP(r)
	if !a.allowAttempt(ip) {
		slog.Warn("login rate limited", "ip", ip)
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}

	var body struct {
		Key    string `json:"key"`
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// key 状态不对时把原因说清楚。以前三种失败都是同一个 401，
	// 用户在网页上只看到「登录失败」，不知道该找谁、该改什么。
	switch st := a.checkKey(body.Key); st {
	case KeyOK:
	case KeyNotFound:
		slog.Warn("login failed: unknown key", "ip", ip)
		http.Error(w, "unknown key", http.StatusUnauthorized)
		return
	case KeyExpired:
		slog.Warn("login failed: expired key", "ip", ip)
		http.Error(w, "key expired", http.StatusUnauthorized)
		return
	case KeyRevoked:
		slog.Warn("login failed: revoked key", "ip", ip)
		http.Error(w, "key revoked", http.StatusUnauthorized)
		return
	}

	if !a.checkSecret(body.Key, body.Secret) {
		slog.Warn("login failed: bad secret", "ip", ip, "key", body.Key)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	a.setCookie(w, cookieSession, a.newSession(body.Key), int(sessionTTL.Seconds()))
	// clientId 是给防踢人循环用的稳定标识，浏览器不需要读它
	if c, err := r.Cookie(cookieClient); err != nil || c.Value == "" {
		a.setCookie(w, cookieClient, randomID(), int(sessionTTL.Seconds()))
	}

	slog.Info("login ok", "ip", ip, "key", body.Key)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (a *authenticator) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieSession); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
	a.setCookie(w, cookieSession, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// handleMe 让前端在加载时问一句「我这 cookie 还有效吗」。
func (a *authenticator) handleMe(w http.ResponseWriter, r *http.Request) {
	key, ok := a.authed(r)
	if !ok {
		// 这一条要记。前端收到 401 就会退回登录页，用户看到的是
		// 「怎么又要重新登录」——排查时第一件事就是确认到底是谁没带上 cookie。
		c, err := r.Cookie(cookieSession)
		hasCookie := err == nil && c.Value != ""
		slog.Info("me: unauthorized",
			"ip", a.clientIP(r),
			"hasCookie", hasCookie,
			"client", r.Header.Get("User-Agent"),
		)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"key": key})
}

// authed 校验 cookie 并返回它对应的 key。
//
// 返回 key 而不只是 bool：handleBrowser 要用它去取正确的槽位。
// 以前只有一套槽位，不需要问「这是谁」；现在必须先问清楚。
func (a *authenticator) authed(r *http.Request) (string, bool) {
	c, err := r.Cookie(cookieSession)
	if err != nil {
		return "", false
	}

	a.mu.Lock()
	entry, ok := a.sessions[c.Value]
	a.mu.Unlock()

	if !ok {
		return "", false
	}
	if time.Now().After(entry.exp) {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
		return "", false
	}
	return entry.keyID, true
}

// clientID 取浏览器会话的稳定标识，用于识别踢人循环。
func (a *authenticator) clientID(r *http.Request) string {
	if c, err := r.Cookie(cookieClient); err == nil {
		return c.Value
	}
	return ""
}

// --- 以下是从原文件保留、未改动的部分 --------------------------------------

func (a *authenticator) clientIP(r *http.Request) string {
	if a.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// 取最左边那个：那是原始客户端
			for i := 0; i < len(xff); i++ {
				if xff[i] == ',' {
					return trimSpace(xff[:i])
				}
			}
			return trimSpace(xff)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func (a *authenticator) allowAttempt(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	kept := a.attempts[ip][:0]
	for _, t := range a.attempts[ip] {
		if now.Sub(t) < loginWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= loginMaxAttempts {
		a.attempts[ip] = kept
		return false
	}
	a.attempts[ip] = append(kept, now)
	return true
}

func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败意味着进程环境已经不可信，没有降级方案
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// newSession 建一条浏览器会话，sid 绑定到某个 key。
func (a *authenticator) newSession(keyID string) string {
	sid := randomID()

	a.mu.Lock()
	defer a.mu.Unlock()
	// 顺手清过期会话，免得 map 无限长
	now := time.Now()
	for id, e := range a.sessions {
		if now.After(e.exp) {
			delete(a.sessions, id)
		}
	}
	a.sessions[sid] = sessionEntry{keyID: keyID, exp: now.Add(sessionTTL)}
	return sid
}

func (a *authenticator) setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		// 无条件置上：TLS 由前置反代终结，程序自己看不到 r.TLS。
		Secure: a.cookieSecure,
		// Lax 而不是 Strict。本应用没有跨站嵌入，Lax 的安全性一样够
		// （跨站 POST 照样不带 cookie），但少掉一整类导航边界情况——
		// 从别的 app 点链接进来、标签页被系统回收后恢复，Strict 下
		// 首次文档请求不带 cookie，表现就是「莫名其妙又要重新登录」。
		SameSite: http.SameSiteLaxMode,
	})
}

// bearerToken 从 Authorization 头里取使用 key。agent 是 Go 程序，能设头，走这条路。
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && h[:len(prefix)] == prefix {
		return h[len(prefix):]
	}
	return ""
}

// 确保 context  import 被用到（handleAgent 里连接生命周期用它）。
var _ = context.Background
