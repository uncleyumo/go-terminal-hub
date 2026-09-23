package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

type DataStore struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Target    string   `json:"target"`
	Args      string   `json:"args"`
	WorkDir   string   `json:"workDir"`
	Env       []string `json:"env"`
	Mode      string   `json:"mode"`
	Encoding  string   `json:"encoding"`
	Cols      uint16   `json:"cols"`
	Rows      uint16   `json:"rows"`
	AutoStart bool     `json:"autoStart"`
}

const (
	// -rw-r--r--, 普通文件默认（配置、JSON、日志、数据文件）
	permFile os.FileMode = 0o644
	// -rwxr-xr-x, 目录默认 / 可执行脚本 / 二进制
	permDir os.FileMode = 0o755
)

var (
	once    sync.Once
	global  *Store
	loadErr error
)

type Store struct {
	mu           sync.RWMutex
	dataFilePath string
	dataList     map[string]DataStore
}

// GetStore will return a global Store instance
func GetStore() (*Store, error) {
	once.Do(func() {
		s := &Store{
			dataFilePath: loadDataFilePath(),
			dataList:     make(map[string]DataStore),
		}
		if err := s.load(); err != nil {
			loadErr = err
			return
		}
		global = s
	})
	return global, loadErr
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// o: octal,
	if err := os.MkdirAll(filepath.Dir(s.dataFilePath), permDir); err != nil {
		return err
	}

	raw, err := os.ReadFile(s.dataFilePath)
	if errors.Is(err, os.ErrNotExist) {
		return s.writeLocked(s.dataList)
	}
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &s.dataList); err != nil {
		return err
	}
	// prevent data.json's content is a literal 'null'
	if s.dataList == nil {
		s.dataList = make(map[string]DataStore)
	}
	return nil
}

func (s *Store) UpdateDataJson(dataList map[string]DataStore) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// prevent dataList is nil
	if dataList == nil {
		dataList = make(map[string]DataStore)
	}

	s.dataList = dataList
	return s.writeLocked(dataList)
}

func (s *Store) writeLocked(data map[string]DataStore) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.dataFilePath, raw, permFile)
}

func loadDataFilePath() string {
	if dir := os.Getenv("GO_TERMINAL_HUB_DIR"); dir != "" {
		return filepath.Join(dir, "data.json")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".go-terminal-hub", "data.json")
	}
	return filepath.Join(".go-terminal-hub", "data.json")
}

func (s *Store) Add(data DataStore) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	uuid7, err := uuid.NewV7()
	if err != nil {
		return err
	}

	// if data.ID is empty, use uuid7.String() as default
	if data.ID == "" {
		data.ID = uuid7.String()
	}

	if s.dataList == nil {
		s.dataList = make(map[string]DataStore)
	}
	s.dataList[uuid7.String()] = data
	return s.writeLocked(s.dataList)
}

func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dataList == nil {
		return nil
	}
	delete(s.dataList, id)
	return s.writeLocked(s.dataList)
}

func (s *Store) GetOne(id string) (DataStore, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.dataList == nil {
		return DataStore{}, false
	}
	data, ok := s.dataList[id]
	return data, ok
}
