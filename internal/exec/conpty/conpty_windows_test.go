package conpty

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
)

// TestConPTYExecutorStart 走完一个 Executor 的完整生命周期：
// Start → 排干 Output() → ProcessExited() → CloseTerminal() → Output() 关闭。
//
// ⚠️ 这个测试**不许**碰 c.cmd.Wait()。Wait 归 Executor 内部的 Wait goroutine 独占：
// go-pty 的 wait() 在 defer 里做 `sys.done <- nil`，那个 channel 容量只有 1
// 且只在设了 ctx 时才有读者（cmd_windows.go:100 / :173 / :203）。
// 并发第二次调 Wait() 会永久卡死在那句上，表现是 flaky 挂起，不是报错。
func TestConPTYExecutorStart(t *testing.T) {
	c := &ConPTYExecutor{}
	err := c.Start(executor.LaunchSpec{
		Path:    "cmd.exe",
		Command: `cmd.exe /d /s /c "echo hello-conpty"`,
	})
	if err != nil {
		t.Fatalf("Start 返回错误: %v", err)
	}
	if c.cmd == nil {
		t.Fatal("Start 成功后 c.cmd 仍是 nil")
	}

	// 排干必须一直在跑：读循环往无缓冲 channel 送，没人收就永远堵在 send 上，
	// CloseTerminal() 的第二步（<-readDoneCh）会跟着一起挂。
	var mu sync.Mutex
	var got []byte
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for chunk := range c.Output() {
			mu.Lock()
			got = append(got, chunk...)
			mu.Unlock()
		}
	}()

	// 进程时钟。到货只说明进程死了——终端还活着、读循环还卡在 Read。
	select {
	case res := <-c.ProcessExited():
		if res.Err != nil {
			t.Fatalf("ProcessExited 带回错误: %v", res.Err)
		}
		if res.Code != 0 {
			t.Errorf("退出码 = %d, 期望 0", res.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内没等到 ProcessExited")
	}

	// 终端时钟。四步：关会话 → 等读循环排干 → 关管道 → 丢 term 引用。
	if err := c.CloseTerminal(); err != nil {
		t.Fatalf("CloseTerminal 返回错误: %v", err)
	}

	// 读时钟。Output() 关闭 == 读循环已排干并退出。
	// CloseTerminal() 返回时这一步就该成立——不成立说明第二步等错了东西。
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("CloseTerminal 返回后 Output() 仍未关闭——读循环没退出")
	}

	mu.Lock()
	defer mu.Unlock()
	if !bytes.Contains(got, []byte("hello-conpty")) {
		t.Errorf("输出里没找到 hello-conpty，收到的是 %q", got)
	}
}

// ---- 并发与幂等 ----
//
// race detector 在本机不可用（cgo 编译不了），所以断言靠超时和行为：
// 死锁 = 超时；空指针 = panic（测试直接挂红）。

// within 保证 f 在时限内返回。死锁的表现是永久挂起，不是报错。
// f 里只许 t.Errorf，不许 t.Fatal（t.Fatal 只结束当前 goroutine）。
func within(t *testing.T, name string, timeout time.Duration, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("%s 超过 %v 没返回——多半是锁没还，死锁了", name, timeout)
	}
}

// drain 排干 Output()。不排干的话读循环会堵在发送上，CloseTerminal 的 <-readDoneCh 跟着一起挂。
func drain(c *ConPTYExecutor) (readAll func() []byte, drained chan struct{}) {
	var mu sync.Mutex
	var buf []byte
	drained = make(chan struct{})
	go func() {
		defer close(drained)
		for chunk := range c.Output() {
			mu.Lock()
			buf = append(buf, chunk...)
			mu.Unlock()
		}
	}()
	return func() []byte {
		mu.Lock()
		defer mu.Unlock()
		return buf
	}, drained
}

func startEcho(t *testing.T) *ConPTYExecutor {
	t.Helper()
	c := &ConPTYExecutor{}
	if err := c.Start(executor.LaunchSpec{
		Path:    "cmd.exe",
		Command: `cmd.exe /d /s /c "echo hi-conpty"`,
	}); err != nil {
		t.Fatalf("Start 返回错误: %v", err)
	}
	return c
}

func waitExit(t *testing.T, c *ConPTYExecutor) executor.ExitResult {
	t.Helper()
	select {
	case res := <-c.ProcessExited():
		return res
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内没等到 ProcessExited")
		return executor.ExitResult{}
	}
}

