package hub

import (
	"errors"
	"testing"
	"time"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
)

// addSession 往单例 store 里塞一条能跑的会话定义，返回它的 id。
func addSession(t *testing.T, id, target string) {
	t.Helper()
	storeInst, err := store.GetStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := storeInst.Add(store.DataStore{
		ID:     id,
		Name:   id,
		Kind:   "shell",
		Target: target,
		Mode:   "terminal",
	}); err != nil {
		t.Fatal(err)
	}
}

// reentrantTap 在 OnStarted 里回头读 Hub —— 这正是远程桥干的事：
// Agent.OnStarted → bridge.sendList → hub.ListRecordStatus。
//
// 旧实现把 Tap 放在**持着 h.mu 的 Start 协程上同步调**，
// 上面那一行 ListRecordStatus 就是拿同一把不可重入的锁再锁一次 = 自锁死。
type reentrantTap struct {
	hub   *Hub
	got   chan struct{}
	lease chan struct{}
}

func (t *reentrantTap) OnStarted(id string) {
	_ = t.hub.ListRecordStatus()
	_, _ = t.hub.Controller(id)
	select {
	case t.got <- struct{}{}:
	default:
	}
}

func (t *reentrantTap) OnExited(id string, res executor.ExitResult) {}

func (t *reentrantTap) OnLease(id, controller string, cols, rows uint16) {
	select {
	case t.lease <- struct{}{}:
	default:
	}
}

// TestStartSessionWithReentrantTap 是那次线上故障的回归测试：
// 启动后按钮一直亮着、打不进字、别的会话也起不来、桥判死掉线——
// 全都是 Start 自锁死的外在表现。
func TestStartSessionWithReentrantTap(t *testing.T) {
	storeInst, err := store.GetStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := storeInst.UpdateDataJson(nil); err != nil {
		t.Fatal(err)
	}

	id := "01930000-0000-7000-8000-0000000000a1"
	addSession(t, id, "echo hi")

	h := NewHub()
	tap := &reentrantTap{hub: h, got: make(chan struct{}, 1), lease: make(chan struct{}, 1)}
	h.SetTap(tap)
	defer h.SetTap(nil)

	done := make(chan error, 1)
	go func() { done <- h.StartSession(id) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StartSession 报错: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("StartSession 卡住了 —— 持锁回调 Tap 造成的自锁死又回来了")
	}

	// 回调必须真的跑到了：它跑在 tapQueue 那条专用协程上，不是在 Start 的协程上
	select {
	case <-tap.got:
	case <-time.After(5 * time.Second):
		t.Fatal("Tap.OnStarted 一直没被调到")
	}

	// 登记必须完成：登记完 ListRecordStatus 才看得见这个会话，
	// 前端就是靠它判断「启动按钮该不该亮」的
	status := h.ListRecordStatus()
	if len(status) != 1 || status[0].ID != id || !status[0].Running {
		t.Fatalf("登记没完成，ListRecordStatus = %+v", status)
	}
}

// blockingStartSink 在 OnStarted 里卡住，好让测试抓住「Start 已经进到进程启动、
// 但还没登记 Record」的那个窗口。
type blockingStartSink struct {
	testSink
	entered chan struct{}
	unblock chan struct{}
}

func (s *blockingStartSink) OnStarted(id string) {
	close(s.entered)
	<-s.unblock
}

// TestStartingSlotHeldUntilRegistered 盯住「正在启动」那个名额。
//
// 检查与登记之间是放开了锁的，名额必须一直占到 Record 登记完为止。
// 中途就放掉的话，两次并发 Start 会各起一个进程，后登记的把前一个覆盖掉，
// 前一个进程就成了没人管得着的孤儿。
func TestStartingSlotHeldUntilRegistered(t *testing.T) {
	storeInst, err := store.GetStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := storeInst.UpdateDataJson(nil); err != nil {
		t.Fatal(err)
	}

	id := "01930000-0000-7000-8000-0000000000a2"
	addSession(t, id, "echo hi")

	h := NewHub()
	s := &blockingStartSink{
		testSink: testSink{exited: make(chan executor.ExitResult, 1)},
		entered:  make(chan struct{}),
		unblock:  make(chan struct{}),
	}

	done := make(chan error, 1)
	go func() { done <- h.Start(id, s) }()

	select {
	case <-s.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("进程没起来")
	}

	h.mu.Lock()
	_, busy := h.starting[id]
	_, registered := h.records[id]
	h.mu.Unlock()

	if registered {
		t.Fatal("这个窗口里不该已经登记了，测试前提不成立")
	}
	if !busy {
		t.Fatal("名额在 Record 登记之前就被放掉了，并发 Start 会各起一个进程")
	}

	close(s.unblock)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start 报错: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("放行之后 Start 没回来")
	}

	h.mu.Lock()
	n := len(h.starting)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("Start 返回后名额还留着 %d 条，这个 id 永远起不来了", n)
	}
}

// 抖尺寸只对控制方有效。
//
// 旁观方动不了 PTY 的行列数——尺寸是控制方的，这是整个租约的地基。
// 而控制方调的时候必须**真的抖两下**：只改一次的话程序收到的是「尺寸没变」，
// 不会重绘，等于没抖。
func TestNudgeRepaintRespectsLease(t *testing.T) {
	storeInst, err := store.GetStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := storeInst.UpdateDataJson(nil); err != nil {
		t.Fatal(err)
	}

	// 要一个活得够久的进程，不然抖到一半它已经退出了
	id := "01930000-0000-7000-8000-0000000000a3"
	addSession(t, id, "ping -n 20 127.0.0.1")

	h := NewHub()
	s := &testSink{exited: make(chan executor.ExitResult, 1)}
	if err := h.Start(id, s); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Stop(id) }()

	// 刚起来时控制方是桌面
	if who, ok := h.Controller(id); !ok || who != SourceDesktop {
		t.Fatalf("控制方 = %q，want %q", who, SourceDesktop)
	}

	// 还没接管的 web 端来抖：拒掉
	if err := h.NudgeRepaint(id, SourceWeb); !errors.Is(err, ErrNotController) {
		t.Errorf("旁观方 NudgeRepaint 应该被拒，实际 %v", err)
	}

	// 控制方来抖：成功，而且中间那一下真的发生了
	start := time.Now()
	if err := h.NudgeRepaint(id, SourceDesktop); err != nil {
		t.Fatalf("控制方 NudgeRepaint 报错: %v", err)
	}
	if d := time.Since(start); d < nudgeGap {
		t.Errorf("只花了 %v（间隔是 %v），说明中间那一下没抖，程序不会重绘", d, nudgeGap)
	}
}

// TestStartingSlotReleasedOnError 走错误路径也必须放掉名额，
// 否则一次失败就把这个 id 永久锁死。
func TestStartingSlotReleasedOnError(t *testing.T) {
	h := NewHub()

	if err := h.StartSession("01930000-0000-7000-8000-0000000000ff"); err == nil {
		t.Fatal("store 里没有这个会话，应该报错")
	}

	h.mu.Lock()
	n := len(h.starting)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("失败路径没放掉名额，starting 还剩 %d 条", n)
	}
}
