package exec

import (
	"os"
	"path/filepath"
	"testing"
)

// fixtures 在临时目录里造出测试用的文件，返回目录。
//
// 目录名故意带空格（"my tools"）——引号规则的全部意义就在带空格的路径上，
// 不带空格的路径怎么拼都跑得通，测了等于没测。
func fixtures(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "my tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"run.bat", "run.cmd", "run.ps1", "app.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestBuildCommand 的每条 want 都是 ARCHITECTURE.md §6 那张表逐字誊下来的，不是从实现反推的。
func TestBuildCommand(t *testing.T) {
	dir := fixtures(t)
	batPath := filepath.Join(dir, "run.bat")
	cmdPath := filepath.Join(dir, "run.cmd")
	ps1Path := filepath.Join(dir, "run.ps1")
	exePath := filepath.Join(dir, "app.exe")

	cases := []struct {
		name   string
		kind   string
		target string
		args   string
		want   string
	}{
		{
			name: "bat 有 args",
			kind: "bat", target: batPath, args: "--fast",
			want: `cmd.exe /d /s /c ""` + batPath + `" --fast"`,
		},
		{
			name: "bat 无 args",
			kind: "bat", target: batPath, args: "",
			want: `cmd.exe /d /s /c ""` + batPath + `""`,
		},
		{
			name: "cmd 有 args",
			kind: "cmd", target: cmdPath, args: "--fast",
			want: `cmd.exe /d /s /c ""` + cmdPath + `" --fast"`,
		},
		{
			name: "ps1 有 args",
			kind: "ps1", target: ps1Path, args: "--fast",
			want: `powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "` + ps1Path + `" --fast`,
		},
		{
			name: "ps1 无 args",
			kind: "ps1", target: ps1Path, args: "",
			want: `powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "` + ps1Path + `"`,
		},
		{
			name: "exe 有 args",
			kind: "exe", target: exePath, args: "--fast",
			want: `"` + exePath + `" --fast`,
		},
		{
			name: "exe 无 args",
			kind: "exe", target: exePath, args: "",
			want: `"` + exePath + `"`,
		},
		{
			// shell 的 Target 是命令不是文件，所以 os.Stat 被跳过，不需要 fixture
			name: "shell 原样丢给 cmd",
			kind: "shell", target: "dir /b", args: "",
			want: `cmd.exe /d /s /c dir /b`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := BuildCommand(Entry{Kind: c.kind, Target: c.target, Args: c.args})
			if err != nil {
				t.Fatalf("意外报错: %v", err)
			}
			if got != c.want {
				t.Errorf("拼出来的命令行不对\n  得到: %s\n  期望: %s", got, c.want)
			}
		})
	}
}

func TestBuildCommandUnknownKind(t *testing.T) {
	if _, err := BuildCommand(Entry{Kind: "python", Target: "x.py"}); err == nil {
		t.Error("Kind 不认识时应该返回 error，实际返回了 nil")
	}
}

func TestBuildCommandMissingTarget(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.bat")
	if _, err := BuildCommand(Entry{Kind: "bat", Target: missing}); err == nil {
		t.Error("Target 不存在时应该返回 error，实际返回了 nil")
	}
}
