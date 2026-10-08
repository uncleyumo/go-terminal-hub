// 按键工具条的定义。
//
// 手机软键盘上没有终端常用的那些键：Esc、Tab、方向键、退格、Ctrl 组合。
// 这一排按钮直接往 PTY 里写对应的控制字节。
//
// 分两层：
//   常驻层 —— 每时每刻都要用的（Esc/Tab/退格/方向/回车/换行）
//   展开层 —— 点「更多按键」才出来，主要是 Ctrl 组合和符号
// 展开层每个键都带一句用途说明：不知道 C-r 是干什么的人，摆在那里也不会去点。
//
// 字节口径：
//   Esc=\x1b  Tab=\x09  Enter=\r  Shift+Tab=\x1b[Z
//   退格=\x7f（DEL，终端里的默认退格）  Del=\x1b[3~
//   方向键=\x1b[A/B/C/D，带 Shift 修饰是 \x1b[1;2A/B/C/D
//   Ctrl+字母 = 字母 & 0x1f（C-c=\x03、C-d=\x04、C-z=\x1a…）

export type KeyDef = {
  /** 主标签，比如 "C-c"。 */
  label: string
  /** 一句话说清这个键干什么。展开层必填——摆一排 C-xxx 没人知道是干嘛的。 */
  hint?: string
  /** 常规情况下发出的字节。 */
  data: string
  /** 粘滞 Shift 生效时改发这个。没写就还是 data。 */
  shiftData?: string
  /** 需要更宽的按钮（比如带说明的 Ctrl 键）。 */
  wide?: boolean
}

// —— 常驻层 ——

/** 第一行：最基础的编辑与导航。 */
export const ROW_EDIT: KeyDef[] = [
  { label: 'Esc', hint: '取消', data: '\x1b' },
  { label: 'Tab', hint: '补全', data: '\t', shiftData: '\x1b[Z' },
  { label: '⇧Tab', hint: '反向补全', data: '\x1b[Z' },
  { label: '⌫', hint: '退格', data: '\x7f' },
  { label: '⏎', hint: '回车', data: '\r' },
]

/** 第二行：方向键 + 删除 + 换行。 */
export const ROW_MOVE: KeyDef[] = [
  { label: '↑', data: '\x1b[A', shiftData: '\x1b[1;2A' },
  { label: '↓', data: '\x1b[B', shiftData: '\x1b[1;2B' },
  { label: '←', data: '\x1b[D', shiftData: '\x1b[1;2D' },
  { label: '→', data: '\x1b[C', shiftData: '\x1b[1;2C' },
  { label: 'Del', hint: '删光标处', data: '\x1b[3~' },
]

// —— 展开层 ——

/**
 * 换行：跟回车不是一回事。
 *
 * 回车是「发送」。写多行 prompt 时想换行又不提交，得用这三个之一——
 * 都是 Claude Code 认的（它的默认键位表里 `ctrl+j` 绑的是 newline，
 * `/terminal-setup` 给终端写的也是 `\x1b\r`）。
 *
 * ⚠️ **Ctrl+Enter 不是换行**：在 Claude Code 里它绑的是 `sendNow`，是「立刻发送」。
 * 别把它放进来，那正好是用户想避免的事。
 */
export const ROW_NEWLINE: KeyDef[] = [
  { label: 'Alt ⏎', hint: '换行（推荐）', data: '\x1b\r', wide: true },
  { label: 'C-j', hint: '换行', data: '\x0a', wide: true },
  { label: '\\ ⏎', hint: '反斜杠换行', data: '\\\r', wide: true },
]

/** Ctrl 组合。每个都标用途——不标的话这一排就是天书。 */
export const ROW_CTRL: KeyDef[] = [
  { label: 'C-c', hint: '中断', data: '\x03', wide: true },
  { label: 'C-d', hint: '退出 / EOF', data: '\x04', wide: true },
  { label: 'C-z', hint: '挂起', data: '\x1a', wide: true },
  { label: 'C-l', hint: '清屏', data: '\x0c', wide: true },
  { label: 'C-r', hint: '搜历史', data: '\x12', wide: true },
  { label: 'C-a', hint: '行首', data: '\x01', wide: true },
  { label: 'C-e', hint: '行尾', data: '\x05', wide: true },
  { label: 'C-u', hint: '删到行首', data: '\x15', wide: true },
  { label: 'C-k', hint: '删到行尾', data: '\x0b', wide: true },
  { label: 'C-w', hint: '删一个词', data: '\x17', wide: true },
]

/** 符号。手机键盘上打这些要来回切输入法，放一排省事。 */
export const ROW_SYMBOL: KeyDef[] = [
  { label: '|', data: '|' },
  { label: '~', data: '~' },
  { label: '/', data: '/' },
  { label: '-', data: '-' },
  { label: '_', data: '_' },
  { label: '\\', data: '\\' },
  { label: '`', data: '`' },
  { label: '^', data: '^' },
  { label: '&', data: '&' },
  { label: '*', data: '*' },
  { label: '>', data: '>' },
  { label: '<', data: '<' },
  { label: '$', data: '$' },
  { label: '=', data: '=' },
  { label: "'", data: "'" },
  { label: '"', data: '"' },
]

/**
 * 把粘滞 Ctrl 作用到一个字符上：字母 & 0x1f。
 *
 * 软键盘上敲的字符也走这里，所以「按 Ctrl 再敲 c」能出 \x03，
 * 不用非得点工具条上的 C-c。
 */
export function applyCtrl(ch: string): string {
  const code = ch.charCodeAt(0)
  if (code >= 97 && code <= 122) return String.fromCharCode(code - 96) // a-z
  if (code >= 64 && code <= 95) return String.fromCharCode(code - 64) // @A-Z[\]^_
  if (ch === ' ') return '\x00'
  if (ch === '?') return '\x7f'
  return ch
}
