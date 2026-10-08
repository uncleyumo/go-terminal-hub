package hub

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/uncleyumo/go-terminal-hub/internal/exec/conpty"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/session"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/sink"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// 控制权的两个来源。同一时刻一个会话只有一个是「控制方」，
// 只有它能发输入、改尺寸——尺寸跟着控制方走。
const (
	SourceDesktop = "desktop"
	SourceWeb     = "web"
)

// ErrNotController 表示调用方不是这个会话当前的控制方。
// 旁观方发输入会被拒，这是防止两个端同时往里打字、把画面搞乱。
var ErrNotController = errors.New("caller is not the controller of this session")

type Record struct {
	Entry executor.Entry `json:"entry"`
	sess  *session.Session

	// lease 用独立的锁，不跟 h.mu 混：SetController 里要调 sess.Resize，
	// 那可能等一会儿，不该把整个 Hub 卡住。
	leaseMu sync.RWMutex
	lease   string

	// sizes 记住每个来源上次要的尺寸。接管/交还时要切回对方上次用的那个，
	// 否则「交还」之后 PTY 会停在接管方的尺寸上。
	sizes map[string][2]uint16
}

// ringCapacity 是每个会话回放缓冲的容量。
// 桌面端 xterm 用的是 scrollback: 5000 行，5000 行 × 80 列 ≈ 400 KB，
// 512 KB 是对得上这个量级的取值。
const ringCapacity = 512 << 10

type Hub struct {
	mu      sync.Mutex
	records map[string]*Record
	// starting 占位用：检查与登记之间放开了锁，靠它挡住并发启动。
	starting map[string]struct{}

	// broadcasters 用独立的锁：Start 会持有 h.mu 一路做到进程创建，
	// 而 Subscribe 是远程桥的热路径，不该被建进程拖住。
	bcMu         sync.Mutex
	broadcasters map[string]*broadcaster
}

type RecordStatus struct {
	ID       string `json:"id"`
	ExitCode int    `json:"exitCode"`
	ErrMsg   string `json:"errMsg"`
	Running  bool   `json:"running"`
}

var (
	once   sync.Once
	global *Hub
)

func GetHub() *Hub {
	once.Do(func() {
		global = NewHub()
	})
	return global
}

func NewHub() *Hub {
	return &Hub{
		records:      make(map[string]*Record),
		starting:     make(map[string]struct{}),
		broadcasters: make(map[string]*broadcaster),
	}
}

// Start 起一个会话。
//
// ⚠️ 全程不能持着 h.mu 去调 s.Start()：那里面会**同步**回调 sink.OnStarted，
// 而 sink 那条链最终可能回头读 Hub（远程桥就是这样），拿同一把不可重入的锁
// 再锁一次就是自锁死。所以分三段：持锁检查 → 放锁启动 → 持锁登记。
func (h *Hub) Start(id string, sink session.Sink) error {
	entry, release, err := h.prepareStart(id)
	if err != nil {
		return err
	}
	// 名额一直占到 Record 登记完为止，中途任何一条 return 都会放掉。
	// defer 是后进先出：下面那个 h.mu.Unlock 先跑，再跑这个，不会自己锁自己。
	defer release()

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
		slog.Error("log mode is not supported yet when starting session")
		return errors.New("log mode is not supported yet when starting session")
	default:
		return errors.New("unsupported mode: " + entry.Mode)
	}

	// 记的是**实际生效**的尺寸，不是 entry 里的原始值。
	// 会话定义里没写网格时 entry 是 0，而 executor 会兜底成默认值——
	// 这里跟着 0 走的话，接管、抖画面这些按真实网格算的地方就全错了。
	cols, rows := executor.GridSize(entry.Cols, entry.Rows)

	h.mu.Lock()
	defer h.mu.Unlock()
	h.records[id] = &Record{
		Entry: entry,
		sess:  s,
		lease: SourceDesktop,
		sizes: map[string][2]uint16{
			SourceDesktop: {cols, rows},
		},
	}
	return nil
}

