// 与本地 hub 的 WebSocket 客户端。
//
// 线协议见 remote/protocol/protocol.go：文本帧是 JSON 控制消息，
// 二进制帧是裸字节（收 = PTY 输出，发 = 键盘输入）。

export type SessionInfo = {
  id: string
  name: string
  kind: string
  running: boolean
  exitCode: number
  errMsg: string
}

export type ControlMessage = {
  t: string
  id?: string
  controller?: string
  cols?: number
  rows?: number
  sessions?: SessionInfo[]
  code?: number
  errMsg?: string
  msg?: string
}

/** 关闭码，必须和 protocol.go 里的常量一致。 */
export const CLOSE_KICKED = 4001
export const CLOSE_SUPERSEDED = 4005

export type ConnectionState =
  | 'connecting'
  | 'open'
  | 'closed'
  /** 被**另一台设备**顶掉了。要停下来等用户决定，不能自动重连。 */
  | 'kicked'
  /** 被**同一个浏览器**的另一条连接顶掉了。同样不能重连——重连就是死循环。 */
  | 'superseded'

type Handlers = {
  onState?: (state: ConnectionState, detail?: string) => void
  onControl?: (m: ControlMessage) => void
  onOutput?: (bytes: Uint8Array) => void
  /** 本地 hub 是否在线（由 relay 插的合成消息）。 */
  onAgentOnline?: (online: boolean) => void
}

export class HubClient {
  private ws: WebSocket | null = null
  private timer: number | null = null
  private backoff = 1000
  private stopped = false
  /** 本次连接是不是人手动发起的。见 reconnectNow。 */
  private force = false

  /**
   * 已经确认绑定的会话 id。
   *
   * 二进制帧里不带会话 id（一次只绑一个），所以切会话时旧会话的尾巴可能
   * 晚到并画到新会话的屏幕上。这个变量就是闸门：没确认之前一律丢弃。
   */
  private attachedId: string | null = null

  constructor(private handlers: Handlers) {}

  /** 自动连（页面加载时）。 */
  connect() {
    this.stopped = false
    this.force = false
    this.open()
  }

  /**
   * 人手动点的重连。
   *
   * 带上 force=1：服务端那道「刚被踢过就别马上回来」的闸是拦**自动**重连的，
   * 拦到手动重连上只会让人以为坏了。人点这个按钮的意思是「我就要这个位置」。
   */
  reconnectNow() {
    if (this.timer !== null) {
      window.clearTimeout(this.timer)
      this.timer = null
    }
    this.stopped = false
    this.force = true
    // open() 里会先关掉旧 socket，所以连点也不会开出两条
    this.open()
  }

  private open() {
    // ⚠️ 开新连接之前先关掉旧的。
    //
    // 不关的话会出现「两个 socket 并存」：旧的那条被服务端顶掉时触发 onclose，
    // 而 onclose 里的重连逻辑会再开一条 —— 新的顶掉旧的、旧的又开新的，
    // 无限循环。这就是日志里连接成对出现的来源。
    this.closeSocket()

    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const query = this.force ? '?force=1' : ''
    const ws = new WebSocket(`${proto}//${location.host}/ws/browser${query}`)
    ws.binaryType = 'arraybuffer'
    this.ws = ws

    this.handlers.onState?.('connecting')

    // 每个回调第一件事都是确认「这条 socket 还是当前那条」。
    // 陈旧 socket 的事件一律丢弃——否则它会污染新连接的状态。
    const stale = () => this.ws !== ws

    ws.onopen = () => {
      if (stale()) return
      this.backoff = 1000
      this.handlers.onState?.('open')
    }

    ws.onmessage = (ev) => {
      if (stale()) return
      if (typeof ev.data === 'string') {
        let m: ControlMessage
        try {
          m = JSON.parse(ev.data)
        } catch {
          return
        }
        this.handleControl(m)
        return
      }
      // 没确认绑定之前，输出一律丢掉
      if (this.attachedId === null) return
      this.handlers.onOutput?.(new Uint8Array(ev.data as ArrayBuffer))
    }

    ws.onclose = (ev) => {
      // 陈旧 socket 关了不算数：它可能是被新连接顶掉的那条，
      // 而新连接活得好好的。
      if (stale()) return

      this.ws = null
      this.attachedId = null

      if (ev.code === CLOSE_KICKED) {
        // ⚠️ 被**另一台设备**顶掉之后绝不能自动重连：两台设备会互相踢成无限循环。
        // 服务端还有一道 5 秒窗口的闸，但客户端这道不能省。
        this.stopped = true
        this.handlers.onState?.('kicked', ev.reason)
        return
      }

      if (ev.code === CLOSE_SUPERSEDED) {
        // ⚠️ 同理，而且这条更危险：这是同一个浏览器自己的新连接把旧连接顶掉了。
        // 这里要是重连，就会把刚建立的那条再顶掉，来回踢成死循环。
        this.stopped = true
        this.handlers.onState?.('superseded', ev.reason)
        return
      }

      this.handlers.onState?.('closed', ev.reason)
      if (!this.stopped) this.scheduleReconnect()
    }

    ws.onerror = () => {
      // onclose 紧随其后，重连逻辑放在那里
    }
  }

