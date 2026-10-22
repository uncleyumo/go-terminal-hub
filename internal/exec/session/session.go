package session

import (
	"log/slog"
	"sync"
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

	// timeMu 护着下面两个时刻。
	//
	// startedAt 只在 Start 里写一次，endedAt 只在 reapLoop 里写一次，
	// 但读它们的是别的 goroutine（列状态的界面），所以要一把锁。
	timeMu     sync.Mutex
	startedAt  time.Time
	endedAt    time.Time
}

func NewSession(id string, ex executor.Executor, sink Sink) *Session {
	return &Session{
		id:          id,
		ex:          ex,
		sink:        sink,
		doneCh:      make(chan struct{}),
		exitResult:  executor.ExitResult{},
		drainExitCh: make(chan struct{}),
	}
}

func (s *Session) Start(spec executor.LaunchSpec) error {
	err := s.ex.Start(spec)
	if err != nil {
		slog.Error("failed to start session", "err", err)
		return err
	}
	s.setStartedAt(time.Now())
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
	s.setEndedAt(time.Now())
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

func (s *Session) GetLaunchSpec() executor.LaunchSpec {
	return s.ex.GetLaunchSpec()
}

func (s *Session) setStartedAt(t time.Time) {
	s.timeMu.Lock()
	s.startedAt = t
	s.timeMu.Unlock()
}

func (s *Session) setEndedAt(t time.Time) {
	s.timeMu.Lock()
	s.endedAt = t
	s.timeMu.Unlock()
}

// StartedAt 是这个会话的进程起来的时刻。
// 没起来过（启动失败、还没启动）是零值。
func (s *Session) StartedAt() time.Time {
	s.timeMu.Lock()
	defer s.timeMu.Unlock()
	return s.startedAt
}

// EndedAt 是这个会话的进程退出的时刻。
// 还在跑是零值。
func (s *Session) EndedAt() time.Time {
	s.timeMu.Lock()
	defer s.timeMu.Unlock()
	return s.endedAt
}

// Pid 是这个会话的进程号。拿不到是 0。
func (s *Session) Pid() int {
	return s.ex.Pid()
}
