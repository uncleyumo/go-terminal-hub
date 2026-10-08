package remote

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/executor"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/hub"
	"github.com/uncleyumo/go-terminal-hub/internal/exec/store"
	"github.com/uncleyumo/go-terminal-hub/internal/paste"
	"github.com/uncleyumo/go-terminal-hub/remote/protocol"
)

// 单帧上限。整段回放可能有几百 KB，切成小片发，免得一次 write 卡太久。
const maxFrameBytes = 64 << 10

const writeTimeout = 20 * time.Second

// outQueueLen 是出站队列长度。
//
// 队列满 = 对端读得太慢，直接断开重连（重连会整段重同步），
// 绝不阻塞调用方 —— 调用方可能是 hub 的输出链路，在那里等会一路堵回
// ConPTY 读循环，把终端冻住。
const outQueueLen = 512

type outFrame struct {
	typ  websocket.MessageType
	data []byte
}

// bridge 是一条连接的协议状态机：把浏览器的控制消息翻成 hub 调用，
// 把 hub 的输出翻成二进制帧。
//
// 它同时是 hub 的 Subscriber：hub 只敲一下「有新数据」，真正读字节、
// 发字节都在这里自己的写协程里做。所以浏览器再慢也堵不到 PTY。
type bridge struct {
	agent *Agent
	hub   *hub.Hub
	ws    *websocket.Conn
	cfg   Config

	ctx    context.Context
	cancel context.CancelFunc

	// out 是唯一的出站通道，由 writeLoop 一个协程消费。
	//
	// 所有写出都必须走这里，不能就地 ws.Write。原因：
	//   - coder/websocket 不允许并发写
	//   - 更要命的是**不能阻塞调用方**。sendControl 会被 hub 的 Tap 调到，
	//     而 Tap 是在 Session 的 goroutine 上、还持着 broadcaster 的锁被调的。
	//     在这里等一次网络写，等于把整条输出链路卡住：drainLoop 停 → ConPTY
	//     读循环停 → 子进程 write 阻塞，同时 ping 也答不上来，连接被对端判死。
	out chan outFrame

	mu        sync.Mutex
	boundID   string
	ring      *hub.Ring
	cursor    uint64
	cancelSub func()

	autoTakeover  bool
	browserOnline bool

	// notify 是容量 1 的信号通道：满了就丢，因为读协程下一轮一定会读到最新数据。
	notify chan struct{}
}

func newBridge(parent context.Context, a *Agent, ws *websocket.Conn, cfg Config) *bridge {
	ctx, cancel := context.WithCancel(parent)
	return &bridge{
		agent:  a,
		hub:    a.hub,
		ws:     ws,
		cfg:    cfg,
		ctx:    ctx,
		cancel: cancel,
		notify: make(chan struct{}, 1),
		out:    make(chan outFrame, outQueueLen),
	}
}

// Notify 实现 hub.Subscriber。它在 Session 的 drainLoop goroutine 上被调用，
// 所以只做一次非阻塞的信号发送，绝不碰网络。
func (b *bridge) Notify() {
	select {
	case b.notify <- struct{}{}:
	default:
		// 已经有一个待处理信号了，读协程下一轮会读到最新数据
	}
}

func (b *bridge) run() error {
	defer b.cancel()

	go b.writeLoop()
	go b.flushLoop()

	for {
		typ, data, err := b.ws.Read(b.ctx)
		if err != nil {
			return err
		}
		switch typ {
		case websocket.MessageText:
			b.handleControl(data)
		case websocket.MessageBinary:
			b.handleInput(data)
		}
	}
}

// --- 出站 -------------------------------------------------------------------

// writeLoop 是唯一的出站写协程。队列里的帧按入队顺序写出，
// 所以「先 attached 再回放」这种顺序是保住的。
func (b *bridge) writeLoop() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case f := <-b.out:
			ctx, cancel := context.WithTimeout(b.ctx, writeTimeout)
			err := b.ws.Write(ctx, f.typ, f.data)
			cancel()
			if err != nil {
				slog.Debug("bridge write failed", "err", err)
				b.cancel()
				return
			}
		}
	}
}

