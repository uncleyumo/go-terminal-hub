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
