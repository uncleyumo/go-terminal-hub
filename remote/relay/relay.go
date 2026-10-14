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
// 所以服务端必须有第二道 —— 否则两台设备能把对方无限踢下去。
const kickLoopWindow = 5 * time.Second

// 全局并发上限。
//
// 每个 key 一套槽位，每套最多两条长连接。没有上限的话，key 签出去一万张
// 就有两万条连接挂着，内存和文件描述符都会被吃光。
// 1024 套对「发给朋友用」这个量级远远够，且这个数 purely 是防御性的——
// 正常用法下同时在线的不超过个位数。
const maxSlots = 1024

// slot 是一套完整的「一个 agent + 一个浏览器」。
//
// 这就是原来 relay 结构的全部内容，原样搬过来。按 key 拆开的意思只是
// 「一个 key 拥有一套 slot」，而不是把 slot 内部改掉。
type slot struct {
	agent   *conn
	browser *conn
	kicked  map[string]time.Time // clientId → 上次被踢的时刻
}

func newSlot() *slot {
	return &slot{kicked: make(map[string]time.Time)}
}

// relay 是按 key 组织的转发器集合。
//
// 从「单套槽位」改成「按 key 各一套」是整个中央站改造的核心。
// relay 仍然不理解会话，只搬帧——会话的唯一真相源在本地 hub 那台机器上
// （进程、ConPTY、配置都在那儿）。relay 一旦开始缓存状态，就会多出一份
// 可能对不上的副本。
//
// 按 key 拆开时没有需要分家的会话状态，所以这件事比想象干净：
// 要拆的只有连接槽位，而连接是无状态的。
type relay struct {
	mu    sync.Mutex
	slots map[string]*slot
}

func newRelay() *relay {
	return &relay{slots: make(map[string]*slot)}
}

// --- 槽位管理 ---------------------------------------------------------------

// getOrCreateSlot 取出某个 key 的槽位，没有就建。
//
// 超过 maxSlots 时返回 nil：调用方必须处理这种情况（拒掉新连接），
// 不能假定一定有槽位。
func (r *relay) getOrCreateSlot(key string) *slot {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s, ok := r.slots[key]; ok {
		return s
	}
	if len(r.slots) >= maxSlots {
		return nil
	}
	s := newSlot()
	r.slots[key] = s
	return s
}

// getSlot 只读地取槽位，不创建。可能为 nil。
func (r *relay) getSlot(key string) *slot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.slots[key]
}

// dropSlot 删掉整个槽位。
//
// 只在 key 被吊销/删除时调用：那个 key 下的两条连接都该断。
// 普通的 agent/browser 断开不走这里——那只是槽位里某一侧空出来，
// 槽位本身还要留着（agent 重连时它还得是同一个，防踢记录也还在）。
func (r *relay) dropSlot(key string) {
	r.mu.Lock()
	s := r.slots[key]
	delete(r.slots, key)
	r.mu.Unlock()

	if s == nil {
		return
	}
	// 锁外关连接：close 会往队列里塞帧，可能阻塞。
	if s.agent != nil {
		s.agent.close(protocol.CloseKeyRevoked, "key revoked")
	}
	if s.browser != nil {
		s.browser.close(protocol.CloseKeyRevoked, "key revoked")
	}
}

// setAgent 装上新的 agent 连接，返回被顶掉的旧连接（可能为 nil）。
func (r *relay) setAgent(key string, c *conn) (old *conn) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := r.slots[key]
	if s == nil {
		// 调用方必须先 getOrCreateSlot。这里是防御：真发生了，
		// 静默丢掉比 panic 好，但日志要留下线索。
		slog.Error("setAgent on missing slot", "key", key)
		return nil
	}
	old = s.agent
	s.agent = c
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
func (r *relay) setBrowser(key string, c *conn, clientID string, force bool) (old *conn, rejected bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := r.slots[key]
	if s == nil {
		slog.Error("setBrowser on missing slot", "key", key)
		return nil, false
	}

	// 顺手清理过期的记录，免得 map 无限长
	for id, t := range s.kicked {
		if time.Since(t) >= kickLoopWindow {
			delete(s.kicked, id)
		}
	}

	// clientID 为空说明这个浏览器没带上 cookie。空串不能当成一个身份去查——
	// 那会让「所有没有 cookie 的浏览器」共用一个防循环记录，互相误伤。
	if !force && clientID != "" {
		if t, ok := s.kicked[clientID]; ok && time.Since(t) < kickLoopWindow {
			return nil, true
		}
	}

	old = s.browser
	s.browser = c
	return old, false
}

// markKicked 记下某个 clientId 被踢的时刻，供 setBrowser 判断踢人循环。
func (r *relay) markKicked(key, clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := r.slots[key]
	if s == nil {
		return
	}
	s.kicked[clientID] = time.Now()
}

