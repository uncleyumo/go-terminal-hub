// 主题三选一：浅色 / 深色 / 跟随系统（D34）。
//
// 为什么不放后端：Settings.Theme 本来就是自由字符串（store.go 的 `Theme string`），
// 三种值直接写进去就行 —— **不用改 Go、不用重新生成绑定**。代价是后端不做值校验
// （settings.json 里写 "fr" 也原样返回），所以归一必须在这一层做。
import { setTerminalPalette } from './terminal/manager'

export type ThemeMode = 'light' | 'dark' | 'system'
/** 真正落到 CSS 上的只有这两个：system 在这里被解析掉 */
export type ResolvedTheme = 'light' | 'dark'

// 必须和 index.html 里那段内联脚本用同一个 key —— 首帧靠它上色，见 index.html 的注释
const CACHE_KEY = 'go-terminal-hub.theme'

export const DEFAULT_THEME: ThemeMode = 'system'

/** 任何从外部来的值（settings.json / 缓存）都要先过这一关，不认识的当默认 */
export function normalizeTheme(value: unknown): ThemeMode {
  return value === 'light' || value === 'dark' || value === 'system' ? value : DEFAULT_THEME
}

const query = () => window.matchMedia('(prefers-color-scheme: dark)')

export function resolveTheme(mode: ThemeMode): ResolvedTheme {
  if (mode === 'system') return query().matches ? 'dark' : 'light'
  return mode
}

let currentMode: ThemeMode = DEFAULT_THEME

export function getThemeMode(): ThemeMode {
  return currentMode
}

/**
 * 应用主题。
 * ① 解析 system → 落到 <html data-theme>；② 通知所有活着的终端换配色；
 * ③ 把解析结果缓存下来（首帧用，见 index.html）。
 */
export function applyTheme(mode: ThemeMode): ResolvedTheme {
  currentMode = mode
  const resolved = resolveTheme(mode)
  document.documentElement.dataset.theme = resolved
  // 已经建好的终端要一起换配色，否则旧会话留着上一套颜色
  setTerminalPalette(resolved)
  try {
    localStorage.setItem(CACHE_KEY, resolved)
  } catch {
    // 存不进去（隐私模式 / 协议不支持）不影响功能，只是首帧会闪一下
  }
  return resolved
}

// 系统主题在「跟随系统」时变了要跟着变。系统色变了不需要重新订阅，
// 这里只注册一次；模式不是 system 时回调里直接返回。
let watching = false

export function watchSystemTheme(onChange: () => void) {
  if (watching) return
  watching = true
  query().addEventListener('change', () => {
    if (currentMode === 'system') onChange()
  })
}
