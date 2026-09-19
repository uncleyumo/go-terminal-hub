package exec

import "github.com/aymanbagabas/go-pty"

type ConPTYExecutor struct {
	p    pty.Pty
	cmd  *pty.Cmd
	spec LaunchSpec
	// TODO 实现 Executor
}

func (c *ConPTYExecutor) Start(spec LaunchSpec) error {
	//TODO implement me
	panic("implement me")
}

func (c *ConPTYExecutor) Output() <-chan []byte {
	//TODO implement me
	panic("implement me")
}

func (c *ConPTYExecutor) Done() <-chan ExitResult {
	//TODO implement me
	panic("implement me")
}

func (c *ConPTYExecutor) Write(b []byte) (int, error) {
	//TODO implement me
	panic("implement me")
}

func (c *ConPTYExecutor) Resize(cols, rows uint16) error {
	//TODO implement me
	panic("implement me")
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