// clearAgent 在 agent 断开时清槽位，并通知浏览器。
//
// 只清「还是自己」的那一份：如果已经被新连接顶掉，槽位里是新连接，不能动。
func (r *relay) clearAgent(key string, a *conn) {
	r.mu.Lock()
	s := r.slots[key]
	if s == nil || s.agent != a {
		r.mu.Unlock()
		return
	}
	s.agent = nil
	b := s.browser
	r.mu.Unlock()

	if b != nil {
		b.sendJSON(protocol.Message{Type: protocol.TypeAgentOffline})
	}
}

// clearBrowser 在浏览器断开时清槽位，并通知 agent。
//
// ⚠️ 这一条不能省。不发的话，手机一关页面，桌面端会永远卡在旁观模式：
// 租约还写着 web，而已经没有谁能把它改回来了。
func (r *relay) clearBrowser(key string, b *conn) {
	r.mu.Lock()
	s := r.slots[key]
	if s == nil || s.browser != b {
		r.mu.Unlock()
		return
	}
	s.browser = nil
	a := s.agent
	r.mu.Unlock()

	if a != nil {
		a.sendJSON(protocol.Message{Type: protocol.TypeBrowserOffline})
	}
}

// --- 转发 -------------------------------------------------------------------

// toBrowser 把 agent 的帧转给同一个 key 下的浏览器。返回 false 表示浏览器读得太慢。
func (r *relay) toBrowser(key string, f frame) bool {
	r.mu.Lock()
	s := r.slots[key]
	var b *conn
	if s != nil {
		b = s.browser
	}
	r.mu.Unlock()

	if b == nil {
		return true // 没有浏览器，丢掉就行，不是错误
	}
	return b.send(f)
}

// toAgent 把浏览器的帧转给同一个 key 下的 agent。返回 false 表示 agent 读得太慢。
func (r *relay) toAgent(key string, f frame) bool {
	r.mu.Lock()
	s := r.slots[key]
	var a *conn
	if s != nil {
		a = s.agent
	}
	r.mu.Unlock()

	if a == nil {
		return true
	}
	return a.send(f)
}

// dropBrowser 踢掉某个 key 下当前的浏览器。它的读循环随后会走 clearBrowser，
// 由那里负责通知 agent 归还租约。
func (r *relay) dropBrowser(key string, code int, reason string) {
	r.mu.Lock()
	s := r.slots[key]
	var b *conn
	if s != nil {
		b = s.browser
	}
	r.mu.Unlock()

	if b != nil {
		slog.Info("dropping browser", "key", key, "code", code, "reason", reason)
		b.close(code, reason)
	}
}

func (r *relay) dropAgent(key string, code int, reason string) {
	r.mu.Lock()
	s := r.slots[key]
	var a *conn
	if s != nil {
		a = s.agent
	}
	r.mu.Unlock()

	if a != nil {
		slog.Info("dropping agent", "key", key, "code", code, "reason", reason)
		a.close(code, reason)
	}
}

// --- 两侧的读循环 -----------------------------------------------------------

func (r *relay) onAgentFrame(key string, typ websocket.MessageType, data []byte) bool {
	if !r.toBrowser(key, frame{typ: typ, data: data}) {
		// 手机读得太慢。踢掉它让手机重连重同步 —— 绝不阻塞 agent 的读循环，
		// 那会一路堵回本地 hub 的输出转发路径。
		r.dropBrowser(key, protocol.CloseSlowConsumer, "browser too slow")
	}
	return true
}

func (r *relay) onBrowserFrame(key string, typ websocket.MessageType, data []byte) bool {
	if !r.toAgent(key, frame{typ: typ, data: data}) {
		r.dropAgent(key, protocol.CloseSlowConsumer, "agent too slow")
	}
	return true
}

// --- 给两侧发当前状态 -------------------------------------------------------

// greetAgent 把「浏览器在不在」告诉刚连上的 agent。
func (r *relay) greetAgent(key string, a *conn) {
	r.mu.Lock()
	s := r.slots[key]
	online := s != nil && s.browser != nil
	r.mu.Unlock()

	if online {
		a.sendJSON(protocol.Message{Type: protocol.TypeBrowserOnline})
	}
}

// greetBrowser 把「本地 hub 在不在」告诉刚连上的浏览器。
func (r *relay) greetBrowser(key string, b *conn) {
	r.mu.Lock()
	s := r.slots[key]
	online := s != nil && s.agent != nil
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
func (r *relay) notifyAgentBrowserChange(key string, replaced bool) {
	r.mu.Lock()
	s := r.slots[key]
	var a *conn
	if s != nil {
		a = s.agent
	}
	r.mu.Unlock()

	if a == nil {
		return
	}
	if replaced {
		a.sendJSON(protocol.Message{Type: protocol.TypeBrowserOffline})
	}
	a.sendJSON(protocol.Message{Type: protocol.TypeBrowserOnline})
}
