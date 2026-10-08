package hub

import (
	"log/slog"
	"sync"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
)

// Tap 是会话生命周期和租约变化的旁路观察者。
//
// 它存在的理由是打破 import 环：hub 不能 import internal/remote（remote 要 import hub），
// 所以由 remote 实现这个接口、反过来注册进来。
//
// 只管「发生了什么」，不管输出字节——输出走 Subscribe，那条路有背压和回放。
//
// ⚠️ 回调**跑在一个专用协程上**，不在触发它的那个协程上。见下面的 notifyTap。
type Tap interface {
	OnStarted(id string)
	OnExited(id string, res executor.ExitResult)
	// OnLease 在控制方换人时触发，也在控制方改了尺寸时触发——
	// 两种情况旁观方都要跟着重新渲染。
	OnLease(id, controller string, cols, rows uint16)
}

// tapHolder 挂在 Hub 上，不是包级全局。
//
// 全局那一份会让两个 Hub 实例互相收到对方的通知——测试里每个用例都 NewHub()，
// 于是上一个用例排在队列里的通知会被投给下一个用例的 Tap，断言里就凭空多出一条。
type tapHolder struct {
	mu  sync.RWMutex
	tap Tap
}

// tapNotice 是排队中的一次通知。
//
// 带上 holder 而不是等派发时去查：投给谁在**入队时**就定下来了，
// 队列里躺着的旧通知不会因为 SetTap 换了人而改投。
type tapNotice struct {
	holder *tapHolder
	fn     func(Tap)
}

// tapQueue 把通知从触发方挪到一个专用协程上去执行。
//
// ⚠️ 这不是性能优化，是**防死锁**。
//
// 触发点常常在持锁状态下：Session.Start 是 Hub.Start 在持着 h.mu 时调的，
// 而 Tap 的实现（远程桥）会回头去读 Hub（列会话状态、取 PTY 尺寸）。
// 直接在原地回调就是拿同一把不可重入的锁再锁一次——自己把自己锁死，
// 表现是「启动按钮一直亮着、打不进字、别的会话也起不来、桥判死掉线」。
//
// 队列有界，满了就丢：Tap 只推 UI 状态，丢一条下次列表刷新会补上，
// 绝不能为了它把 hub 卡住。
var tapQueue = make(chan tapNotice, 256)

func init() {
	go func() {
		for n := range tapQueue {
			n.holder.mu.RLock()
			t := n.holder.tap
			n.holder.mu.RUnlock()
			if t == nil {
				continue
			}
			func() {
				// 观察者崩了不该拖垮整个 hub
				defer func() {
					if r := recover(); r != nil {
						slog.Error("tap callback panicked", "panic", r)
					}
				}()
				n.fn(t)
			}()
		}
	}()
}

// SetTap 注册观察者。传 nil 取消注册。
func (h *Hub) SetTap(t Tap) {
	h.tap.mu.Lock()
	defer h.tap.mu.Unlock()
	h.tap.tap = t
}

// notifyTap 把一次通知排进队列。**立即返回**，不在调用方协程上执行。
func notifyTap(holder *tapHolder, fn func(Tap)) {
	select {
	case tapQueue <- tapNotice{holder: holder, fn: fn}:
	default:
		slog.Warn("tap queue full, dropping notification")
	}
}
