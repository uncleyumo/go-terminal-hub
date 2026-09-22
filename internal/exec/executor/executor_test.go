package executor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/conpty"
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
			wantPath: exePath,
			want:     `"` + exePath + `" --fast`,
		},
		{
			name: "exe 无 args",
			kind: "exe", target: exePath, args: "",
			wantPath: exePath,
			want:     `"` + exePath + `"`,
		},
		{
			// shell 的 Target 是命令不是文件，所以 os.Stat 被跳过，不需要 fixture
			name: "shell 原样丢给 cmd",
			kind: "shell", target: "dir /b", args: "",
			wantPath: "cmd.exe",
			want:     `cmd.exe /d /s /c dir /b`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := BuildCommand(Entry{Kind: c.kind, Target: c.target, Args: c.args})
			if err != nil {
				t.Fatalf("意外报错: %v", err)
			}
			if got.Path != c.wantPath {
				t.Errorf("Path 不对\n  得到: %s\n  期望: %s", got.Path, c.wantPath)
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
	// Kind 校验挡在 os.Stat 前面：这里的路径也不存在，但报的必须是 Kind 的错。
	// 否则 Kind 打错的人会收到"脚本不存在"，照着去查文件，方向直接跑偏。
	if !strings.Contains(err.Error(), "bat") {
		t.Errorf("应该报 Kind 不支持，实际: %v", err)
	}
}

func TestBuildCommandMissingTarget(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.bat")
	if _, err := BuildCommand(Entry{Kind: "bat", Target: missing}); err == nil {
		t.Error("Target 不存在时应该返回 error，实际返回了 nil")
	}
}

// TestConPTYExecutorStart 走完一个 Executor 的完整生命周期：
// Start → 排干 Output() → ProcessExited() → CloseTerminal() → Output() 关闭。
//
// ⚠️ 这个测试**不许**碰 c.cmd.Wait()。Wait 归 Executor 内部的 Wait goroutine 独占：
// go-pty 的 wait() 在 defer 里做 `sys.done <- nil`，那个 channel 容量只有 1
// 且只在设了 ctx 时才有读者（cmd_windows.go:100 / :173 / :203）。
// 并发第二次调 Wait() 会永久卡死在那句上，表现是 flaky 挂起，不是报错。
func TestConPTYExecutorStart(t *testing.T) {
	c := &conpty.ConPTYExecutor{}
	err := c.Start(LaunchSpec{
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
