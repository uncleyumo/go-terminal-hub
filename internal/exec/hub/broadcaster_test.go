package hub

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
)

// fakeSink 冒充桌面那条链，记下收到的字节。
type fakeSink struct {
	mu     sync.Mutex
	buf    []byte
	exited int
}

func (s *fakeSink) OnStarted(id string) {}

func (s *fakeSink) OnOutput(chunk []byte) {
	s.mu.Lock()
	s.buf = append(s.buf, chunk...)
	s.mu.Unlock()
}

func (s *fakeSink) OnProcessExited(res executor.ExitResult) {
	s.mu.Lock()
	s.exited++
	s.mu.Unlock()
}

func (s *fakeSink) output() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.buf...)
}

// fakeSub 是订阅者的替身，只数通知次数。真实实现拿到 ring 后自己去读。
type fakeSub struct {
	mu       sync.Mutex
	notifies int
}

func (s *fakeSub) Notify() {
	s.mu.Lock()
	s.notifies++
	s.mu.Unlock()
}

func (s *fakeSub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notifies
}

// 桌面那条链的行为不能被改变。
func TestBroadcasterForwardsToEmit(t *testing.T) {
	emit := &fakeSink{}
	b := newBroadcaster("s1", 64, emit)

	b.OnOutput([]byte("hello"))
	b.OnOutput([]byte(" world"))
	b.OnStarted("id-1")

	if got := emit.output(); !bytes.Equal(got, []byte("hello world")) {
		t.Errorf("emit 收到 %q，want %q", got, "hello world")
	}
}

// 订阅者从游标 0 读，拿到的就是「全部历史 + 之后的实时输出」，中间不漏不重。
func TestBroadcasterSubscribeGivesContinuousStream(t *testing.T) {
	b := newBroadcaster("s1", 64, &fakeSink{})
	b.OnOutput([]byte("before "))

	sub := &fakeSub{}
	ring := b.subscribe(sub)

	// 从 0 读 → 历史
	replay, cursor, ok := ring.Since(0)
	if !ok || !bytes.Equal(replay, []byte("before ")) {
		t.Fatalf("Since(0) = (%q, %v), want (before , true)", replay, ok)
	}

	// 新输出之后接着读 → 实时，且与历史严丝合缝
	b.OnOutput([]byte("after"))
	live, _, ok := ring.Since(cursor)
	if !ok || !bytes.Equal(live, []byte("after")) {
		t.Fatalf("Since(%d) = (%q, %v), want (after, true)", cursor, live, ok)
	}

	full := append(append([]byte(nil), replay...), live...)
	if !bytes.Equal(full, []byte("before after")) {
		t.Errorf("拼起来 = %q，want %q", full, "before after")
	}
	if sub.count() != 1 {
		t.Errorf("订阅后收到 %d 次通知，want 1", sub.count())
	}
}

func TestBroadcasterUnsubscribe(t *testing.T) {
	b := newBroadcaster("s1", 64, &fakeSink{})
	sub := &fakeSub{}
	b.subscribe(sub)
	b.unsubscribe(sub)

	b.OnOutput([]byte("after unsub"))
	if sub.count() != 0 {
		t.Errorf("取消订阅后不该再收到通知，实际 %d 次", sub.count())
	}
}

// 进程退出只通知桌面链一次——BatchingSink 收到第二次会 panic。
// 订阅者不走这条路，它靠 Tap.OnExited。
func TestBroadcasterExitNotifiesEmitOnce(t *testing.T) {
	emit := &fakeSink{}
	b := newBroadcaster("s1", 64, emit)

	b.OnProcessExited(executor.ExitResult{Code: 0})

	if emit.exited != 1 {
		t.Errorf("emit 收到 %d 次退出，want 1", emit.exited)
	}
}

// Tap 要能收到会话的开始、退出和租约变化——远程桥靠它把状态推给浏览器。
func TestTapReceivesLifecycle(t *testing.T) {
	rec := &fakeTap{}
	h := NewHub()
	h.SetTap(rec)
	defer h.SetTap(nil)

	b := newBroadcaster("s1", 64, &fakeSink{})
	b.OnStarted("s1")
	b.OnProcessExited(executor.ExitResult{Code: 3})

	// Tap 是**异步**调的（见 tap.go：就地回调会跟 Hub 的锁自锁死），
	// 所以这里得等，不能发完就断言。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		started, exited := rec.snapshot()
		if len(started) > 0 && len(exited) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	started, exited := rec.snapshot()
	if len(started) != 1 || started[0] != "s1" {
		t.Errorf("OnStarted = %v, want [s1]", started)
	}
	if len(exited) != 1 || exited[0] != "s1" {
		t.Errorf("OnExited = %v, want [s1]", exited)
	}
}

type fakeTap struct {
	mu      sync.Mutex
	started []string
	exited  []string
	leases  []string
}

// snapshot 加锁读。Tap 现在跑在别的协程上，裸读切片是数据竞争。
func (t *fakeTap) snapshot() (started, exited []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.started...), append([]string(nil), t.exited...)
}

func (t *fakeTap) OnStarted(id string) { t.mu.Lock(); t.started = append(t.started, id); t.mu.Unlock() }
func (t *fakeTap) OnExited(id string, res executor.ExitResult) {
	t.mu.Lock()
	t.exited = append(t.exited, id)
	t.mu.Unlock()
}
func (t *fakeTap) OnLease(id, controller string, cols, rows uint16) {
	t.mu.Lock()
	t.leases = append(t.leases, controller)
	t.mu.Unlock()
}

// 重启会话会换一条新的 emit 链，但订阅者要跟过来，否则远程桥在重启后静默失联。
func TestBroadcasterTakeSubs(t *testing.T) {
	old := newBroadcaster("s1", 64, &fakeSink{})
	sub := &fakeSub{}
	old.subscribe(sub)

	fresh := newBroadcaster("s1", 64, &fakeSink{})
	fresh.takeSubs(old)

	fresh.OnOutput([]byte("new run"))
	if sub.count() != 1 {
		t.Errorf("订阅者在新 broadcaster 上收到 %d 次通知，want 1", sub.count())
	}

	// 旧的必须清空，免得同一份输出被投递两遍
	old.OnOutput([]byte("old run"))
	if sub.count() != 1 {
		t.Errorf("旧 broadcaster 不该再通知，实际共 %d 次", sub.count())
	}
}

// 新 broadcaster 是空环：重启后不该回放上一轮的输出。
func TestBroadcasterFreshRingAfterRestart(t *testing.T) {
	old := newBroadcaster("s1", 64, &fakeSink{})
	old.OnOutput([]byte("previous run output"))

	fresh := newBroadcaster("s1", 64, &fakeSink{})
	fresh.takeSubs(old)

	sub := &fakeSub{}
	ring := fresh.subscribe(sub)
	if got, _, _ := ring.Since(0); len(got) != 0 {
		t.Errorf("重启后不该有回放内容，实际 %q", got)
	}
}
