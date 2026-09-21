package exec

import (
	"log/slog"
	"syscall"

	"github.com/aymanbagabas/go-pty"
)

type ConPTYExecutor struct {
	p      pty.Pty
	cmd    *pty.Cmd
	spec   LaunchSpec
	outCh  chan []byte
	doneCh chan ExitResult
}

func (c *ConPTYExecutor) Start(spec LaunchSpec) error {
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
	cmd.Env = spec.Env
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
	outChannel := make(chan []byte)
	exitResultChannel := make(chan ExitResult)
	c.outCh = outChannel
	c.doneCh = exitResultChannel
	c.p = p
	c.cmd = cmd
	c.spec = spec

	// handle output of the command
	go func() {
		buf := make([]byte, 4096)
		for {
			n, bufReadErr := c.p.Read(buf)
			if n > 0 {
				slog.Debug("read from pty", "bytes", n)
				tempChannel := make([]byte, n)
				copy(tempChannel, buf[:n])
				outChannel <- tempChannel
			}
			if bufReadErr != nil {
				slog.Debug("failed to read from pty in current iteration", "err", bufReadErr)
				break
			}
		}
		close(outChannel)
	}()

	// handle exit result of the command
	go func() {
		waitErr := c.cmd.Wait()
		exitCode := -1
		if c.cmd.ProcessState != nil {
			exitCode = c.cmd.ProcessState.ExitCode()
		}
		exitResultChannel <- ExitResult{Code: exitCode, Err: waitErr}
		close(exitResultChannel)
	}()
	return nil
}

func (c *ConPTYExecutor) Output() <-chan []byte {
	return c.outCh
}

func (c *ConPTYExecutor) Done() <-chan ExitResult {
	return c.doneCh
}

func (c *ConPTYExecutor) Write(b []byte) (int, error) {
	//TODO implement me
	panic("implement me")
}

func (c *ConPTYExecutor) Resize(cols, rows uint16) error {
	slog.Debug("Resizing pty", "cols", cols, "rows", rows)
	return c.p.Resize(int(cols), int(rows))
}

func (c *ConPTYExecutor) Stop() error {
	//TODO implement me
	panic("implement me")
}

func (c *ConPTYExecutor) Close() error {
	//TODO implement me
	panic("implement me")
}

// 检查 ConPTYExecutor 是否实现了 Executor 接口
var _ Executor = (*ConPTYExecutor)(nil)
