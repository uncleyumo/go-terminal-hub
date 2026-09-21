// Package exec 是执行核心：把一个 Entry 变成一条真正跑起来的进程，并管住它的输入输出。
//
// 三层概念（ARCHITECTURE.md §3）在这里的落点：
//
//	Entry    —— 用户存的预设配置。本文件里只是数据。
//	Executor —— 执行后端，一个能起进程、能读写、能停掉它的东西。本文件里是接口。
//	Session  —— 一次运行实例。不在本文件，M2 第三步。
//
// 三个生命周期完全不同，别混着写。
package exec

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// Entry 是一条预设配置（将来存进 %USERPROFILE%\.go-terminal-hub\data.json）。
type Entry struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`   // bat | cmd | ps1 | exe | shell
	Target    string   `json:"target"` // 脚本路径 或 可执行文件路径
	Args      string   `json:"args"`   // 原始字符串，绝不 split
	WorkDir   string   `json:"workDir"`
	Env       []string `json:"env"`      // KEY=VALUE
	Mode      string   `json:"mode"`     // terminal | log
	Encoding  string   `json:"encoding"` // auto | utf8 | gbk，仅日志模式用
	Cols      uint16   `json:"cols"`
	Rows      uint16   `json:"rows"`
	AutoStart bool     `json:"autoStart"`
}

type Command struct {
	Path    string // 要启动的 exe
	CmdLine string // 完整命令行
}

// BuildCommand 把 Entry 翻译成一条完整命令行。
// Kind 不认识时返回 error。
func BuildCommand(e Entry) (Command, error) {
	if e.Kind != "bat" && e.Kind != "cmd" && e.Kind != "ps1" && e.Kind != "exe" && e.Kind != "shell" {
		slog.Info("script formats other than bat / cmd / ps1 / exe / shell are not supported for the time being", "e.Kind", e.Kind)
		return Command{}, errors.New("script formats other than bat / cmd / ps1 / exe / shell are not supported for the time being")
	}

	// 查看 Target 脚本是否存在
	if _, err := os.Stat(e.Target); err != nil {
		if e.Kind != "shell" {
			slog.Info("target is missing", "e.Target", e.Target)
			return Command{}, errors.New("target is missing")
		}
	}
	cmdLine := ""
	switch e.Kind {
	case "bat", "cmd":
		if e.Args == "" {
			// example: cmd.exe /d /s /c ""C:\my tools\run.bat""
			cmdLine = fmt.Sprintf("cmd.exe /d /s /c \"\"%s\"\"", e.Target)
		} else {
			// example: cmd.exe /d /s /c ""C:\my tools\run.bat" --fast"
			cmdLine = fmt.Sprintf("cmd.exe /d /s /c \"\"%s\" %s\"", e.Target, e.Args)
		}
		return Command{Path: "cmd.exe", CmdLine: cmdLine}, nil
	case "ps1":
		args := ""
		if e.Args == "" {
			args = ""
		} else {
			args = " " + e.Args
		}
		// example: powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "C:\my tools\run.ps1" --fast
		cmdLine = fmt.Sprintf("powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File \"%s\"%s", e.Target, args)
		return Command{Path: "powershell.exe", CmdLine: cmdLine}, nil
	case "exe":
		args := ""
		if e.Args == "" {
			args = ""
		} else {
			args = " " + e.Args
		}
		cmdLine = fmt.Sprintf("\"%s\"%s", e.Target, args)
		return Command{Path: e.Target, CmdLine: cmdLine}, nil
	case "shell":
		cmdLine = fmt.Sprintf("cmd.exe /d /s /c %s", e.Target)
		return Command{Path: "cmd.exe", CmdLine: cmdLine}, nil
	}
	return Command{}, errors.New("unknown Kind")
}

// LaunchSpec 是「起一次会话」需要的全部输入。
//
// 它已经是拼好的结果——Entry 到它的翻译在 BuildCommand 里做完了。
// 这里没有 Kind、没有 Mode：Kind 是拼命令行时才用的中间物；
// Mode 决定用哪个 Executor 实现，是上层选实现的事，不是 Executor 的输入。
type LaunchSpec struct {
	Path    string   // 要启动的 exe
	Command string   // 完整命令行
	WorkDir string   // 空 = 当前目录
	Env     []string // KEY=VALUE；nil = 继承当前进程
	Cols    uint16   // 初始列数；0 = 用默认 80
	Rows    uint16   // 初始行数；0 = 用默认 25
}

// ExitResult 是会话结束的最终结果。
type ExitResult struct {
	Code int   // 退出码
	Err  error // 非正常结束的原因；正常退出为 nil
}

// Executor 是一个执行后端。
//
// 两个实现：ConPTYExecutor（terminal 模式，M2 第二步）、PipeExecutor（log 模式，M4）。
//
// 生命周期：
//
//	Start(spec)
//	  → 持续消费 Output() 和 ProcessExited() 两个 channel（各起一个 goroutine）
//	  → 进程自然退出，或用户调 StopProcess() 请求停止
//	  → ProcessExited() 收到 ExitResult
//	  → CloseTerminal() 收尾
//
// 为什么读循环放在 Executor 内部，而不是暴露一个 io.Reader 让上层自己读：
// 收尾流程「关会话 → 等读循环排干 → 关管道」是 ConPTY 专属知识（ARCHITECTURE.md §5）。
// 读循环一旦归上层，这套知识就漏进 Session，M4 的管道模式还得让 Session 再学一套。
type Executor interface {
	// Start 起进程。返回 nil 只代表创建成功，不代表进程跑起来了——死活看 ProcessExited()。
	Start(spec LaunchSpec) error

	// Output 是终端输出流，由 Executor 内部的读循环写入。
	// channel 关闭 == 读循环已排干并退出。这就是 M1 要的那个「排干」握手信号。
	//
	// ⚠️ 收尾时必须一直读到它关闭为止：读循环可能正阻塞在发送上，中途停读会死锁。
	Output() <-chan []byte

	// ProcessExited 在进程退出时收到恰好一个值，随后关闭。
	ProcessExited() <-chan ExitResult

	// Write 往终端写输入，等于在键盘上打字。
	Write(b []byte) (int, error)

	// Resize 改终端尺寸，参数顺序是（列, 行）。
	// 初始尺寸走 LaunchSpec，这里只管运行中的改动。
	Resize(cols, rows uint16) error

	// StopProcess 请求停止进程：往终端输入写 \x03（Ctrl-C），随即返回。幂等。
	//
	// 是请求不是保证——程序可以不理。超时与强杀兜底归调用方，不在这里。
	// 不负责收尾。进程真死了 ProcessExited() 会收到值。
	StopProcess() error

	// CloseTerminal 收尾终端资源：关会话 → 等读循环排干 → 关管道 → 丢掉 term 的引用。
	//
	// ⚠️ 必须在 ProcessExited() 收到之后调。
	// ⚠️ 实现里绝不能再调 go-pty 的 p.Close()——M1 实测堆损坏 0xc0000374，2/2 复现。
	CloseTerminal() error

	KillProcess() error
}
