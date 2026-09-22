package session

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/conpty"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
)

// fakeSink 是 Sink 的测试替身：Session 递出来什么，就原样记下来。
type fakeSink struct {
	mu       sync.Mutex
	out      []byte
	exitedCh chan executor.ExitResult
}

func newFakeSink() *fakeSink {
	return &fakeSink{exitedCh: make(chan executor.ExitResult, 1)}
}

func (f *fakeSink) OnOutput(chunk []byte) {
	f.mu.Lock()
	f.out = append(f.out, chunk...)
	f.mu.Unlock()
}

func (f *fakeSink) OnProcessExited(res executor.ExitResult) {
	f.exitedCh <- res
}

func (f *fakeSink) output() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.out)
}

// fakeExecutor 是 Executor 的测试替身：按剧本吐几批输出，然后报一个退出码。
//
// 它只管 Session 有没有把「排干 → 收尾 → 报退出」做全，
// ConPTY 那一层的真伪归 TestConPTYExecutorStart。
type fakeExecutor struct {
	outCh      chan []byte
	procExitCh chan executor.ExitResult
	closeCalls chan struct{}
	killCalls  chan struct{}
	chunks     [][]byte
	res        executor.ExitResult
	startErr   error
	stopErr    error
	autoExit   bool
	onStop     func() // StopProcess() 被调时干什么
	onKill     func() // KillProcess() 被调时干什么
}

// newFakeExecutor 造一个跑完就走的假进程：Start 后吐 chunks，并自动报退出码。
func newFakeExecutor(chunks [][]byte, res executor.ExitResult) *fakeExecutor {
	return &fakeExecutor{
		outCh:      make(chan []byte),
		procExitCh: make(chan executor.ExitResult),
		closeCalls: make(chan struct{}, 1),
		killCalls:  make(chan struct{}, 1),
		chunks:     chunks,
		res:        res,
		autoExit:   true,
	}
}

// newHangingExecutor 造一个赖着不走的假进程：吐 chunks，但没人喊就一直不报退出。
// Stop() 的超时兜底用它。
func newHangingExecutor(chunks [][]byte, res executor.ExitResult) *fakeExecutor {
	f := newFakeExecutor(chunks, res)
	f.autoExit = false
	return f
}

func (f *fakeExecutor) Start(spec executor.LaunchSpec) error {
	if f.startErr != nil {
		return f.startErr
	}
	go func() {
		for _, c := range f.chunks {
			f.outCh <- c
		}
		close(f.outCh)
	}()
	if f.autoExit {
		go f.die()
	}
	return nil
}

// die 让假进程死掉。只能叫一次——第二次会 close 一个已关的 channel。
func (f *fakeExecutor) die() {
	f.procExitCh <- f.res
	close(f.procExitCh)
}

func (f *fakeExecutor) Output() <-chan []byte                     { return f.outCh }
func (f *fakeExecutor) ProcessExited() <-chan executor.ExitResult { return f.procExitCh }
func (f *fakeExecutor) Write(b []byte) (int, error)               { return len(b), nil }
func (f *fakeExecutor) Resize(cols, rows uint16) error            { return nil }

func (f *fakeExecutor) StopProcess() error {
	if f.stopErr != nil {
		return f.stopErr
	}
	if f.onStop != nil {
		f.onStop()
	}
	return nil
}

func (f *fakeExecutor) KillProcess() error {
	select {
	case f.killCalls <- struct{}{}:
	default:
	}
	if f.onKill != nil {
		f.onKill()
	}
	return nil
}

func (f *fakeExecutor) CloseTerminal() error {
	select {
	case f.closeCalls <- struct{}{}:
	default:
	}
	return nil
}

var _ executor.Executor = (*fakeExecutor)(nil)

// waitFor 轮询等条件成立，别用「睡一下再断言」。
// 排干工人和收尾工人是两条 goroutine，谁先跑完没有保证。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等 %s 超时", what)
}

