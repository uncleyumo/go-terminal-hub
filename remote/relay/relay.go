package main

import (
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/uncleyumo/go-terminal-hub/remote/protocol"
)

// kickLoopWindow 是防踢人循环的窗口：同一个 clientId 在被踢之后这么久内
// 又连上来，直接拒绝，且不替换当前的浏览器。
//
// 客户端收到 4001 后应当停止自动重连（第一道闸），但客户端可能有 bug，
// 所以服务端必须有第二道 —— 否则两台手机能把对方无限踢下去。
const kickLoopWindow = 5 * time.Second

// relay 是两侧各一个槽位的转发器。它不理解会话，只搬帧。
//
// 之所以是「哑管道」：会话的唯一真相源在本地 hub 那台机器上（进程、ConPTY、
// 配置都在那儿）。relay 一旦开始缓存状态，就会多出一份可能对不上的副本。
type relay struct {
	mu      sync.Mutex
	agent   *conn
	browser *conn
	kicked  map[string]time.Time // clientId → 上次被踢的时刻
}

func newRelay() *relay {
	return &relay{kicked: make(map[string]time.Time)}
}

// --- 槽位管理 ---------------------------------------------------------------

// setAgent 装上新的 agent 连接，返回被顶掉的旧连接（可能为 nil）。
func (r *relay) setAgent(c *conn) (old *conn) {
	r.mu.Lock()
	defer r.mu.Unlock()

	old = r.agent
	r.agent = c
	return old
}

// setBrowser 装上新的浏览器连接。
//
// force 为 true 表示这是**人手动点的**重连，跳过防踢循环那道闸。
// 那道闸拦的是「客户端自动重连」——两台设备谁也不让谁，无限对踢。
// 但人点「重新连接」的意思是「我就要这个位置」，拦下来只会让人以为坏了。
//
// rejected 为 true 表示这个 clientId 刚被踢过，判定为踢人循环，不予安装。
// 注意：这种情况返回的 old 为 nil，调用方不要顺手把当前浏览器关掉。
func (r *relay) setBrowser(c *conn, clientID string, force bool) (old *conn, rejected bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 顺手清理过期的记录，免得 map 无限长
	for id, t := range r.kicked {
		if time.Since(t) >= kickLoopWindow {
			delete(r.kicked, id)
		}
	}

	// clientID 为空说明这个浏览器没带上 cookie。空串不能当成一个身份去查——
	// 那会让「所有没有 cookie 的浏览器」共用一个防循环记录，互相误伤。
	if !force && clientID != "" {
		if t, ok := r.kicked[clientID]; ok && time.Since(t) < kickLoopWindow {
			return nil, true
		}
	}

	old = r.browser
	r.browser = c
	return old, false
}

// markKicked 记下某个 clientId 被踢的时刻，供 setBrowser 判断踢人循环。
func (r *relay) markKicked(clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kicked[clientID] = time.Now()
}

// clearAgent 在 agent 断开时清槽位，并通知浏览器。
//
// 只清「还是自己」的那一份：如果已经被新连接顶掉，槽位里是新连接，不能动。
func (r *relay) clearAgent(a *conn) {
	r.mu.Lock()
	if r.agent != a {
		r.mu.Unlock()
		return
	}
	r.agent = nil
	b := r.browser
	r.mu.Unlock()

	if b != nil {
		b.sendJSON(protocol.Message{Type: protocol.TypeAgentOffline})
	}
}

// clearBrowser 在浏览器断开时清槽位，并通知 agent。
//
// ⚠️ 这一条不能省。不发的话，手机一关页面，桌面端会永远卡在旁观模式：
// 租约还写着 web，而已经没有谁能把它改回来了。
func (r *relay) clearBrowser(b *conn) {
	r.mu.Lock()
	if r.browser != b {
		r.mu.Unlock()
		return
	}
	r.browser = nil
	a := r.agent
	r.mu.Unlock()

	if a != nil {
		a.sendJSON(protocol.Message{Type: protocol.TypeBrowserOffline})
	}
}

// --- 转发 -------------------------------------------------------------------

// toBrowser 把 agent 的帧转给浏览器。返回 false 表示浏览器读得太慢。
func (r *relay) toBrowser(f frame) bool {
	r.mu.Lock()
	b := r.browser
	r.mu.Unlock()

	if b == nil {
		return true // 没有浏览器，丢掉就行，不是错误
	}
	return b.send(f)
}

// toAgent 把浏览器的帧转给 agent。返回 false 表示 agent 读得太慢。
func (r *relay) toAgent(f frame) bool {
	r.mu.Lock()
	a := r.agent
	r.mu.Unlock()

	if a == nil {
		return true
	}
	return a.send(f)
}

// dropBrowser 踢掉当前的浏览器。它的读循环随后会走 clearBrowser，
// 由那里负责通知 agent 归还租约。
func (r *relay) dropBrowser(code int, reason string) {
	r.mu.Lock()
	b := r.browser
	r.mu.Unlock()

	if b != nil {
		slog.Info("dropping browser", "code", code, "reason", reason)
		b.close(code, reason)
	}
}

func (r *relay) dropAgent(code int, reason string) {
	r.mu.Lock()
	a := r.agent
	r.mu.Unlock()

	if a != nil {
		slog.Info("dropping agent", "code", code, "reason", reason)
		a.close(code, reason)
	}
}

// --- 两侧的读循环 -----------------------------------------------------------

func (r *relay) onAgentFrame(typ websocket.MessageType, data []byte) bool {
	if !r.toBrowser(frame{typ: typ, data: data}) {
		// 手机读得太慢。踢掉它让手机重连重同步 —— 绝不阻塞 agent 的读循环，
		// 那会一路堵回本地 hub 的输出转发路径。
		r.dropBrowser(protocol.CloseSlowConsumer, "browser too slow")
	}
	return true
}

func (r *relay) onBrowserFrame(typ websocket.MessageType, data []byte) bool {
	if !r.toAgent(frame{typ: typ, data: data}) {
		r.dropAgent(protocol.CloseSlowConsumer, "agent too slow")
	}
	return true
}

// --- 给浏览器发当前状态 -----------------------------------------------------

// greetAgent 把「浏览器在不在」告诉刚连上的 agent。
func (r *relay) greetAgent(a *conn) {
	r.mu.Lock()
	online := r.browser != nil
	r.mu.Unlock()

	if online {
		a.sendJSON(protocol.Message{Type: protocol.TypeBrowserOnline})
	}
}

// greetBrowser 把「本地 hub 在不在」告诉刚连上的浏览器。
func (r *relay) greetBrowser(b *conn) {
	r.mu.Lock()
	online := r.agent != nil
	r.mu.Unlock()

	if online {
		b.sendJSON(protocol.Message{Type: protocol.TypeAgentOnline})
		return
	}
	b.sendJSON(protocol.Message{Type: protocol.TypeAgentOffline})
}

// notifyAgentBrowserChange 告诉 agent 浏览器槽位换了人。
//
// 换人时先发 offline 再发 online：offline 让 agent 把所有 web 租约还给桌面。
// 这是「防止终端争抢」的关键一步 —— 旧浏览器可能正握着控制权。
func (r *relay) notifyAgentBrowserChange(replaced bool) {
	r.mu.Lock()
	a := r.agent
	r.mu.Unlock()

	if a == nil {
		return
	}
	if replaced {
		a.sendJSON(protocol.Message{Type: protocol.TypeBrowserOffline})
	}
	a.sendJSON(protocol.Message{Type: protocol.TypeBrowserOnline})
}
