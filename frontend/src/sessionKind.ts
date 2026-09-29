// 「常驻终端」这两个类型只存在于前端：后端白名单不认它们（executor.go 的 Kind 校验），
// 提交时翻译成 kind=shell + 拼好的 target（D39，后端零改动）。
// target 的值就是「起一个常驻 shell」的那条命令 —— /k 的意思是执行完不退出，
// PowerShell 不带 -Command 也是同一个效果。
//
// 代价：落盘之后 target 和「用户当时选的是哪一种」不是一一对应地被记下来了 ——
// 任何人手写一条一样的命令也会得到同样的记录。所以这里是**反着猜**：
// target 跟哪一条预设完全一样，就当它是那一种；不一样就还是 shell。
export const TERMINAL_TARGETS: Record<string, string> = {
  'terminal-cmd': 'cmd.exe /k',
  'terminal-powershell': 'powershell.exe -NoLogo -NoProfile',
}

/** 归一化到「能拿来比字符串」：去首尾空白、中间连续空白压成一个、小写。
 *  用户在表单里手打出来的 target 难免多一个空格，不归一就会漏判。 */
function normalize(value: string): string {
  return value.trim().replace(/\s+/g, ' ').toLowerCase()
}

/** 落盘的值（kind + target）→ 前端的类型。列表、详情、表单回填都用它。 */
export function resolveKind(kind: string, target: string): string {
  if (kind !== 'shell') return kind
  const wanted = normalize(target ?? '')
  for (const [frontendKind, preset] of Object.entries(TERMINAL_TARGETS)) {
    if (normalize(preset) === wanted) return frontendKind
  }
  return kind
}

/** 常驻终端：既不用 target 也不用 args（那条命令前端已经拼好了）。 */
export function isTerminalKind(kind: string): boolean {
  return TERMINAL_TARGETS[kind] !== undefined
}
