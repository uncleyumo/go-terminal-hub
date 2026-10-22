package hub

import (
	"testing"
	"time"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
)

// TestRecordStatusCarriesPidAndTimes 状态里要带得上进程号和两个时刻。
//
// 界面上「运行时长」就是这么算的：
//   - 起始时刻是 0 = 这条会话根本没起来过
//   - 结束时刻是 0 = 还在跑
//
// 少了这两个时刻，界面就只能显示「在跑 / 不在跑」，没法回答
// 「它跑了多久」「它到底是没起来还是起来过」。
func TestRecordStatusCarriesPidAndTimes(t *testing.T) {
	storeInst, err := store.GetStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := storeInst.UpdateDataJson(nil); err != nil {
		t.Fatal(err)
	}

	id := "01930000-0000-7000-8000-0000000000a4"
	addSession(t, id, "ping -n 2 127.0.0.1")

	h := NewHub()
	sink := &testSink{exited: make(chan executor.ExitResult, 1)}
	if err := h.Start(id, sink); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Stop(id) }()

	status := h.ListRecordStatus()
	if len(status) != 1 {
		t.Fatalf("应该有 1 条记录，实际 %d 条", len(status))
	}
	st := status[0]
	if !st.Running {
		t.Fatal("刚起来就该是 Running")
	}
	if st.PID == 0 {
		t.Error("进程号是 0 —— 界面上没法把这条会话跟任务管理器里的进程对上号")
	}
	if st.StartedAt == 0 {
		t.Error("起始时刻是 0 —— 界面算不出运行时长")
	}
	if st.EndedAt != 0 {
		t.Error("还在跑，结束时刻不该有值")
	}

	// 等它自己跑完
	select {
	case <-sink.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("10s 内进程没退出")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status = h.ListRecordStatus()
		if len(status) == 1 && status[0].EndedAt != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(status) != 1 {
		t.Fatalf("应该有 1 条记录，实际 %d 条", len(status))
	}
	if status[0].EndedAt == 0 {
		t.Error("进程退出了，结束时刻还是 0")
	}
	if status[0].EndedAt < status[0].StartedAt {
		t.Errorf("结束时刻(%d) 早于起始时刻(%d)", status[0].EndedAt, status[0].StartedAt)
	}
}
