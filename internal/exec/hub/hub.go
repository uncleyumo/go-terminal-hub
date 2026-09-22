package hub

import (
	"sync"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/session"
)

type Record struct {
	Entry executor.Entry
	sess  *session.Session
}

type Hub struct {
	mu      sync.RWMutex
	records map[string]*Record
}

func NewHub() *Hub {
	return &Hub{
		records: make(map[string]*Record),
	}
}

func (h *Hub) Start(id string, e executor.Entry, sink session.Sink) error {
	// TODO
	return nil
}

func (h *Hub) Write(id string, b []byte) (int, error) {
	// TODO
	return 0, nil
}

func (h *Hub) Resize(id string, cols, rows uint16) error {
	// TODO
	return nil
}

func (h *Hub) Stop(id string) error {
	// TODO
	return nil
}