// prepareStart 做启动前的检查与准备，占一个「正在启动」的名额，
// 并返回释放名额的函数——**调用方要一直拿到 Record 登记完才放**。
//
// 名额是必要的：检查与登记之间放开了锁，没有它的话两次并发 Start 会各起一个
// 进程，后登记的那个把前一个的 Record 覆盖掉——前一个进程就成了没人管的孤儿。
func (h *Hub) prepareStart(id string) (executor.Entry, func(), error) {
	h.mu.Lock()

	if _, busy := h.starting[id]; busy {
		h.mu.Unlock()
		return executor.Entry{}, nil, errors.New("session is already starting, please wait")
	}
	if record, ok := h.records[id]; ok {
		if _, done := record.sess.Result(); !done {
			h.mu.Unlock()
			return executor.Entry{}, nil, errors.New("session is still running, please wait or stop it first")
		}
	}
	h.starting[id] = struct{}{}
	h.mu.Unlock()

	release := func() {
		h.mu.Lock()
		delete(h.starting, id)
		h.mu.Unlock()
	}

	storeInstance, err := store.GetStore()
	if err != nil {
		release()
		return executor.Entry{}, nil, err
	}
	dataStore, ok := storeInstance.GetOne(id)
	if !ok {
		release()
		return executor.Entry{}, nil, errors.New("data store not found for id when starting session: " + id)
	}
	return executor.BuildEntry(dataStore), release, nil
}

func (h *Hub) StartSession(id string) error {
	// application.Get() 在 application.New 之前返回 nil（单元测试里就是这种情况）。
	// EmitSink 对 nil 是安全的，会话照跑，只是不往前端推事件。
	bc := newBroadcaster(id, ringCapacity, sink.NewBatchingSink(&sink.EmitSink{
		App: application.Get(),
		Id:  id,
	}))

	h.bcMu.Lock()
	if old, ok := h.broadcasters[id]; ok {
		// 重启会换一条新的 emit 链（旧的 BatchingSink 已经 close 了），
		// 但已经挂上的订阅者要跟过来，否则远程桥会在重启后静默失联。
		bc.takeSubs(old)
	}
	h.broadcasters[id] = bc
	h.bcMu.Unlock()

	if err := h.Start(id, bc); err != nil {
		h.bcMu.Lock()
		// 只有还是自己那份才删，别把并发的另一次 Start 建的删掉
		if cur, ok := h.broadcasters[id]; ok && cur == bc {
			delete(h.broadcasters, id)
		}
		h.bcMu.Unlock()
		return err
	}
	return nil
}

// Subscribe 把一个订阅者挂到会话 id 上，并交出该会话的环形缓冲。
//
// 订阅者从游标 0 开始读 ring.Since，读到的是「全部历史 + 之后的实时输出」，
// 中间不漏不重——因为历史就在同一个环里，不存在单独的取快照那一步。
//
// 返回的 cancel 必须被调用，否则订阅者会一直挂在 broadcaster 上。
func (h *Hub) Subscribe(id string, s Subscriber) (ring *Ring, cancel func(), err error) {
	h.bcMu.Lock()
	defer h.bcMu.Unlock()

	bc, ok := h.broadcasters[id]
	if !ok {
		return nil, nil, errors.New("no broadcaster for id: " + id)
	}
	ring = bc.subscribe(s)
	return ring, func() { bc.unsubscribe(s) }, nil
}

// History 返回会话当前的回放缓冲内容（一份拷贝）。
func (h *Hub) History(id string) ([]byte, error) {
	h.bcMu.Lock()
	bc, ok := h.broadcasters[id]
	h.bcMu.Unlock()
	if !ok {
		return nil, errors.New("no broadcaster for id: " + id)
	}
	return bc.ring.Snapshot(), nil
}

func (h *Hub) StopSession(id string) error {
	h.mu.Lock()

	record, ok := h.records[id]
	if !ok {
		h.mu.Unlock()
		return errors.New("record not found for id when stopping: " + id)
	}
	if result, done := record.sess.Result(); done == true {
		h.mu.Unlock()
		return fmt.Errorf("session is already done with result when stopping: %v", result)
	}
	h.mu.Unlock()
	record.sess.Stop()
	return nil
}

