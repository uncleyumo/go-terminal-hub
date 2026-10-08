package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
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

// authenticator 管令牌校验和浏览器会话。
//
// 浏览器不能用 WebSocket 的 Authorization 头，所以走「登录换 cookie」这条路：
// 令牌只出现在一次 POST 的请求体里，不会进 URL、不会进访问日志、不会进 Referer。
type authenticator struct {
	token        string
	cookieSecure bool
	trustProxy   bool

	mu       sync.Mutex
	sessions map[string]time.Time // sid → 过期时刻
	attempts map[string][]time.Time
}

func newAuthenticator(token string, cookieSecure, trustProxy bool) *authenticator {
	return &authenticator{
		token:        token,
		cookieSecure: cookieSecure,
		trustProxy:   trustProxy,
		sessions:     make(map[string]time.Time),
		attempts:     make(map[string][]time.Time),
	}
}

// checkToken 用常量时间比较，避免按字节比较泄露令牌前缀。
func (a *authenticator) checkToken(got string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) == 1
}

// bearerToken 从 Authorization 头里取令牌。agent 是 Go 程序，能设头，走这条路。
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && h[:len(prefix)] == prefix {
		return h[len(prefix):]
	}
	return ""
}

// clientIP 取来源 IP。放在反代后面时必须信任 X-Forwarded-For，
// 否则限速会把所有请求算到反代那一个 IP 上。
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

// allowAttempt 做滑动窗口限速。返回 false 表示这次尝试应当被拒。
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

func (a *authenticator) newSession() string {
	sid := randomID()

	a.mu.Lock()
	defer a.mu.Unlock()
	// 顺手清过期会话，免得 map 无限长
	now := time.Now()
	for id, exp := range a.sessions {
		if now.After(exp) {
			delete(a.sessions, id)
		}
	}
	a.sessions[sid] = now.Add(sessionTTL)
	return sid
}

func (a *authenticator) validSession(sid string) bool {
	if sid == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	exp, ok := a.sessions[sid]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(a.sessions, sid)
		return false
	}
	return true
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

// --- HTTP handlers ----------------------------------------------------------

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
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if !a.checkToken(body.Token) {
		slog.Warn("login failed", "ip", ip)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	a.setCookie(w, cookieSession, a.newSession(), int(sessionTTL.Seconds()))
	// clientId 是给防踢人循环用的稳定标识，浏览器不需要读它
	if c, err := r.Cookie(cookieClient); err != nil || c.Value == "" {
		a.setCookie(w, cookieClient, randomID(), int(sessionTTL.Seconds()))
	}

	slog.Info("login ok", "ip", ip)
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
	if !a.authed(r) {
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
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (a *authenticator) authed(r *http.Request) bool {
	c, err := r.Cookie(cookieSession)
	if err != nil {
		return false
	}
	return a.validSession(c.Value)
}

// clientID 取浏览器会话的稳定标识，用于识别踢人循环。
func (a *authenticator) clientID(r *http.Request) string {
	if c, err := r.Cookie(cookieClient); err == nil {
		return c.Value
	}
	return ""
}