  /** 关掉当前 socket，并把它的回调失效（置 null 之后 stale() 就成立了）。 */
  private closeSocket() {
    const ws = this.ws
    this.ws = null
    if (ws) {
      try {
        ws.close()
      } catch {
        // 已经关了，无所谓
      }
    }
  }

  private scheduleReconnect() {
    if (this.timer !== null) return
    const wait = this.backoff
    this.backoff = Math.min(this.backoff * 2, 30000)
    this.timer = window.setTimeout(() => {
      this.timer = null
      if (!this.stopped) this.open()
    }, wait)
  }

  private handleControl(m: ControlMessage) {
    switch (m.t) {
      case 'attached':
        this.attachedId = m.id ?? null
        // 刚绑上来，随后紧跟的是**字节日志**回放，不是屏幕快照。全屏/增量渲染的
        // 程序（claude code 就是）从日志中间重放，重建不出它的当前画面——手机上是
        // 一片黑或者两帧叠字。如果上来就是控制方，请后端抖一下尺寸逼它重画。
        if (m.controller === 'web') this.repaint()
        break
      case 'agent_online':
        this.handlers.onAgentOnline?.(true)
        break
      case 'agent_offline':
        // 本地 hub 掉了。保持自己的连接不动，hub 回来就能接着用，
        // 不用重新登录。同时清掉绑定状态，免得旧输出画上来。
        this.attachedId = null
        this.handlers.onAgentOnline?.(false)
        break
      case 'resync':
        // 落后太多，服务端要整段重发。先清屏，否则会在错误的画面上续写。
        this.handlers.onOutput?.(RESET_MARKER)
        // 清完屏重放的那一段同样是字节日志的尾巴，画面照样对不上，得让程序重画
        this.repaint()
        break
    }
    this.handlers.onControl?.(m)
  }

  private send(m: ControlMessage) {
    if (this.ws?.readyState !== WebSocket.OPEN) return
    this.ws.send(JSON.stringify(m))
  }

  /** 发输入。走二进制帧，省掉 JSON 转义。 */
  sendInput(bytes: Uint8Array | string) {
    if (this.ws?.readyState !== WebSocket.OPEN) return
    const data = typeof bytes === 'string' ? new TextEncoder().encode(bytes) : bytes
    this.ws.send(data)
  }

  list() {
    this.send({ t: 'list' })
  }

  attach(id: string) {
    this.attachedId = null
    this.send({ t: 'attach', id })
  }

  detach() {
    this.attachedId = null
    this.send({ t: 'detach' })
  }

  resize(cols: number, rows: number) {
    this.send({ t: 'resize', cols, rows })
  }

  takeover() {
    this.send({ t: 'takeover' })
  }

  release() {
    this.send({ t: 'release' })
  }

  /**
   * 请后端把 PTY 的行列数抖一下，逼里面的程序整屏重画。
   *
   * 只在「我这边画面确定跟真实画面脱节」时发：刚 attach、收到 resync、刚接管。
   * 不是控制方的话后端会忽略——尺寸是控制方的，旁观者没资格动。
   */
  repaint() {
    this.send({ t: 'repaint' })
  }

  start(id: string) {
    this.send({ t: 'start', id })
  }

  stop(id: string) {
    this.send({ t: 'stop', id })
  }

  restart(id: string) {
    this.send({ t: 'restart', id })
  }

  close() {
    this.stopped = true
    if (this.timer !== null) {
      window.clearTimeout(this.timer)
      this.timer = null
    }
    this.closeSocket()
  }
}

/**
 * 一个特殊的输出回调值，表示「先清屏再写」。
 *
 * 用 Uint8Array 而不是布尔回调，是为了让它和真实输出走同一条路——
 * 顺序才不会乱。TerminalView 里靠 `=== RESET_MARKER` 分辨。
 */
export const RESET_MARKER = new Uint8Array(0)
