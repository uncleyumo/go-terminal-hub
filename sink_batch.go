package main

import (
	"sync"
	"time"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/session"
)

type BatchingSink struct {
	next session.Sink
	mu   sync.Mutex
	buf  []byte
	quit chan struct{}
}

func NewBatchingSink(next session.Sink) *BatchingSink {
	b := &BatchingSink{next: next, quit: make(chan struct{})}
	timer := time.NewTimer(50 * time.Millisecond)
	go func() {
		for {
			select {
			case <-timer.C:
				b.mu.Lock()
				if b.buf != nil {
					b.next.OnOutput(b.buf)
					b.buf = nil
				}
				b.mu.Unlock()
				timer.Reset(50 * time.Millisecond)
			case <-b.quit:
				timer.Stop()
				return
			}
		}
	}()
	return b
}

func (b *BatchingSink) OnOutput(chunk []byte) {
	b.mu.Lock()
	b.buf = append(b.buf, chunk...)
	b.mu.Unlock()
}

func (b *BatchingSink) OnProcessExited(res executor.ExitResult) {
	b.mu.Lock()
	if b.buf != nil {
		b.next.OnOutput(b.buf)
		b.buf = nil
	}
	b.next.OnProcessExited(res)
	b.mu.Unlock()
	close(b.quit)
}
