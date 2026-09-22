package store

import "sync"

type DataStore struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`   // bat | cmd | ps1 | exe | shell
	Target    string   `json:"target"` // 脚本路径 或 可执行文件路径
	Args      string   `json:"args"`   // 原始字符串，绝不 split
	WorkDir   string   `json:"workDir"`
	Env       []string `json:"env"`      // KEY=VALUE
	Mode      string   `json:"mode"`     // terminal | log
	Encoding  string   `json:"encoding"` // auto | utf8 | gbk，仅日志模式用
	Cols      uint16   `json:"cols"`
	Rows      uint16   `json:"rows"`
	AutoStart bool     `json:"autoStart"`
}

var (
	once    sync.Once
	global  *Store
	loadErr error
)

func GetStore() *Store {
	initOnce("data.json")
	return global
}

func initOnce(absPath string) {
	once.Do(func() {
		s := &Store{dataFile: absPath, dataList: make(map[string]DataStore)}
		if err := s.LoadDataJson(); err != nil {
			loadErr = err
		} else {
			global = s
		}
	})
}

type Store struct {
	mu       sync.RWMutex
	dataFile string
	dataList map[string]DataStore
}

func (s *Store) LoadDataJson() error {
	// TODO: load data from JSON file
	// If the file does not exist, it will be created.
	// filling data to dataList
	return nil
}