func (h *Hub) RestartSession(id string) error {
	h.mu.Lock()

	record, ok := h.records[id]
	if !ok {
		h.mu.Unlock()
		return errors.New("record not found for id when restarting: " + id)
	}

	if result, done := record.sess.Result(); done != true {
		h.mu.Unlock()
		return fmt.Errorf("you can only restart a session that is done: %v", result)
	}

	record.sess.Stop()
	h.mu.Unlock()
	return h.StartSession(id)
}

func (h *Hub) WriteSession(id string, data string) (int, error) {
	return h.WriteFrom(id, SourceDesktop, []byte(data))
}

func (h *Hub) ResizeSession(id string, cols, rows uint16) error {
	return h.ResizeFrom(id, SourceDesktop, cols, rows)
}

// record 取一条记录，顺带确认它还在跑。不加租约锁。
func (h *Hub) record(id string) (*Record, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	record, ok := h.records[id]
	if !ok {
		return nil, errors.New("record not found for id: " + id)
	}
	if result, done := record.sess.Result(); done {
		return nil, fmt.Errorf("session is already done with result: %v", result)
	}
	return record, nil
}

// WriteFrom 按来源写输入。只有当前控制方能写。
//
// 闸门放在 Hub 而不是远程桥里，因为桌面和 web 两条路都汇到这里。
// 持读锁写完：这样一次输入不会落在一次接管之后——否则接管瞬间的那几个字节
// 会跑进新控制方的画面里。
func (h *Hub) WriteFrom(id, who string, b []byte) (int, error) {
	record, err := h.record(id)
	if err != nil {
		return 0, err
	}

	record.leaseMu.RLock()
	defer record.leaseMu.RUnlock()

	if record.lease != who {
		return 0, fmt.Errorf("%w: %s", ErrNotController, record.lease)
	}
	return record.sess.Write(b)
}

// ResizeFrom 按来源改尺寸。
//
// 不是控制方也照样记下尺寸（但不下发）——接管时要用它切到对方上次的尺寸。
func (h *Hub) ResizeFrom(id, who string, cols, rows uint16) error {
	record, err := h.record(id)
	if err != nil {
		return err
	}

	record.leaseMu.Lock()
	record.sizes[who] = [2]uint16{cols, rows}
	allowed := record.lease == who
	record.leaseMu.Unlock()

	if !allowed {
		return fmt.Errorf("%w: %s", ErrNotController, who)
	}
	if err := record.sess.Resize(cols, rows); err != nil {
		return err
	}
	emitEvent("session:size", sink.SizePayload{ID: id, Cols: cols, Rows: rows})
	notifyTap(func(t Tap) { t.OnLease(id, who, cols, rows) })
	return nil
}

// SetController 把控制权交给 who，并把 PTY 切到 who 上次用的尺寸。
//
// cols/rows 传 0 表示「用记着的那个尺寸」——web 端接管走这条，
// 它在 attach 时已经报过自己的尺寸了。桌面端「收回控制」则要显式传：
// 它旁观时容器是按对方的网格撑开的，得先恢复自己的布局量出真实尺寸再交过来。
func (h *Hub) SetController(id, who string, cols, rows uint16) error {
	record, err := h.record(id)
	if err != nil {
		return err
	}

	record.leaseMu.Lock()
	if cols == 0 || rows == 0 {
		if size, ok := record.sizes[who]; ok {
			cols, rows = size[0], size[1]
		}
	}
	if cols != 0 && rows != 0 {
		record.sizes[who] = [2]uint16{cols, rows}
	}
	record.lease = who
	record.leaseMu.Unlock()

	if cols != 0 && rows != 0 {
		// 接管方尺寸变了就要重设 PTY。副作用是 TUI 程序会收到窗口变化通知
		// 而重绘——这正是纯旁观时画面可能是旧帧、接管后一定正确的原因。
		if err := record.sess.Resize(cols, rows); err != nil {
			return err
		}
	}

	emitEvent("session:lease", sink.LeasePayload{ID: id, Controller: who, Cols: cols, Rows: rows})
	emitEvent("session:size", sink.SizePayload{ID: id, Cols: cols, Rows: rows})
	notifyTap(func(t Tap) { t.OnLease(id, who, cols, rows) })
	return nil
}

// nudgeGap 是抖尺寸时两次 Resize 之间的间隔。
//
// 不留间隔的话两次调用可能被合成一次，程序只看到一个「没变」的尺寸，
// 就不重绘了——那样抖了等于没抖。
const nudgeGap = 30 * time.Millisecond