// write 入队。**绝不阻塞** —— 队列满了就断开重连，让对端重新同步。
//
// 不阻塞是硬要求：这个函数会被 hub 的 Tap 调到，而 Tap 是在 Session 的
// goroutine 上、还持着 broadcaster 的锁被调的。在这里等一次网络写，
// 会把整条输出链路连同 ping 一起卡死。
func (b *bridge) write(typ websocket.MessageType, data []byte) {
	select {
	case b.out <- outFrame{typ: typ, data: data}:
	case <-b.ctx.Done():
	default:
		slog.Warn("bridge outbound queue full, dropping connection")
		b.cancel()
	}
}

func (b *bridge) sendControl(m protocol.Message) {
	data, err := protocol.Encode(m)
	if err != nil {
		slog.Error("encode control message failed", "type", m.Type, "err", err)
		return
	}
	b.write(websocket.MessageText, data)
}

func (b *bridge) sendError(msg string) {
	b.sendControl(protocol.Message{Type: protocol.TypeError, Msg: msg})
}

// --- 输出 -------------------------------------------------------------------

// flushLoop 是唯一的输出写协程。它按游标从环形缓冲读增量：
// 浏览器慢的时候自然就读到更大的一块，不会堆成一堆小帧；
// 落后超过环容量时游标失效，整段重同步。
func (b *bridge) flushLoop() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-b.notify:
			b.flush()
		}
	}
}

func (b *bridge) flush() {
	b.mu.Lock()
	ring, cursor := b.ring, b.cursor
	b.mu.Unlock()

	if ring == nil {
		return
	}

	data, next, ok := ring.Since(cursor)
	resync := !ok
	if resync {
		// 游标落在环外了：这段历史已经被裁掉，只能在新的画面上续写。
		// 先让客户端清屏，再整段重发。
		data = ring.Snapshot()
		next = ring.Total()
	}

	b.mu.Lock()
	// 期间可能已经切到别的会话了，那就别把这个游标写回去
	if b.ring == ring {
		b.cursor = next
	}
	b.mu.Unlock()

	if resync {
		b.sendControl(protocol.Message{Type: protocol.TypeResync})
	}
	for len(data) > 0 {
		n := min(len(data), maxFrameBytes)
		b.write(websocket.MessageBinary, data[:n])
		data = data[n:]
	}
}

// --- 入站 -------------------------------------------------------------------

func (b *bridge) handleControl(data []byte) {
	m, err := protocol.Decode(data)
	if err != nil {
		b.sendError("bad control message: " + err.Error())
		return
	}

	switch m.Type {
	case protocol.TypeList:
		b.sendList()
	case protocol.TypeAttach:
		b.attach(m.ID)
	case protocol.TypeDetach:
		b.detach()
	case protocol.TypeResize:
		b.handleResize(m.Cols, m.Rows)
	case protocol.TypeTakeover:
		b.takeover()
	case protocol.TypeRelease:
		b.release()
	case protocol.TypeStart:
		b.startSession(m.ID)
	case protocol.TypeStop:
		b.stopSession(m.ID)
	case protocol.TypeRestart:
		b.restartSession(m.ID)
	case protocol.TypeResync:
		b.forceResync()
	case protocol.TypeRepaint:
		b.repaint()
	case protocol.TypeUpload:
		b.upload(m)
	case protocol.TypeBrowserOnline:
		b.browserOnline = true
		b.sendList()
	case protocol.TypeBrowserOffline:
		// 浏览器走了，没人能控制了：把所有 web 租约还给桌面。
		// 少了这一步，桌面会永远卡在旁观模式。
		b.releaseAllWebLeases()
		b.sendControl(protocol.Message{Type: protocol.TypeLease, Controller: hub.SourceDesktop})
	default:
		b.sendError("unknown message type: " + string(m.Type))
	}
}

// handleInput 处理浏览器的裸输入字节。
func (b *bridge) handleInput(data []byte) {
	id := b.bound()
	if id == "" {
		return
	}
	_, err := b.hub.WriteFrom(id, hub.SourceWeb, data)
	if err != nil && !errors.Is(err, hub.ErrNotController) {
		b.sendError(err.Error())
	}
	// ErrNotController 是正常的：旁观方敲键盘本来就该没反应，
	// 前端靠租约徽标告诉用户原因，不用再报错。
}

