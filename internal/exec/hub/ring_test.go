package hub

import (
	"bytes"
	"testing"
)

func TestRingSnapshotUnderCapacity(t *testing.T) {
	r := NewRing(64)
	r.Append([]byte("hello "))
	r.Append([]byte("world"))

	if got := r.Snapshot(); !bytes.Equal(got, []byte("hello world")) {
		t.Errorf("Snapshot = %q, want %q", got, "hello world")
	}
	if r.Total() != 11 {
		t.Errorf("Total = %d, want 11", r.Total())
	}
}

// 裁剪必须落在边界上：从中间砍会切碎转义序列，客户端渲染成乱码。
func TestRingTrimKeepsEscapeSequenceWhole(t *testing.T) {
	r := NewRing(10)
	r.Append([]byte("abc\x1b[31mred"))

	got := r.Snapshot()
	if len(got) > 10 {
		t.Fatalf("长度 %d 超过容量 10", len(got))
	}
	// 裁剪点应该被推到 ESC 上，所以内容以 ESC 开头
	if !bytes.HasPrefix(got, []byte("\x1b")) {
		t.Errorf("裁剪后应以 ESC 开头，实际 %q", got)
	}
	if !bytes.HasSuffix(got, []byte("red")) {
		t.Errorf("尾部不该被裁掉，实际 %q", got)
	}
}

// 裁剪不能把 UTF-8 码点切成两半。
func TestRingTrimDoesNotSplitRune(t *testing.T) {
	r := NewRing(6)
	r.Append([]byte("中文字符")) // 每个字 3 字节，共 12 字节

	got := r.Snapshot()
	for i, b := range got {
		if b&0xC0 == 0x80 && i == 0 {
			t.Fatalf("首个字节是 UTF-8 续字节，码点被切开了：%q", got)
		}
	}
	if len(got) > 6 {
		t.Errorf("长度 %d 超过容量 6", len(got))
	}
}

func TestRingSince(t *testing.T) {
	r := NewRing(64)
	r.Append([]byte("abc"))

	data, next, ok := r.Since(0)
	if !ok || !bytes.Equal(data, []byte("abc")) || next != 3 {
		t.Fatalf("Since(0) = (%q, %d, %v), want (abc, 3, true)", data, next, ok)
	}

	// 从游标 3 读：还没新数据
	data, next, ok = r.Since(next)
	if !ok || len(data) != 0 || next != 3 {
		t.Fatalf("Since(3) = (%q, %d, %v), want (, 3, true)", data, next, ok)
	}

	r.Append([]byte("def"))
	data, next, ok = r.Since(3)
	if !ok || !bytes.Equal(data, []byte("def")) || next != 6 {
		t.Fatalf("Since(3) = (%q, %d, %v), want (def, 6, true)", data, next, ok)
	}
}

// 游标落在环外（那段已经被裁掉）时必须报 ok=false，
// 让调用方改走 Snapshot 整段重发，而不是在错误的画面上续写。
func TestRingSinceStaleCursor(t *testing.T) {
	r := NewRing(4)
	r.Append([]byte("aaaaaaaa")) // total=8, 环里只有最后 4 字节

	if _, _, ok := r.Since(0); ok {
		t.Error("游标 0 已落在环外，应该返回 ok=false")
	}
	if _, _, ok := r.Since(99); ok {
		t.Error("游标超过 Total，应该返回 ok=false")
	}
}

func TestRingReset(t *testing.T) {
	r := NewRing(64)
	r.Append([]byte("old"))
	r.Reset()

	if got := r.Snapshot(); len(got) != 0 {
		t.Errorf("Reset 后 Snapshot 应为空，实际 %q", got)
	}
	if r.Total() != 0 {
		t.Errorf("Reset 后 Total 应为 0，实际 %d", r.Total())
	}
}
