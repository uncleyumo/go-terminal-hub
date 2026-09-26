package session

import (
	"log/slog"
	"time"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
)

type Sink interface {
	OnOutput(chunk []byte)
	OnProcessExited(res executor.ExitResult)
	OnStarted(id string)
}

type Session struct {
	id          string
	ex          executor.Executor
	sink        Sink
	doneCh      chan struct{}
	exitResult  executor.ExitResult
	drainExitCh chan struct{}
}

func NewSession(id string, ex executor.Executor, sink Sink) *Session {
	return &Session{id, ex, sink, make(chan struct{}), executor.ExitResult{}, make(chan struct{})}
}

func (s *Session) Start(spec executor.LaunchSpec) error {
	err := s.ex.Start(spec)
	if err != nil {
		slog.Error("failed to start session", "err", err)
		return err
	}
	go s.drainLoop()
	go s.reapLoop()
	s.sink.OnStarted(s.id)
	return nil
}

// drainLoop reads from the process output and forwards it to the sink
func (s *Session) drainLoop() {
	for chunk := range s.ex.Output() {
		s.sink.OnOutput(chunk)
	}
	close(s.drainExitCh)
}

// reapLoop the real ending process of session, no matter normal or abnormal
func (s *Session) reapLoop() {
	res := <-s.ex.ProcessExited()
	s.exitResult = res
	close(s.doneCh)
	_ = s.ex.CloseTerminal()
	// wait for drainLoop to finish
	<-s.drainExitCh
	s.sink.OnProcessExited(res)
}

func (s *Session) Write(b []byte) (int, error) {
	return s.ex.Write(b)
}

func (s *Session) Resize(cols, rows uint16) error {
	return s.ex.Resize(cols, rows)
}

func (s *Session) Stop() {
	stopErr := s.ex.StopProcess()
	if stopErr != nil {
		slog.Error("failed to stop process", "err", stopErr)
		if killErr := s.ex.KillProcess(); killErr != nil {
			slog.Error("failed to kill process", "err", killErr)
		}
	}
	select {
	case <-s.doneCh:
		slog.Debug("session stopped")
	case <-time.After(2 * time.Second):
		slog.Warn("session took too long to stop")
		if killErr := s.ex.KillProcess(); killErr != nil {
			slog.Error("failed to kill process", "err", killErr)
		}
	}
}

func (s *Session) Result() (executor.ExitResult, bool) {
	select {
	case <-s.doneCh:
		return s.exitResult, true
	default:
		return executor.ExitResult{}, false
	}
}
