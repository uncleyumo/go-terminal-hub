package store

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
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

type Settings struct {
	Language string `json:"language"`
	Theme    string `json:"theme"`
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
	mu               sync.Mutex
	dataFilePath     string
	settingsFilePath string
	dataList         map[string]DataStore
}

// GetStore will return a global Store instance
func GetStore() (*Store, error) {
	once.Do(func() {
		s := &Store{
			dataFilePath:     loadDataFilePath(),
			settingsFilePath: loadSettingsFilePath(),
			dataList:         make(map[string]DataStore),
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

func (s *Store) writeSettingsLocked(settings Settings) error {
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.settingsFilePath, raw, permFile)
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

func loadSettingsFilePath() string {
	if dir := os.Getenv("GO_TERMINAL_HUB_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".go-terminal-hub", "settings.json")
	}
	return filepath.Join(".go-terminal-hub", "settings.json")
}

func (s *Store) Add(data DataStore) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	uuid7, err := uuid.NewV7()
	if err != nil {
		return err
	}
	data.ID = uuid7.String()

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
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dataList == nil {
		return DataStore{}, false
	}
	data, ok := s.dataList[id]
	return data, ok
}

func (s *Store) List() []DataStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dataList == nil {
		return []DataStore{}
	}
	list := make([]DataStore, 0, len(s.dataList))
	for _, data := range s.dataList {
		list = append(list, data)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ID > list[j].ID
	})
	return list
}

func (s *Store) GetSettings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings := Settings{
		Language: "en",
		Theme:    "light",
	}
	if err := os.MkdirAll(filepath.Dir(s.settingsFilePath), permDir); err != nil {
		slog.Error("Failed to create directory for settings file", "err", err)
		return settings
	}
	raw, err := os.ReadFile(s.settingsFilePath)
	if err != nil {
		slog.Error("Failed to read settings file", "err", err)
		return settings
	}
	if len(raw) == 0 {
		// set default settings to file
		if err := s.writeSettingsLocked(settings); err != nil {
			slog.Error("Failed to write default settings to file", "err", err)
		}
	}

	if err := json.Unmarshal(raw, &settings); err != nil {
		slog.Error("Failed to unmarshal settings", "err", err)
		return settings
	}

	return settings
}

func (s *Store) UpdateSettings(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeSettingsLocked(settings)
}
