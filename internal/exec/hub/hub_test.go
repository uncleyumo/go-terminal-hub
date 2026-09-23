package hub

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
)

// store 是 sync.Once 单例，整个测试进程只有一个 data 目录。
// -count=N 会跑多轮，t.TempDir() 每轮都换 → 上一轮的目录已删，写 data.json 会报找不到路径。
// 所以目录在 TestMain 里建一次，跟着进程走。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hub-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("GO_TERMINAL_HUB_DIR", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type testSink struct {
	mu     sync.Mutex
	buf    []byte
	exited chan executor.ExitResult
}

func (s *testSink) OnOutput(chunk []byte) {
	s.mu.Lock()
	s.buf = append(s.buf, chunk...)
	s.mu.Unlock()
}

func (s *testSink) OnProcessExited(res executor.ExitResult) {
	s.exited <- res
}

// TestListRecordStatusAfterCleanExit 正常退出时 ExitResult.Err 是 nil。
// ListRecordStatus 对 nil 调 Err.Error() 会 panic——前端一拉列表就崩。
func TestListRecordStatusAfterCleanExit(t *testing.T) {
	storeInst, err := store.GetStore()
	if err != nil {
		t.Fatal(err)
	}
	// 多轮跑会累积旧数据，先清空
	if err := storeInst.UpdateDataJson(nil); err != nil {
		t.Fatal(err)
	}
	// Add 会自己生成 UUIDv7 当 ID，所以从 List 里取真实 ID
	if err := storeInst.Add(store.DataStore{
		Name:   "clean exit",
		Kind:   "shell",
		Target: "echo hi",
		Mode:   "terminal",
	}); err != nil {
		t.Fatal(err)
	}
	list := storeInst.List()
	if len(list) != 1 {
		t.Fatalf("store 里应该有 1 条，实际 %d", len(list))
	}
	id := list[0].ID

	sink := &testSink{exited: make(chan executor.ExitResult, 1)}
	h := NewHub()
	if err := h.Start(id, sink); err != nil {
		t.Fatal(err)
	}

	select {
	case res := <-sink.exited:
		if res.Err != nil {
			t.Fatalf("进程应该正常退出，实际 Err=%v", res.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内没等到 OnProcessExited")
	}

	// 这里之前会 panic
	status := h.ListRecordStatus()
	if len(status) != 1 {
		t.Fatalf("应该有 1 条记录，实际 %d", len(status))
	}
	if status[0].Running {
		t.Error("进程已退出，Running 应该是 false")
	}
	if status[0].ErrMsg != "" {
		t.Errorf("正常退出 ErrMsg 应该是空串，实际 %q", status[0].ErrMsg)
	}
	if status[0].ExitCode != 0 {
		t.Errorf("退出码应该是 0，实际 %d", status[0].ExitCode)
	}
}
