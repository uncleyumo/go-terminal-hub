package hub

import "sync"

// Ring 是固定容量的字节缓冲，记一个会话最近输出的原始字节。
//
// 它记的是「字节日志」不是「屏幕快照」：把这段字节原样喂给一个全新的 xterm，
// 能重建出大部分画面（转义序列都在里面）。但全屏 TUI 的当前帧不一定对得上——
// 那要靠接管时改尺寸，让程序自己重绘。
//
// 偏移量是「累计写入字节数」，单调递增的全局坐标，不随环形回绕变化。
// 调用方拿它当游标，就能问「从我上次读到的地方到现在，新来了什么」。
type Ring struct {
	mu    sync.Mutex
	buf   []byte
	max   int
	total uint64
}

func NewRing(max int) *Ring {
	return &Ring{max: max}
}

// Append 追加一段输出，必要时从头部裁掉旧数据。
func (r *Ring) Append(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf = append(r.buf, b...)
	r.total += uint64(len(b))
	r.trim()
}

// trim 把 buf 裁到 max 以内。调用方必须已持有 r.mu。
//
// 裁的位置要对齐边界：直接从中间砍会切碎 CSI/OSC 转义序列或 UTF-8 码点，
// 客户端渲染出来就是乱码。所以往后找到第一个「转义序列开头」或「行首」。
func (r *Ring) trim() {
	if len(r.buf) <= r.max {
		return
	}
	cut := len(r.buf) - r.max
	for cut < len(r.buf) && r.buf[cut] != 0x1b && r.buf[cut] != '\n' {
		cut++
	}
	if cut >= len(r.buf) {
		// 这 max 个字节里一个边界都没有（极端情况）。
		// 退回原始位置，只保证不把一个 UTF-8 码点切成两半。
		cut = len(r.buf) - r.max
		for cut < len(r.buf) && r.buf[cut]&0xC0 == 0x80 {
			cut++
		}
	}
	r.buf = append(r.buf[:0], r.buf[cut:]...)
}

// Snapshot 拷贝出当前全部历史。
func (r *Ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}

// Since 从全局偏移 cursor 读到最新。
//
// ok 为 false 表示 cursor 已经落在环外（那段被裁掉了），调用方应当改用
// Snapshot 整段重发，并让客户端先清屏——否则会在错误的画面上续写。
func (r *Ring) Since(cursor uint64) (data []byte, next uint64, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	start := r.total - uint64(len(r.buf))
	if cursor < start || cursor > r.total {
		return nil, r.total, false
	}
	out := make([]byte, r.total-cursor)
	copy(out, r.buf[cursor-start:])
	return out, r.total, true
}

// Total 是累计写入的字节数，用作 Since 的游标基准。
func (r *Ring) Total() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}

// Reset 清空历史。新一轮启动时调用——上一轮的输出不该被回放出来。
func (r *Ring) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf = nil
	r.total = 0
}
