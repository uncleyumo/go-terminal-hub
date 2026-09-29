package executor

import (
	"os"
	"path/filepath"
	"strings"
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
//
// 两条不变量：
//   - Path    = 该行的第一个词，即 CreateProcess 要加载哪个 exe
//   - CmdLine = §6 表里那一整行，含开头的程序名
//
// 为什么 CmdLine 必须含程序名：Windows 交给新进程的只有一个字符串，怎么拆是那个程序
// 自己的事。cmd.exe / powershell.exe 是扫着读的（找 /c、找 -File），不含名字也能跑；
// 但普通 exe 按位置读，第一个词就是它自己的名字——少了名字，参数全体错位，且零报错。
//
// wantPath 只比**文件名**，不比完整路径：BuildCommand 用的是 exec.LookPath，
// 它返回的是系统自己解析出来的绝对路径（前缀 C:\Windows\System32\ 之类）。
// 那个前缀归操作系统管，不是这里要定的事，这里要定的只是「挑的是哪个 exe」。
func TestBuildCommand(t *testing.T) {
	dir := fixtures(t)
	batPath := filepath.Join(dir, "run.bat")
	cmdPath := filepath.Join(dir, "run.cmd")
	ps1Path := filepath.Join(dir, "run.ps1")
	exePath := filepath.Join(dir, "app.exe")

	cases := []struct {
		name     string
		kind     string
		target   string
		args     string
		wantPath string
		want     string
	}{
		{
			name: "bat 有 args",
			kind: "bat", target: batPath, args: "--fast",
			wantPath: "cmd.exe",
			want:     `cmd.exe /d /s /c ""` + batPath + `" --fast"`,
		},
		{
			name: "bat 无 args",
			kind: "bat", target: batPath, args: "",
			wantPath: "cmd.exe",
			want:     `cmd.exe /d /s /c ""` + batPath + `""`,
		},
		{
			name: "cmd 有 args",
			kind: "cmd", target: cmdPath, args: "--fast",
			wantPath: "cmd.exe",
			want:     `cmd.exe /d /s /c ""` + cmdPath + `" --fast"`,
		},
		{
			name: "ps1 有 args",
			kind: "ps1", target: ps1Path, args: "--fast",
			wantPath: "powershell.exe",
			want:     `powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "` + ps1Path + `" --fast`,
		},
		{
			name: "ps1 无 args",
			kind: "ps1", target: ps1Path, args: "",
			wantPath: "powershell.exe",
			want:     `powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "` + ps1Path + `"`,
		},
		{
			// 路径带空格，所以这对引号不是装饰：少了它，argv 会在空格处断成两截。
			// 实测（2026-09-19）：CmdLine 不带引号时 argv[0] 只到 "\my"，"--fast" 被挤到 argv[2]。
			name: "exe 有 args",
			kind: "exe", target: exePath, args: "--fast",
			wantPath: "app.exe",
			want:     `"` + exePath + `" --fast`,
		},
		{
			name: "exe 无 args",
			kind: "exe", target: exePath, args: "",
			wantPath: "app.exe",
			want:     `"` + exePath + `"`,
		},
		{
			// shell 的 Target 是命令不是文件，所以 os.Stat 被跳过，不需要 fixture
			name: "shell 原样丢给 cmd",
			kind: "shell", target: "dir /b", args: "",
			wantPath: "cmd.exe",
			want:     `cmd.exe /d /s /c dir /b`,
		},
		// —— 常驻终端 ——
		// 这两个 kind 的 target 是**选填**的：空 = 开个空终端等敲，填了 = 先跑它再停在提示符。
		// 下面四条 want 是在 Windows 上对着真 cmd.exe / powershell.exe 跑出来的，
		// 不是从实现反推的。
		{
			// 空 target：光一个 /k，后面没有命令，cmd 起完就停在提示符
			name: "terminal-cmd 空 target",
			kind: "terminal-cmd", target: "", args: "",
			wantPath: "cmd.exe",
			want:     `cmd.exe /k`,
		},
		{
			// 填了 target：跑完它，但**不退出**。这里挡着的是 /k 放错位置 ——
			// 写成 /k /d /s /c 的话，/k 会把后面整行当命令执行，报「/d 不是命令」。
			name: "terminal-cmd 有 target",
			kind: "terminal-cmd", target: batPath, args: "",
			wantPath: "cmd.exe",
			want:     `cmd.exe /d /s /k ""` + batPath + `""`,
		},
		{
			// PowerShell 那边对应 cmd 的 /k 的是 -NoExit（没有 -Command 时不需要它，
			// 带上也没坏处，这里跟有 target 的那条保持同一个骨架）
			name: "terminal-powershell 空 target",
			kind: "terminal-powershell", target: "", args: "",
			wantPath: "powershell.exe",
			want:     `powershell.exe -NoLogo -NoProfile`,
		},
		{
			// -NoExit 是关键：没有它，-Command 跑完就退出了，不是「常驻终端」
			name: "terminal-powershell 有 target",
			kind: "terminal-powershell", target: "Get-Date", args: "",
			wantPath: "powershell.exe",
			want:     `powershell.exe -NoLogo -NoProfile -NoExit -Command "Get-Date"`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := BuildCommand(Entry{Kind: c.kind, Target: c.target, Args: c.args})
			if err != nil {
				t.Fatalf("意外报错: %v", err)
			}
			if filepath.Base(got.Path) != c.wantPath {
				t.Errorf("Path 不对\n  得到: %s\n  期望（文件名）: %s", got.Path, c.wantPath)
			}
			if got.CmdLine != c.want {
				t.Errorf("拼出来的命令行不对\n  得到: %s\n  期望: %s", got.CmdLine, c.want)
			}
		})
	}
}

func TestBuildCommandUnknownKind(t *testing.T) {
	_, err := BuildCommand(Entry{Kind: "python", Target: "x.py"})
	if err == nil {
		t.Fatal("Kind 不认识时应该返回 error，实际返回了 nil")
	}
	// Kind 校验挡在 os.Stat 前面：这里的 Target 也不存在，但报的必须是 Kind 的错。
	// 否则 Kind 打错的人会收到"脚本不存在"，照着去查文件，方向直接跑偏。
	//
	// 所以这里只断言「报的不是 target 的错」——**不挑具体措辞**。
	// 早先这行是 strings.Contains(err.Error(), "bat")，逼着实现必须把支持的类型
	// 一个个抄进错误信息里：加一个 kind 就要记得改句子，忘了没人拦得住，只有测试红。
	// 措辞是给人看的，不是接口。
	if strings.Contains(err.Error(), "target is missing") {
		t.Errorf("应该报 Kind 不认识，却报了 target 的问题，方向会跑偏: %v", err)
	}
}

func TestBuildCommandMissingTarget(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.bat")
	if _, err := BuildCommand(Entry{Kind: "bat", Target: missing}); err == nil {
		t.Error("Target 不存在时应该返回 error，实际返回了 nil")
	}
}