// TestSessionDrainAndReap 拿假 Executor 测 Session 本身。
func TestSessionDrainAndReap(t *testing.T) {
	ex := newFakeExecutor([][]byte{[]byte("hel"), []byte("lo")}, executor.ExitResult{Code: 7})
	sink := newFakeSink()
	s := NewSession("s1", ex, sink)

	if err := s.Start(executor.LaunchSpec{}); err != nil {
		t.Fatalf("Start 返回错误: %v", err)
	}

	select {
	case res := <-sink.exitedCh:
		if res.Code != 7 {
			t.Errorf("退出码 = %d, 期望 7", res.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内 sink 没收到 OnProcessExited")
	}

	// 两批分两次到 sink，拼回来必须还是原样——顺序反了或丢了都会在这里露出来
	waitFor(t, "输出到齐", func() bool { return sink.output() == "hello" })

	select {
	case <-ex.closeCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("CloseTerminal 没被调用——终端资源会泄漏")
	}
}

// TestSessionWithConPTYExecutor 把 Session 和真的 ConPTYExecutor 接起来跑一遍。
// 上一个是零件测试，这个是整机测试——接口对不上、调用顺序错，只有这里会红。
func TestSessionWithConPTYExecutor(t *testing.T) {
	sink := newFakeSink()
	s := NewSession("s2", &conpty.ConPTYExecutor{}, sink)

	err := s.Start(executor.LaunchSpec{
		Path:    "cmd.exe",
		Command: `cmd.exe /d /s /c "echo hello-session"`,
	})
	if err != nil {
		t.Fatalf("Start 返回错误: %v", err)
	}

	select {
	case res := <-sink.exitedCh:
		if res.Err != nil {
			t.Fatalf("ProcessExited 带回错误: %v", res.Err)
		}
		if res.Code != 0 {
			t.Errorf("退出码 = %d, 期望 0", res.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内 sink 没收到 OnProcessExited")
	}

	waitFor(t, "hello-session 到 sink", func() bool {
		return strings.Contains(sink.output(), "hello-session")
	})
}

// Start 失败时不许放工人：进程都没起来，收尾工人会一直等一个永远不来的死讯。
func TestSessionStartFailureLeavesNoWorker(t *testing.T) {
	ex := newFakeExecutor(nil, executor.ExitResult{})
	ex.startErr = errors.New("boom")
	sink := newFakeSink()
	s := NewSession("s3", ex, sink)

	if err := s.Start(executor.LaunchSpec{}); err == nil {
		t.Fatal("Executor 起不来时 Start 应该返回 error")
	}

	select {
	case <-sink.exitedCh:
		t.Fatal("Start 失败后 sink 仍收到了 OnProcessExited——工人被放出去了")
	case <-time.After(200 * time.Millisecond):
	}
}

// 进程听话：\x03 一到就退。Stop() 该走 doneCh 那条路，不该动刀。
func TestSessionStopGraceful(t *testing.T) {
	ex := newHangingExecutor(nil, executor.ExitResult{Code: 0})
	ex.onStop = ex.die // 收到 \x03 就退
	sink := newFakeSink()
	s := NewSession("s4", ex, sink)
	if err := s.Start(executor.LaunchSpec{}); err != nil {
		t.Fatalf("Start 返回错误: %v", err)
	}

	s.Stop()

	select {
	case <-ex.killCalls:
		t.Fatal("进程自己退了，不该强杀")
	case <-time.After(200 * time.Millisecond):
	}
}

// 进程赖着不走：Stop() 得等满超时才动手。
//
// 断言「等满了」而不只是「动刀了」：没死讯就当死讯用（比如去读一个已经关掉的 channel），
// 会立刻拿到零值、立刻强杀 —— 那种写法在这里同样是绿的，但它没在兜底。
func TestSessionStopTimeoutKills(t *testing.T) {
	ex := newHangingExecutor(nil, executor.ExitResult{Code: 1})
	ex.onKill = ex.die // 只有被强杀才死
	sink := newFakeSink()
	s := NewSession("s5", ex, sink)
	if err := s.Start(executor.LaunchSpec{}); err != nil {
		t.Fatalf("Start 返回错误: %v", err)
	}

	start := time.Now()
	s.Stop()

	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Errorf("Stop() 只用了 %v —— 没等满超时就杀，说明「没死讯」被当成了「死讯」", elapsed)
	}
	select {
	case <-ex.killCalls:
	default:
		t.Fatal("超时后没有强杀——进程会一直挂着")
	}
}

// \x03 压根写不进去 = 进程没收到停止请求，没理由再白等两秒。
func TestSessionStopProcessErrorKillsNow(t *testing.T) {
	ex := newHangingExecutor(nil, executor.ExitResult{Code: 1})
	ex.stopErr = errors.New("pty is nil")
	ex.onKill = ex.die
	sink := newFakeSink()
	s := NewSession("s6", ex, sink)
	if err := s.Start(executor.LaunchSpec{}); err != nil {
		t.Fatalf("Start 返回错误: %v", err)
	}

	start := time.Now()
	s.Stop()

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Stop() 用了 %v —— StopProcess 报错后该立刻强杀，不该等超时", elapsed)
	}
	select {
	case <-ex.killCalls:
	default:
		t.Fatal("StopProcess 报错后没有强杀")
	}
}
