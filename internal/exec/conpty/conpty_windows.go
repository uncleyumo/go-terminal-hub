package conpty

import (
	"errors"
	"log/slog"
	"os"
	"sync"
	"syscall"

	"github.com/aymanbagabas/go-pty"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"golang.org/x/sys/windows"
)

//goland:noinspection GoNameStartsWithPackageName
type ConPTYExecutor struct {
	startMu    sync.Mutex
	mu         sync.Mutex
	term       pty.Pty
	cmd        *pty.Cmd
	spec       executor.LaunchSpec
	outCh      chan []byte
	procExitCh chan executor.ExitResult
	readDoneCh chan struct{}
}

func (c *ConPTYExecutor) Start(spec executor.LaunchSpec) error {
	c.startMu.Lock()
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.startMu.Unlock()
	p, err := pty.New()
	if err != nil {
		slog.Error("Failed to create pty", "err", err)
		return err
	}
	if spec.Cols < 1 || spec.Rows < 1 {
		slog.Info("set cols and rows to default: 80x25")
		spec.Cols, spec.Rows = 80, 25
	}
	err = p.Resize(int(spec.Cols), int(spec.Rows))
	if err != nil {
		slog.Error("Failed to resize pty", "err", err)
	}
	cmd := p.Command(spec.Path)
	if spec.WorkDir == "" {
		slog.Debug("work dir is empty, set to current directory")
		slog.Debug("current directory", "dir", spec.WorkDir)
	}
	cmd.Dir = spec.WorkDir
	environ := os.Environ()
	cmd.Env = append(environ, spec.Env...)
	sysProcAttr := &syscall.SysProcAttr{
		CmdLine: spec.Command,
	}
	cmd.SysProcAttr = sysProcAttr
	if err := cmd.Start(); err != nil {
		slog.Error("Failed to start command", "err", err)
		// close pty if start fails
		if closeErr := p.Close(); closeErr != nil {
			slog.Error("Failed to close pty", "err", closeErr)
		}
		return err
	}

	c.outCh = make(chan []byte)
	c.procExitCh = make(chan executor.ExitResult)
	c.term = p
	c.cmd = cmd
	c.spec = spec
	c.readDoneCh = make(chan struct{})

	// handle output of the command
	go func() {
		defer close(c.readDoneCh)
		buf := make([]byte, 4096)
		for {
			n, bufReadErr := c.term.Read(buf)
			if n > 0 {
				slog.Debug("read from pty", "bytes", n)
				tempChannel := make([]byte, n)
				copy(tempChannel, buf[:n])
				c.outCh <- tempChannel
			}
			if bufReadErr != nil {
				slog.Debug("failed to read from pty in current iteration", "err", bufReadErr)
				break
			}
		}
		close(c.outCh)
	}()

	// handle exit result of the command
	go func() {
		waitErr := c.cmd.Wait()
		exitCode := -1
		if c.cmd.ProcessState != nil {
			exitCode = c.cmd.ProcessState.ExitCode()
		}
		c.procExitCh <- executor.ExitResult{Code: exitCode, Err: waitErr}
		close(c.procExitCh)
	}()
	return nil
}

func (c *ConPTYExecutor) Output() <-chan []byte {
	return c.outCh
}

func (c *ConPTYExecutor) ProcessExited() <-chan executor.ExitResult {
	return c.procExitCh
}

func (c *ConPTYExecutor) Write(b []byte) (int, error) {
	c.mu.Lock()
	t := c.term
	c.mu.Unlock()
	if t == nil {
		return 0, errors.New("pty is nil")
	}
	return t.Write(b)
}

func (c *ConPTYExecutor) Resize(cols, rows uint16) error {
	c.mu.Lock()
	t := c.term
	c.mu.Unlock()
	if t == nil {
		return errors.New("pty is nil")
	}
	slog.Debug("Resizing pty", "cols", cols, "rows", rows)
	return t.Resize(int(cols), int(rows))
}

func (c *ConPTYExecutor) StopProcess() error {
	c.mu.Lock()
	t := c.term
	c.mu.Unlock()
	if t == nil {
		return errors.New("pty is nil")
	}
	_, err := t.Write([]byte{0x03})
	return err
}

func (c *ConPTYExecutor) CloseTerminal() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.term == nil {
		return nil
	}
	handle := windows.Handle(c.term.Fd())
	windows.ClosePseudoConsole(handle)
	<-c.readDoneCh
	v, ok := c.term.(pty.ConPty)
	if !ok {
		slog.Error("pty is not a ConPty")
		return errors.New("pty is not a ConPty")
	}
	_ = v.InputPipe().Close()
	_ = v.OutputPipe().Close()
	c.term = nil
	return nil
}

func (c *ConPTYExecutor) KillProcess() error {
	c.startMu.Lock()
	cmd := c.cmd
	c.startMu.Unlock()
	if cmd == nil {
		return errors.New("cmd is nil")
	}
	return cmd.Process.Kill()
}

// 检查 ConPTYExecutor 是否实现了 Executor 接口
var _ executor.Executor = (*ConPTYExecutor)(nil)
