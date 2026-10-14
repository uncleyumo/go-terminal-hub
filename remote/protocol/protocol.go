// Package protocol 定义远程接管链路的线协议。hub 侧的桥和云端的 relay 共用这一份。
//
// 帧的用法：
//
//	文本帧 —— JSON 控制消息，就是下面这些类型
//	二进制帧 —— 裸字节。hub→浏览器是 PTY 输出，浏览器→hub 是键盘输入。
//	             不走 JSON 是为了省掉转义和 base64，终端输出是热路径。
//
// 二进制帧里没有会话 id：一个浏览器同一时刻只绑定一个会话，
// 所以「当前绑定的那个」就是唯一可能的收件人。
package protocol

import "encoding/json"

// MsgType 是控制消息的类型。
type MsgType string

const (
	// 浏览器 → hub
	TypeList     MsgType = "list"     // 要会话列表
	TypeAttach   MsgType = "attach"   // 绑定到某个会话
	TypeDetach   MsgType = "detach"   // 解绑
	TypeResize   MsgType = "resize"   // 改尺寸（仅控制方有效）
	TypeTakeover MsgType = "takeover" // 接管
	TypeRelease  MsgType = "release"  // 交还控制权
	TypeStart    MsgType = "start"
	TypeStop     MsgType = "stop"
	TypeRestart  MsgType = "restart"
	TypeResync   MsgType = "resync" // 请求整段重发回放
	// TypeRepaint 请后端把 PTY 的行列数抖一下，逼里面的程序整屏重画。
	//
	// 回放给浏览器的是字节日志，不是屏幕快照。claude code 这类程序只发
	// **相对**的擦除和重写，从日志中间开始重放，浏览器上的画面就永远跟它
	// 以为的画面错开了——而且不会自己纠正。抖一下尺寸会让它重绘整屏。
	TypeRepaint MsgType = "repaint"

	// TypeUpload 把一张图存到 hub 那台机器上。
	//
	// 走文本帧而不是二进制帧：二进制帧那条路已经被键盘输入占满了，
	// 而发图是低频操作，多 33% 的 base64 开销换一条独立的通道，值。
	TypeUpload MsgType = "upload"

	// TypeRegisterSecret 让 agent 把「登陆密钥」登记到中央站。
	//
	// 为什么要有它：浏览器登录用的密钥必须存在中央站（relay 每次登录都要比对），
	// 但不能由你手动填到服务器上——那等于密钥要经手第二个人。
	// 让桌面端自己生成、自己登记，密钥就只在这一条消息里明文出现一次。
	//
	// 只在 agent 连接的生命周期内有效：agent 一断，这条 key 下的密钥登记
	// 也跟着失效（浏览器再登录会被拒），直到 agent 重连再登记一次。
	// 这是刻意的——agent 不在线时，没有人该能进那台机器的终端。
	TypeRegisterSecret MsgType = "register_secret"

	// TypeRegistered relay 回复：密钥登记成功。
	TypeRegistered MsgType = "registered"

	// TypeRegisterRejected relay 回复：登记被拒。
	//
	// 和 TypeError 分开：这条专指登记失败，Msg 里说明原因
	//（key 不存在 / 已过期 / 已吊销）。agent 收到后应当把原因显示在设置面板里，
	// 而不是笼统的「连接失败」——那三种原因在界面上长得一样，排查时完全分不清。
	TypeRegisterRejected MsgType = "register_rejected"

	// hub → 浏览器
	TypeSessions MsgType = "sessions"
	TypeAttached MsgType = "attached"
	// TypeUploaded 图存好了，Path 是它在 hub 那台机器上的位置。
	//
	// 只回路径、不代插终端：路径要不要裹成 bracketed paste 取决于终端里的
	// 程序有没有开那个模式，而那个状态只有浏览器那侧的 xterm 知道。
	TypeUploaded MsgType = "uploaded"
	TypeLease    MsgType = "lease" // 控制方变了
	TypeSize     MsgType = "size"  // PTY 尺寸变了
	TypeStarted  MsgType = "started"
	TypeExit     MsgType = "exit"
	TypeError    MsgType = "error"

	// relay 合成，插给浏览器
	TypeAgentOnline  MsgType = "agent_online"  // 本地 hub 上线了
	TypeAgentOffline MsgType = "agent_offline" // 本地 hub 掉线了
	TypeKicked       MsgType = "kicked"        // 你被新连接顶掉了，随后以 4001 关闭

	// relay 合成，插给 agent
	TypeBrowserOnline  MsgType = "browser_online"
	TypeBrowserOffline MsgType = "browser_offline"
)