func (b *bridge) bound() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.boundID
}

// --- 会话绑定 ---------------------------------------------------------------

func (b *bridge) attach(id string) {
	b.detach()

	ring, cancel, err := b.hub.Subscribe(id, b)
	if err != nil {
		b.sendError(err.Error())
		return
	}

	cols, rows, _ := b.hub.Size(id)
	controller, _ := b.hub.Controller(id)

	b.mu.Lock()
	b.boundID = id
	b.ring = ring
	b.cursor = 0
	b.cancelSub = cancel
	b.autoTakeover = b.cfg.AutoTakeover
	b.mu.Unlock()

	b.sendControl(protocol.Message{
		Type:       protocol.TypeAttached,
		ID:         id,
		Cols:       cols,
		Rows:       rows,
		Controller: controller,
	})

	// 立刻把回放推过去，不等下一次 Notify。
	// 游标是 0，所以这一次 flush 拿到的就是全部历史。
	b.flush()
}

func (b *bridge) detach() {
	b.mu.Lock()
	cancel := b.cancelSub
	b.boundID = ""
	b.ring = nil
	b.cursor = 0
	b.cancelSub = nil
	b.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// repaint 请 Hub 把 PTY 的行列数抖一下，逼里面的程序整屏重画。
//
// 浏览器发这个的时机都是「我这边画面确定跟真实画面脱节了」：刚 attach 上来
// （回放的是字节日志，重建不出全屏程序的样子）、服务端发过 resync、刚接管。
//
// 不是控制方的话 Hub 会拒掉（尺寸是控制方的），这里不用单独判。
func (b *bridge) repaint() {
	id := b.bound()
	if id == "" {
		return
	}
	if err := b.hub.NudgeRepaint(id, hub.SourceWeb); err != nil && !errors.Is(err, hub.ErrNotController) {
		slog.Debug("nudge repaint failed", "id", id, "err", err)
	}
}

// upload 把浏览器发来的一张图存到 hub 这台机器上，再把路径回过去。
//
// 只存文件、不代插终端：路径要不要裹成 bracketed paste 取决于终端里的程序
// 有没有开那个模式，而那个状态只有浏览器那侧的 xterm 知道，所以插入由浏览器自己做。
func (b *bridge) upload(m protocol.Message) {
	id := b.bound()
	if id == "" {
		b.sendError("还没有绑定会话")
		return
	}
	// 往本机磁盘写文件是控制方才有的能力。旁观方点不出这个按钮，
	// 但闸门放在这里才算数——按钮藏起来不算权限。
	if who, _ := b.hub.Controller(id); who != hub.SourceWeb {
		b.sendError("先接管才能发图")
		return
	}

	data, err := base64.StdEncoding.DecodeString(m.Data)
	if err != nil {
		b.sendError("图片数据不完整")
		return
	}
	path, err := paste.Save(data)
	if err != nil {
		b.sendError(err.Error())
		return
	}
	b.sendControl(protocol.Message{Type: protocol.TypeUploaded, Path: path})
}

func (b *bridge) forceResync() {
	b.mu.Lock()
	b.cursor = 0
	b.mu.Unlock()
	b.flush()
}

// --- 控制权 -----------------------------------------------------------------

func (b *bridge) handleResize(cols, rows uint16) {
	id := b.bound()
	if id == "" {
		return
	}

	err := b.hub.ResizeFrom(id, hub.SourceWeb, cols, rows)
	if err != nil && !errors.Is(err, hub.ErrNotController) {
		b.sendError(err.Error())
		return
	}

	// 「连接后自动接管」只在 attach 之后第一次报尺寸时生效。
	// 不做成「每次 resize 都抢」——那样桌面点「收回控制」之后，
	// 手机一改字号就会把控制权又抢回去。
	b.mu.Lock()
	pending := b.autoTakeover
	b.autoTakeover = false
	b.mu.Unlock()

	if pending {
		b.takeover()
	}
}

func (b *bridge) takeover() {
	id := b.bound()
	if id == "" {
		return
	}
	// 尺寸传 0：用 web 端 attach 时记下的那个。SetController 会把它下发给 PTY，
	// 顺带触发 TUI 重绘——这就是接管后画面一定正确的原因。
	if err := b.hub.SetController(id, hub.SourceWeb, 0, 0); err != nil {
		b.sendError(err.Error())
	}
}

func (b *bridge) release() {
	id := b.bound()
	if id == "" {
		return
	}
	if err := b.hub.SetController(id, hub.SourceDesktop, 0, 0); err != nil {
		b.sendError(err.Error())
	}
}

// releaseAllWebLeases 把所有控制方是 web 的会话还给桌面。
// 浏览器断开、agent 断线时都要调——否则终端会卡在没人能输入的状态。
func (b *bridge) releaseAllWebLeases() {
	for _, st := range b.hub.ListRecordStatus() {
		who, ok := b.hub.Controller(st.ID)
		if !ok || who != hub.SourceWeb {
			continue
		}
		if err := b.hub.SetController(st.ID, hub.SourceDesktop, 0, 0); err != nil {
			slog.Debug("release web lease failed", "id", st.ID, "err", err)
		}
	}
}

// --- 生命周期（由 Agent 从 hub.Tap 转发过来） -------------------------------

func (b *bridge) onSessionStarted(id string) {
	b.sendControl(protocol.Message{Type: protocol.TypeStarted, ID: id})
	b.sendList()
}

func (b *bridge) onSessionExited(id string, res executor.ExitResult) {
	errMsg := ""
	if res.Err != nil {
		errMsg = res.Err.Error()
	}
	b.sendControl(protocol.Message{
		Type:   protocol.TypeExit,
		ID:     id,
		Code:   res.Code,
		ErrMsg: errMsg,
	})
	b.sendList()
}

func (b *bridge) onLeaseChanged(id, controller string, cols, rows uint16) {
	// 只推当前绑定的那个会话——浏览器一次只看一个
	if b.bound() != id {
		return
	}
	b.sendControl(protocol.Message{
		Type:       protocol.TypeLease,
		ID:         id,
		Controller: controller,
		Cols:       cols,
		Rows:       rows,
	})
	b.sendControl(protocol.Message{
		Type: protocol.TypeSize,
		ID:   id,
		Cols: cols,
		Rows: rows,
	})
}

// --- 会话操作 ---------------------------------------------------------------

func (b *bridge) startSession(id string) {
	if err := b.hub.StartSession(id); err != nil {
		b.sendError(err.Error())
	}
}

func (b *bridge) stopSession(id string) {
	if err := b.hub.StopSession(id); err != nil {
		b.sendError(err.Error())
	}
}

func (b *bridge) restartSession(id string) {
	if err := b.hub.RestartSession(id); err != nil {
		b.sendError(err.Error())
	}
}

// sendList 拼出会话列表。
//
// 数据来自两处，缺一不可：store 里有全部「已定义」的会话（名称、类型），
// 而 hub 里只有「本轮跑过至少一次」的那些（运行状态）。少了 store 那一半，
// 浏览器上会是一串 UUID；少了 hub 那一半，所有会话都显示成没跑过。
func (b *bridge) sendList() {
	storeInst, err := store.GetStore()
	if err != nil {
		b.sendError(err.Error())
		return
	}

	statusByID := make(map[string]hub.RecordStatus)
	for _, st := range b.hub.ListRecordStatus() {
		statusByID[st.ID] = st
	}

	items := storeInst.List()
	list := make([]protocol.SessionInfo, 0, len(items))
	for _, it := range items {
		info := protocol.SessionInfo{
			ID:       it.ID,
			Name:     it.Name,
			Kind:     it.Kind,
			ExitCode: -1,
		}
		if st, ok := statusByID[it.ID]; ok {
			info.Running = st.Running
			info.ExitCode = st.ExitCode
			info.ErrMsg = st.ErrMsg
		}
		list = append(list, info)
	}

	b.sendControl(protocol.Message{Type: protocol.TypeSessions, Sessions: list})
}
