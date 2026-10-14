package main

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/uncleyumo/go-terminal-hub/remote/protocol"
)

const (
	// coder/websocket 的默认读上限是 32768 字节。终端输出一次爆发
	// （`dir /s`、`type 大文件`）轻易超过这个数，不放开的话连接会以
	// "message too big" 直接断掉。
	readLimit = 4 << 20

	// 出站队列长度。满了说明对端读得太慢，直接断——见 conn.send 的注释。
	outQueueLen = 256

	pingInterval = 20 * time.Second
	pingTimeout  = 10 * time.Second
	writeTimeout = 15 * time.Second

	// 排队关闭指令的等待上限。超了说明队列已经堵死，对端不必再等了。
	closeEnqueueTimeout = 2 * time.Second
)

type frame struct {
	typ  websocket.MessageType
	data []byte

	// closeCode > 0 表示这是一条关闭指令，由写协程执行关闭握手。
	//
	// 关闭必须走队列、不能直接调 ws.Close：直接调的话，队列里已经排着的帧
	// （比如刚发出去的 kicked）可能还没写出去，客户端就只看到「连接断了」，
	// 分不清是「被顶掉」还是「网络抖了」。
	closeCode   int
	closeReason string
}

// conn 是一条 WebSocket 连接，带一个出站队列和唯一的写协程。
//
// coder/websocket 不允许并发写，所以所有写出都经过写协程。
// 队列有界，塞不进去就断开对端：调用方可能是 hub 的输出转发路径，
// 在那里阻塞会一路堵回 ConPTY 读循环，把终端冻住。
type conn struct {
	ws     *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	out    chan frame

	// closed 表示已经请求关闭，不再接受新帧入队。
	closed atomic.Bool

	// graceful 表示走的是「先发完队列里排着的帧、再走关闭握手」这条路。
	// 置上之后 closeNow 就不能再抢着掐断了——抢了的话对端只会看到
	// 「连接断了」，分不清是被顶掉还是网络抖了。
	graceful atomic.Bool

	label string

	// clientID 只在浏览器连接上有值：从 cookie 里取，用来识别踢人循环
	// （被顶掉的浏览器如果立刻重连，认得出是它）。
	clientID string

	// key 是这条连接所属的使用 key。两侧都有：agent 的是 Authorization 头里
	// 那个，浏览器的是 cookie 会话里那个。relay 所有操作都要靠它找到对的那套
	// 槽位——不带的话就会串到别人的终端上。
	key string
}

func newConn(parent context.Context, ws *websocket.Conn, label string) *conn {
	ctx, cancel := context.WithCancel(parent)
	c := &conn{
		ws:     ws,
		ctx:    ctx,
		cancel: cancel,
		out:    make(chan frame, outQueueLen),
		label:  label,
	}
	ws.SetReadLimit(readLimit)
	go c.writeLoop()
	go c.pingLoop()
	return c
}

// send 入队一帧。返回 false 表示队列满或已在关闭中，调用方应当放弃这条连接。
func (c *conn) send(f frame) bool {
	if c.closed.Load() {
		return false
	}
	select {
	case c.out <- f:
		return true
	default:
		return false
	}
}

// sendJSON 把一个控制消息序列化成文本帧发出去。
func (c *conn) sendJSON(m protocol.Message) bool {
	b, err := protocol.Encode(m)
	if err != nil {
		slog.Error("encode control message failed", "conn", c.label, "type", m.Type, "err", err)
		return false
	}
	return c.send(frame{typ: websocket.MessageText, data: b})
}

// close 请求优雅关闭：先发完队列里已排的帧，再走关闭握手。
// 对端能读到关闭码，这是它区分「被顶掉」和「网络断了」的唯一依据。
func (c *conn) close(code int, reason string) {
	if c.closed.Swap(true) {
		return
	}
	c.graceful.Store(true)
	select {
	case c.out <- frame{closeCode: code, closeReason: reason}:
	case <-time.After(closeEnqueueTimeout):
		slog.Debug("close enqueue timed out, dropping conn", "conn", c.label)
		c.forceClose()
	}
}

// closeNow 立刻掐断，不发关闭帧。读协程出错时走这条。
//
// 优雅关闭已经在路上时直接返回：那条路上的关闭帧还没发出去，
// 抢着掐断会让对端只看到「连接断了」。
func (c *conn) closeNow() {
	if c.graceful.Load() {
		return
	}
	c.forceClose()
}

// forceClose 关掉 socket 并取消 context。
//
// ⚠️ cancel 这一步不能漏。只 CloseNow 的话，写协程会一直卡在 select 上
// （ctx 没取消、队列也没新帧），ping 协程也照常每 20 秒醒一次，
// 对着一个已关闭的连接发 ping —— 日志里就是一堆
// "ping failed: use of closed network connection"，而且协程永远不回收。
func (c *conn) forceClose() {
	c.closed.Store(true)
	c.cancel()
	_ = c.ws.CloseNow()
}

func (c *conn) writeLoop() {
	defer c.cancel()

	for {
		select {
		case <-c.ctx.Done():
			_ = c.ws.CloseNow()
			return
		case f := <-c.out:
			if f.closeCode > 0 {
				// 关闭握手。ws.Close 会先写关闭帧（对端能读到码），再去抢读锁
				// 等对端回应——读协程此时可能正占着读锁，所以这一步可能等一会儿。
				// 但关闭帧早就发出去了，等待失败不影响对端已经收到关闭码。
				_ = c.ws.Close(websocket.StatusCode(f.closeCode), f.closeReason)
				return
			}
			ctx, cancel := context.WithTimeout(c.ctx, writeTimeout)
			err := c.ws.Write(ctx, f.typ, f.data)
			cancel()
			if err != nil {
				slog.Debug("write failed, closing conn", "conn", c.label, "err", err)
				_ = c.ws.CloseNow()
				return
			}
		}
	}
}

// pingLoop 定期探活。手机网络会静默掐掉空闲连接（不产生 FIN），
// 只靠读循环是发现不了的，必须主动 ping。
func (c *conn) pingLoop() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(c.ctx, pingTimeout)
			err := c.ws.Ping(ctx)
			cancel()
			if err != nil {
				slog.Debug("ping failed, closing conn", "conn", c.label, "err", err)
				c.closeNow()
				return
			}
		}
	}
}

// readLoop 反复读帧交给 handler，直到出错。
// handler 返回 false 表示要求关闭这条连接。
func (c *conn) readLoop(handler func(typ websocket.MessageType, data []byte) bool) {
	for {
		typ, data, err := c.ws.Read(c.ctx)
		if err != nil {
			if !isNormalClose(err) {
				slog.Debug("read ended", "conn", c.label, "err", err)
			}
			c.closeNow()
			return
		}
		if !handler(typ, data) {
			c.closeNow()
			return
		}
	}
}

func isNormalClose(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway:
		return true
	}
	return false
}