// 自定义关闭码。客户端必须区别对待 4001：被顶掉的客户端不许自动重连，
// 否则两台手机会互相踢成无限循环。
const (
	CloseKicked        = 4001 // 浏览器被**另一台设备**顶掉
	CloseAgentReplaced = 4002 // agent 被新连接顶掉
	CloseUnauthorized  = 4003
	CloseSlowConsumer  = 4004 // 对端读得太慢，主动断开让它重连重同步

	// CloseSuperseded 是同一个浏览器自己换了一条连接（页面重载、网络抖动、
	// 旧 socket 还没被回收就被新的顶掉了）。
	//
	// 跟 CloseKicked 必须分开：4001 的意思是「别的设备抢走了」，
	// 客户端要停下来别再抢；而这一条只是自己换了条线，**绝不能重连**——
	// 重连就会把刚建立的那条顶掉，旧的再重连，来回踢成死循环。
	CloseSuperseded = 4005

	// CloseKeyRevoked 是这个 key 被吊销/删除了，中央站主动踢掉它的所有连接。
	//
	// 客户端收到后应当停止重连：再连也是同一个 key，还是会被拒。
	// 界面上要显示「这个 key 已失效」，而不是笼统的「连接失败」。
	CloseKeyRevoked = 4006
)

// Controller 的取值。
const (
	ControllerDesktop = "desktop"
	ControllerWeb     = "web"
)

// SessionInfo 是会话列表里的一项。
//
// 字段来自两处拼接：store 提供 ID/Name/Kind（全部已定义的会话），
// hub 提供 Running/ExitCode/ErrMsg（本轮跑过的才有）。
type SessionInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Running  bool   `json:"running"`
	ExitCode int    `json:"exitCode"`
	ErrMsg   string `json:"errMsg"`
}

// Message 是所有控制消息的统一信封。
//
// 用一个宽结构体而不是每种消息一个类型：消息种类不多，字段大多可选，
// 这样 relay 转发时不需要理解内容，直接原样透传即可。
type Message struct {
	Type MsgType `json:"t"`

	ID         string `json:"id,omitempty"`
	Controller string `json:"controller,omitempty"`
	Cols       uint16 `json:"cols,omitempty"`
	Rows       uint16 `json:"rows,omitempty"`

	Sessions []SessionInfo `json:"sessions,omitempty"`

	// Data 是图片的 base64，Path 是 hub 存好之后的位置。
	Data      string `json:"data,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	Path      string `json:"path,omitempty"`

	// Secret 是 register_secret 携带的登陆密钥。
	//
	// 单独一个字段而不塞进 Msg：Msg 会出现在各种日志和错误回执里，
	// 而这个值是浏览器登录的凭据，不能有任何多余的地方接触到它。
	Secret string `json:"secret,omitempty"`

	// Key 是这次操作对应的使用 key。relay 用它在数据库里查归属，
	// 出错时也靠它说清是哪个 key 出了问题。
	Key string `json:"key,omitempty"`

	Code   int    `json:"code,omitempty"`
	ErrMsg string `json:"errMsg,omitempty"`
	Msg    string `json:"msg,omitempty"`
}

// Encode 序列化成文本帧的载荷。
func Encode(m Message) ([]byte, error) {
	return json.Marshal(m)
}

// Decode 解析文本帧。解析不了的消息由调用方决定是忽略还是回 error。
func Decode(b []byte) (Message, error) {
	var m Message
	err := json.Unmarshal(b, &m)
	return m, err
}
