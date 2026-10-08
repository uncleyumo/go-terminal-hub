package hub

import (
	"errors"
	"testing"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/session"
)

// fakeExecutor 是 Executor 的替身，只记下收到的写入和尺寸。
// 用它测租约闸门，不涉及真进程。
type fakeExecutor struct {
	writes  [][]byte
	resizes [][2]uint16
}

func (f *fakeExecutor) Start(spec executor.LaunchSpec) error { return nil }
func (f *fakeExecutor) Output() <-chan []byte                { return make(chan []byte) }
func (f *fakeExecutor) ProcessExited() <-chan executor.ExitResult {
	return make(chan executor.ExitResult)
}
func (f *fakeExecutor) Write(b []byte) (int, error) {
	f.writes = append(f.writes, append([]byte(nil), b...))
	return len(b), nil
}
func (f *fakeExecutor) Resize(cols, rows uint16) error {
	f.resizes = append(f.resizes, [2]uint16{cols, rows})
	return nil
}
func (f *fakeExecutor) StopProcess() error   { return nil }
func (f *fakeExecutor) CloseTerminal() error { return nil }
func (f *fakeExecutor) KillProcess() error   { return nil }
func (f *fakeExecutor) GetLaunchSpec() executor.LaunchSpec {
	return executor.LaunchSpec{}
}

// newLeaseTestHub 造一个只有一条记录、还没启动进程的 Hub。
func newLeaseTestHub(id string, cols, rows uint16) (*Hub, *fakeExecutor) {
	ex := &fakeExecutor{}
	h := NewHub()
	h.records[id] = &Record{
		sess:  session.NewSession(id, ex, &fakeSink{}),
		lease: SourceDesktop,
		sizes: map[string][2]uint16{SourceDesktop: {cols, rows}},
	}
	return h, ex
}

// 控制方写的输入要能到达 executor。
func TestWriteFromControllerReachesExecutor(t *testing.T) {
	h, ex := newLeaseTestHub("s1", 80, 25)

	n, err := h.WriteFrom("s1", SourceDesktop, []byte("hi"))
	if err != nil {
		t.Fatalf("控制方写入不该报错: %v", err)
	}
	if n != 2 || len(ex.writes) != 1 || string(ex.writes[0]) != "hi" {
		t.Errorf("executor 收到 %q, want %q", ex.writes, "hi")
	}
}

// 旁观方写输入必须被拒——这是防止两个端同时往终端里打字。
func TestWriteFromNonControllerRejected(t *testing.T) {
	h, ex := newLeaseTestHub("s1", 80, 25)

	_, err := h.WriteFrom("s1", SourceWeb, []byte("nope"))
	if !errors.Is(err, ErrNotController) {
		t.Fatalf("旁观方写入应当返回 ErrNotController，实际 %v", err)
	}
	if len(ex.writes) != 0 {
		t.Errorf("被拒的写入不该到达 executor，实际 %q", ex.writes)
	}
}

// 非控制方的 resize 要「记下但不下发」：接管时得用它切到对方上次的尺寸。
func TestResizeFromNonControllerRecordsButDoesNotApply(t *testing.T) {
	h, ex := newLeaseTestHub("s1", 200, 50)

	err := h.ResizeFrom("s1", SourceWeb, 48, 20)
	if !errors.Is(err, ErrNotController) {
		t.Fatalf("旁观方 resize 应当返回 ErrNotController，实际 %v", err)
	}
	if len(ex.resizes) != 0 {
		t.Errorf("旁观方的 resize 不该下发到 PTY，实际 %v", ex.resizes)
	}

	// 但尺寸要记住了：接管时切过去
	if err := h.SetController("s1", SourceWeb, 0, 0); err != nil {
		t.Fatal(err)
	}
	if len(ex.resizes) != 1 || ex.resizes[0] != [2]uint16{48, 20} {
		t.Errorf("接管后 PTY 尺寸 = %v, want [[48 20]]", ex.resizes)
	}
}

// 控制权换人时 PTY 跟着换尺寸；交还时切回桌面上次的尺寸。
func TestSetControllerSwitchesPTYSize(t *testing.T) {
	h, ex := newLeaseTestHub("s1", 200, 50)

	// 手机接管（尺寸是它 attach 时报的）
	if err := h.ResizeFrom("s1", SourceWeb, 48, 20); !errors.Is(err, ErrNotController) {
		t.Fatalf("期望 ErrNotController，实际 %v", err)
	}
	if err := h.SetController("s1", SourceWeb, 0, 0); err != nil {
		t.Fatal(err)
	}
	if who, _ := h.Controller("s1"); who != SourceWeb {
		t.Errorf("控制方 = %q, want %q", who, SourceWeb)
	}

	// 桌面收回控制，带上它恢复布局后量出的尺寸
	if err := h.SetController("s1", SourceDesktop, 200, 50); err != nil {
		t.Fatal(err)
	}
	if who, _ := h.Controller("s1"); who != SourceDesktop {
		t.Errorf("控制方 = %q, want %q", who, SourceDesktop)
	}

	want := [][2]uint16{{48, 20}, {200, 50}}
	if len(ex.resizes) != len(want) {
		t.Fatalf("resize 次数 = %d, want %d (%v)", len(ex.resizes), len(want), ex.resizes)
	}
	for i := range want {
		if ex.resizes[i] != want[i] {
			t.Errorf("第 %d 次 resize = %v, want %v", i, ex.resizes[i], want[i])
		}
	}
}

// 控制权换人后，写入的许可也跟着换。
func TestWritePermissionFollowsLease(t *testing.T) {
	h, _ := newLeaseTestHub("s1", 80, 25)

	if err := h.SetController("s1", SourceWeb, 48, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := h.WriteFrom("s1", SourceDesktop, []byte("x")); !errors.Is(err, ErrNotController) {
		t.Errorf("换人后桌面写入应被拒，实际 %v", err)
	}
	if _, err := h.WriteFrom("s1", SourceWeb, []byte("x")); err != nil {
		t.Errorf("换人后 web 写入应当成功，实际 %v", err)
	}
}

// 没有记录时报错而不是 panic。
func TestLeaseUnknownSession(t *testing.T) {
	h, _ := newLeaseTestHub("s1", 80, 25)

	if _, err := h.WriteFrom("nope", SourceDesktop, []byte("x")); err == nil {
		t.Error("未知会话应当报错")
	}
	if _, ok := h.Controller("nope"); ok {
		t.Error("未知会话的 Controller 应当返回 false")
	}
	if err := h.SetController("nope", SourceWeb, 0, 0); err == nil {
		t.Error("未知会话的 SetController 应当报错")
	}
}
