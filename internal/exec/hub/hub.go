package hub

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/conpty"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/session"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
)

type Record struct {
	Entry executor.Entry
	sess  *session.Session
}

type Hub struct {
	mu      sync.Mutex
	records map[string]*Record
}

type RecordStatus struct {
	ID       string
	ExitCode int
	ErrMsg   string
	Running  bool
}

func NewHub() *Hub {
	return &Hub{
		records: make(map[string]*Record),
	}
}

func (h *Hub) Start(id string, sink session.Sink) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	record, ok := h.records[id]
	if ok {
		// exist a record with the given id
		_, done := record.sess.Result()
		if !done {
			// the session is still running
			return errors.New("session is still running, please wait or stop it first")
		}
	}
	storeInstance, err := store.GetStore()
	if err != nil {
		return err
	}
	dataStore, ok := storeInstance.GetOne(id)
	if !ok {
		return errors.New("data store not found for id: " + id)
	}
	entry := executor.BuildEntry(dataStore)
	var s *session.Session
	switch entry.Mode {
	case "terminal":
		s = session.NewSession(id, &conpty.ConPTYExecutor{}, sink)
		command, err := executor.BuildCommand(entry)
		if err != nil {
			return err
		}
		if err = s.Start(executor.LaunchSpec{
			Path:    command.Path,
			Command: command.CmdLine,
			WorkDir: entry.WorkDir,
			Env:     entry.Env,
			Cols:    entry.Cols,
			Rows:    entry.Rows,
		}); err != nil {
			return err
		}
	case "log":
		slog.Error("log mode is not supported yet")
		return errors.New("log mode is not supported yet")
	default:
		return errors.New("unsupported mode: " + entry.Mode)
	}
	h.records[id] = &Record{
		Entry: entry,
		sess:  s,
	}
	return nil
}

func (h *Hub) Write(id string, b []byte) (int, error) {
	h.mu.Lock()

	record, ok := h.records[id]
	if !ok {
		h.mu.Unlock()
		return 0, errors.New("record not found for id: " + id)
	}
	if result, done := record.sess.Result(); done {
		h.mu.Unlock()
		return 0, fmt.Errorf("session is already done with result: %v", result)
	}
	sess := record.sess
	h.mu.Unlock()
	return sess.Write(b)
}

func (h *Hub) Resize(id string, cols, rows uint16) error {
	h.mu.Lock()

	record, ok := h.records[id]
	if !ok {
		h.mu.Unlock()
		return errors.New("record not found for id: " + id)
	}
	if _, done := record.sess.Result(); done {
		h.mu.Unlock()
		return errors.New("session is already done")
	}
	sess := record.sess
	h.mu.Unlock()
	return sess.Resize(cols, rows)
}

func (h *Hub) Stop(id string) error {
	h.mu.Lock()

	record, ok := h.records[id]
	if !ok {
		h.mu.Unlock()
		return errors.New("record not found for id: " + id)
	}
	if _, done := record.sess.Result(); done {
		h.mu.Unlock()
		return errors.New("session is already done")
	}
	sess := record.sess
	// release the lock in advance to prevent blocking other operations when
	h.mu.Unlock()
	sess.Stop()
	return nil
}

func (h *Hub) Remove(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.records[id]; !ok {
		return errors.New("record not found for id: " + id)
	}

	// check if the session is done
	if _, done := h.records[id].sess.Result(); !done {
		return errors.New("session is still running, please stop it first")
	}
	delete(h.records, id)
	return nil
}

func (h *Hub) ListRecordStatus() []RecordStatus {
	h.mu.Lock()
	defer h.mu.Unlock()

	var result []RecordStatus
	for id, record := range h.records {
		exitResult, done := record.sess.Result()
		if done {
			if exitResult.Err != nil {
				result = append(result, RecordStatus{
					ID:       id,
					ExitCode: exitResult.Code,
					ErrMsg:   exitResult.Err.Error(),
					Running:  false,
				})
			} else {
				result = append(result, RecordStatus{
					ID:       id,
					ExitCode: exitResult.Code,
					ErrMsg:   "",
					Running:  false,
				})
			}
		} else {
			result = append(result, RecordStatus{
				ID:       id,
				ExitCode: -1,
				ErrMsg:   "",
				Running:  true,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID > result[j].ID
	})
	return result
}
