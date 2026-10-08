package hub

import (
	"sync"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/session"
)

// Subscriber 是终端输出的一路消费者（目前只有远程桥）。
//
// 广播器不把字节推给订阅者，只敲一下「有新数据」，字节留在环形缓冲里由订阅者
// 按游标自己读。这样三件事一起解决了：
//
//   - 不阻塞 drainLoop。Notify 是纯信号，订阅者慢不慢跟它无关。
//   - 天然合并。订阅者读得慢，下次就读到更大的一块，不会堆积成一堆小帧。
//   - 不会丢。落后超过环容量时游标失效，订阅者整段重同步。
//
// Notify 在 Session 的 drainLoop goroutine 上被同步调用，必须立刻返回——
// 在那里阻塞会一路堵回 ConPTY 读循环，把子进程的 write 卡死。
//
// 「进程退出了」不走这里，走 Tap.OnExited：那是全量事件，订阅者按会话 id 过滤。
type Subscriber interface {
	Notify()
}

// broadcaster 是一个会话唯一的 sink，做两件事：
//
//  1. 把输出原样转给桌面那条链（BatchingSink → EmitSink）
//  2. 记进环形缓冲，并通知所有订阅者
//
// 环和订阅者必须在同一把锁下：订阅者拿到环之后从游标 0 开始读，
// 于是「历史」和「之后的实时输出」是同一段连续字节，不存在快照与注册之间的缝。
type broadcaster struct {
	mu   sync.Mutex
	id   string
	ring *Ring
	emit session.Sink
	subs map[Subscriber]struct{}
}

func newBroadcaster(id string, capacity int, emit session.Sink) *broadcaster {
	return &broadcaster{
		id:   id,
		ring: NewRing(capacity),
		emit: emit,
		subs: make(map[Subscriber]struct{}),
	}
}

func (b *broadcaster) OnOutput(chunk []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 先记历史：这样即使 emit 那边出错，回放也是完整的。
	b.ring.Append(chunk)
	b.emit.OnOutput(chunk)
	for s := range b.subs {
		s.Notify()
	}
}

func (b *broadcaster) OnStarted(id string) {
	b.mu.Lock()
	b.emit.OnStarted(id)
	b.mu.Unlock()

	notifyTap(func(t Tap) { t.OnStarted(id) })
}

func (b *broadcaster) OnProcessExited(res executor.ExitResult) {
	b.mu.Lock()
	// 注意：BatchingSink 收到 OnProcessExited 会 close 自己的 quit 通道，
	// 同一个实例收到第二次会 panic。reapLoop 只跑一次，所以这里安全——
	// 但别再让订阅者那条路也调一遍 emit。
	b.emit.OnProcessExited(res)
	id := b.id
	b.mu.Unlock()

	notifyTap(func(t Tap) { t.OnExited(id, res) })
}

// subscribe 把 s 挂上，并交出该会话的环形缓冲。
//
// 订阅者拿到环之后从游标 0 开始读，读到的就是「全部历史 + 之后的实时输出」，
// 中间不会漏也不会重。不需要单独的快照步骤，所以也就不存在快照与注册之间的竞态。
func (b *broadcaster) subscribe(s Subscriber) *Ring {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subs[s] = struct{}{}
	return b.ring
}

func (b *broadcaster) unsubscribe(s Subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs, s)
}

// takeSubs 把旧 broadcaster 上的订阅者搬过来。
//
// 重启会话会新建一条 emit 链（旧的 BatchingSink 已经 close 掉了），
// 但订阅者不该因此掉线——远程桥还连着浏览器，它只是要继续收新一轮的输出。
// 注意环不搬：新一轮不该回放上一轮的内容。
func (b *broadcaster) takeSubs(old *broadcaster) {
	old.mu.Lock()
	moved := make([]Subscriber, 0, len(old.subs))
	for s := range old.subs {
		moved = append(moved, s)
	}
	old.subs = make(map[Subscriber]struct{})
	old.mu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range moved {
		b.subs[s] = struct{}{}
	}
}
