package paste

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 1×1 的透明 PNG，够短又能过文件头判断
var pngBytes = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
}

func withTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GO_TERMINAL_HUB_DIR", dir)
	return filepath.Join(dir, "paste")
}

func TestSaveWritesFileWithRealExtension(t *testing.T) {
	dir := withTempDir(t)

	path, err := Save(pngBytes)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	// 后缀必须是 .png：claude code 是按后缀认图片的，存成 .tmp 就等于白存
	if !strings.HasSuffix(path, ".png") {
		t.Fatalf("path = %q, want .png suffix", path)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("path = %q, want it under %q", path, dir)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(pngBytes) {
		t.Fatal("写回去的字节和原来不一样")
	}
}

func TestSaveRejectsNonImage(t *testing.T) {
	withTempDir(t)

	if _, err := Save([]byte("#!/bin/sh\nrm -rf /\n")); err == nil {
		t.Fatal("普通文本被当成图片存下来了")
	}
	if _, err := Save(nil); err == nil {
		t.Fatal("空数据被存下来了")
	}
}

func TestSaveRejectsOversize(t *testing.T) {
	withTempDir(t)

	big := make([]byte, MaxBytes+1)
	copy(big, pngBytes)
	if _, err := Save(big); err == nil {
		t.Fatal("超过上限的图片被存下来了")
	}
}

func TestSaveGivesDistinctPaths(t *testing.T) {
	withTempDir(t)

	first, err := Save(pngBytes)
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}
	second, err := Save(pngBytes)
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	// 同一秒内连发两张也不能互相覆盖
	if first == second {
		t.Fatalf("两次保存拿到同一个路径 %q", first)
	}
}

func TestStatAndClear(t *testing.T) {
	withTempDir(t)

	// 目录还不存在时，Stat 该当空的处理，不该报错
	info, err := Stat()
	if err != nil {
		t.Fatalf("Stat on missing dir: %v", err)
	}
	if info.Files != 0 || info.Bytes != 0 {
		t.Fatalf("info = %+v, want zero", info)
	}

	for i := 0; i < 3; i++ {
		if _, err := Save(pngBytes); err != nil {
			t.Fatalf("Save #%d: %v", i, err)
		}
	}

	info, err = Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Files != 3 || info.Bytes != int64(3*len(pngBytes)) {
		t.Fatalf("info = %+v, want 3 files / %d bytes", info, 3*len(pngBytes))
	}

	removed, err := Clear()
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if removed.Files != 3 || removed.Bytes != int64(3*len(pngBytes)) {
		t.Fatalf("removed = %+v, want 3 files / %d bytes", removed, 3*len(pngBytes))
	}

	info, err = Stat()
	if err != nil {
		t.Fatalf("Stat after Clear: %v", err)
	}
	if info.Files != 0 {
		t.Fatalf("清完还剩 %d 个文件", info.Files)
	}
}