// 未 Start 就 CloseTerminal：term 是 nil，必须立刻返回，不能卡在锁上。
// 这条专门盯「term == nil 分支不放锁」。
func TestCloseTerminalBeforeStart(t *testing.T) {
	c := &ConPTYExecutor{}
	var closeErr error
	within(t, "CloseTerminal（未 Start）", 2*time.Second, func() {
		closeErr = c.CloseTerminal()
	})
	if closeErr != nil {
		t.Errorf("未 Start 时 CloseTerminal 应该返回 nil，实际 %v", closeErr)
	}
}

// 连续两次 CloseTerminal：第二次走 term == nil 分支，必须返回而不是死锁。
func TestCloseTerminalTwice(t *testing.T) {
	c := startEcho(t)
	_, drained := drain(c)
	waitExit(t, c)

	within(t, "第一次 CloseTerminal", 5*time.Second, func() {
		if err := c.CloseTerminal(); err != nil {
			t.Errorf("第一次 CloseTerminal 报错: %v", err)
		}
	})
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("CloseTerminal 返回后 Output() 仍未关闭")
	}

	within(t, "第二次 CloseTerminal", 2*time.Second, func() {
		if err := c.CloseTerminal(); err != nil {
			t.Errorf("第二次 CloseTerminal 应该幂等返回 nil，实际 %v", err)
		}
	})
}

// 8 个 goroutine 同时 CloseTerminal：只许第一个干活，其余走 term == nil。
// 全体必须返回，不许双重关句柄、不许死锁。
func TestConcurrentCloseTerminal(t *testing.T) {
	c := startEcho(t)
	_, drained := drain(c)
	waitExit(t, c)

	within(t, "并发 CloseTerminal", 5*time.Second, func() {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = c.CloseTerminal()
			}()
		}
		wg.Wait()
	})
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("并发 CloseTerminal 后 Output() 仍未关闭")
	}
}

// 关掉之后再写：必须返回 error，不许空指针。
func TestWriteAfterCloseTerminal(t *testing.T) {
	c := startEcho(t)
	_, drained := drain(c)
	waitExit(t, c)

	within(t, "CloseTerminal", 5*time.Second, func() {
		_ = c.CloseTerminal()
	})
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("CloseTerminal 返回后 Output() 仍未关闭")
	}

	if _, err := c.Write([]byte("x")); err == nil {
		t.Error("CloseTerminal 之后 Write 应该报错")
	}
	if err := c.Resize(80, 25); err == nil {
		t.Error("CloseTerminal 之后 Resize 应该报错")
	}
	if err := c.StopProcess(); err == nil {
		t.Error("CloseTerminal 之后 StopProcess 应该报错")
	}
}

// Start 失败之后 KillProcess 必须还能跑。
// Start 提前 return 时如果 startMu 没还，兜底就永久卡住——兜底失效等于没有兜底。
func TestKillProcessAfterStartFailure(t *testing.T) {
	c := &ConPTYExecutor{}
	err := c.Start(executor.LaunchSpec{
		Path:    `C:\no\such\binary.exe`,
		Command: `C:\no\such\binary.exe`,
	})
	if err == nil {
		t.Fatal("不存在的 exe 应该 Start 失败")
	}
	within(t, "KillProcess（Start 失败后）", 2*time.Second, func() {
		_ = c.KillProcess()
	})
}

// Write 和 CloseTerminal 撞在一起。用交互式 cmd.exe，进程不会立刻退。
// 若 Write 在放锁后还去取 c.term（而不是用复制的局部变量），CloseTerminal 把 term 置空时会空指针。
//
// ⚠️ 写方**故意不 join**：PTY 输入写满了会一直阻塞在 t.Write 上，那是 go-pty 的性质，不是锁的 bug。
// 这里只断言两件事——CloseTerminal 必须返回；关掉之后的 Write 必须立刻返回。
func TestConcurrentWriteAndCloseTerminal(t *testing.T) {
	c := &ConPTYExecutor{}
	if err := c.Start(executor.LaunchSpec{
		Path:    "cmd.exe",
		Command: `cmd.exe`,
	}); err != nil {
		t.Fatalf("Start 返回错误: %v", err)
	}
	_, drained := drain(c)

	for i := 0; i < 4; i++ {
		go func() {
			for j := 0; j < 20; j++ {
				_, _ = c.Write([]byte("echo hello\r"))
			}
		}()
	}

	within(t, "CloseTerminal", 5*time.Second, func() {
		_ = c.CloseTerminal()
	})

	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("收尾后 Output() 仍未关闭")
	}

	// 关掉之后必须立刻有结论，不许卡住
	within(t, "Write after close", 2*time.Second, func() {
		if _, err := c.Write([]byte("x")); err == nil {
			t.Error("CloseTerminal 之后 Write 应该报错")
		}
	})
	within(t, "收尾后的 CloseTerminal", 2*time.Second, func() {
		_ = c.CloseTerminal()
	})
}