// NudgeRepaint 让 PTY 里的程序把当前画面整屏重画一遍。
//
// 为什么需要：远程端拿到的是**字节日志**，不是屏幕快照。claude code 这类
// 程序是增量更新的——它记着「我上次画了哪几行」，之后只发相对的擦除和重写。
// 从日志中间某处开始重放（刚 attach、或者落后太多触发 resync），浏览器上的
// 画面就跟它以为的画面错开了，而且**永远不会自己纠正**：它只会继续发相对更新，
// 错位一直留着。表现出来就是手机上一片黑，或者两帧文字叠在一起。
//
// 办法是把行数抖一下：减一行、再改回来。程序收到窗口变化就整屏重绘。
// 净尺寸没变，所以只有控制方有资格调——尺寸是它的。
func (h *Hub) NudgeRepaint(id, who string) error {
	record, err := h.record(id)
	if err != nil {
		return err
	}

	record.leaseMu.RLock()
	allowed := record.lease == who
	size, ok := record.sizes[who]
	record.leaseMu.RUnlock()

	if !allowed {
		return fmt.Errorf("%w: %s", ErrNotController, record.lease)
	}
	// 只有一行的话减不出合法的尺寸，直接算了
	if !ok || size[1] < 2 {
		return nil
	}

	cols, rows := size[0], size[1]
	if err := record.sess.Resize(cols, rows-1); err != nil {
		return err
	}
	time.Sleep(nudgeGap)
	return record.sess.Resize(cols, rows)
}

// Size 返回 PTY 当前的网格尺寸——也就是当前控制方上次设的那个。
// 旁观方按这个尺寸渲染：终端尺寸属于 PTY，不属于窗口。
func (h *Hub) Size(id string) (cols, rows uint16, ok bool) {
	h.mu.Lock()
	record, exists := h.records[id]
	h.mu.Unlock()
	if !exists {
		return 0, 0, false
	}

	record.leaseMu.RLock()
	defer record.leaseMu.RUnlock()

	size, ok := record.sizes[record.lease]
	if !ok {
		return 0, 0, false
	}
	return size[0], size[1], true
}

// Controller 返回当前控制方。第二个返回值表示会话是否存在。
func (h *Hub) Controller(id string) (string, bool) {
	h.mu.Lock()
	record, ok := h.records[id]
	h.mu.Unlock()
	if !ok {
		return "", false
	}
	record.leaseMu.RLock()
	defer record.leaseMu.RUnlock()
	return record.lease, true
}

// emitEvent 发一条 Wails 事件。应用还没起来时静默跳过——
// 单元测试里 NewHub() 是直接用的，没有 application 实例。
func emitEvent[T any](name string, payload T) {
	if app := application.Get(); app != nil {
		app.Event.Emit(name, payload)
	}
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
		slog.Debug("record not found for id when removing: " + id)
		return nil
	}

	// check if the session is done
	if _, done := h.records[id].sess.Result(); !done {
		return errors.New("session is still running, please stop it first")
	}
	delete(h.records, id)

	h.bcMu.Lock()
	delete(h.broadcasters, id)
	h.bcMu.Unlock()
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

func (h *Hub) StopAllSessions() error {
	var m sync.Mutex
	var wg sync.WaitGroup
	var errs error
	recordStatus := h.ListRecordStatus()
	for _, record := range recordStatus {
		if record.Running {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				if err := h.Stop(id); err != nil {
					m.Lock()
					errs = errors.Join(errs, err)
					m.Unlock()
				}
			}(record.ID)
		}
	}
	wg.Wait()
	return errs
}

// LaunchSpec 返回会话启动时用的规格，用于回显实际命令行。
//
// 这里刻意只给 LaunchSpec、不给 *session.Session：Session 的 Write/Resize
// 是导出的，谁拿到它都能绕过控制权闸门。之前 GetSession 就是这么一个口子。
func (h *Hub) LaunchSpec(id string) (executor.LaunchSpec, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if record, ok := h.records[id]; ok {
		return record.sess.GetLaunchSpec(), nil
	}
	return executor.LaunchSpec{}, errors.New("record not found for id when getting launch spec: " + id)
}
