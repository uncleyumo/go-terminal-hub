// 每会话的终端背景色（D48，方案 C：只让用户挑**背景**这一个颜色，
// 其余的——文字色、光标、选中底色、ANSI 十六色该用哪一套——全部由程序按背景的亮暗定）。
//
// 为什么存 localStorage 而不是进后端 data.json：
// 它是「看着舒服一点」的东西，丢了不影响这条会话能不能跑；进后端就得跟着
// 备份、迁移、跟着 Store.Update 的整体替换一起写回。放在前端只有一处要维护。
// ⚠️ 代价是**换 origin 就丢**：dev 跑在 http://wails.localhost:5173，
// bin 跑在 http://wails.localhost，两边的 localStorage 不是一份（D46 查证过）。
// 这个 app 不跨设备同步，所以认了。
import type { ITheme } from '@xterm/xterm'

const KEY = 'hub.session.backgrounds'
// 全局默认底色。跟每会话那份**分开存**：会话那份是「这条单独改的」，
// 存在一起的话清掉一条会话就把它对全局的改也一起清了。
const APP_KEY = 'hub.app.terminalBackground'

type Backgrounds = Record<string, string>

// 底色偏亮时文字用深色，偏暗时用浅色。这两个是候选，实际选哪个看对比度。
const DARK_TEXT = '#0e1117'
const LIGHT_TEXT = '#e6e9f0'

export function isHex(value: string): boolean {
  return /^#[0-9a-fA-F]{6}$/.test(value)
}

/** 大写 / 缺 # / 缩写都收成 `#rrggbb`；实在认不出来返回 null */
export function normalizeHex(value: string): string | null {
  let s = value.trim().toLowerCase()
  if (!s.startsWith('#')) s = `#${s}`
  if (/^#[0-9a-f]{3}$/.test(s)) {
    s = `#${s[1]}${s[1]}${s[2]}${s[2]}${s[3]}${s[3]}`
  }
  return isHex(s) ? s : null
}

export function hexToRgb(hex: string): { r: number; g: number; b: number } {
  return {
    r: parseInt(hex.slice(1, 3), 16),
    g: parseInt(hex.slice(3, 5), 16),
    b: parseInt(hex.slice(5, 7), 16),
  }
}

/**
 * HSV → RGB。h 是 0..360，s / v 是 0..100。
 *
 * 色相用 HSV 表示而不直接用 RGB：色相是一个**绕一圈**的值，0 和 360 是同一个红，
 * 拖色相条的时候得能一路拖过去。用 RGB 的话红色会被劈成两半（0 和 360 各一半）。
 */
export function hsvToRgb(
  h: number,
  s: number,
  v: number,
): { r: number; g: number; b: number } {
  const sn = s / 100
  const vn = v / 100
  const c = vn * sn
  const hp = ((((h % 360) + 360) % 360) / 60)
  const x = c * (1 - Math.abs((hp % 2) - 1))
  let r = 0
  let g = 0
  let b = 0
  if (hp < 1) [r, g, b] = [c, x, 0]
  else if (hp < 2) [r, g, b] = [x, c, 0]
  else if (hp < 3) [r, g, b] = [0, c, x]
  else if (hp < 4) [r, g, b] = [0, x, c]
  else if (hp < 5) [r, g, b] = [x, 0, c]
  else [r, g, b] = [c, 0, x]
  const m = vn - c
  return {
    r: Math.round((r + m) * 255),
    g: Math.round((g + m) * 255),
    b: Math.round((b + m) * 255),
  }
}

export function rgbToHsv(r: number, g: number, b: number): { h: number; s: number; v: number } {
  const rn = r / 255
  const gn = g / 255
  const bn = b / 255
  const max = Math.max(rn, gn, bn)
  const min = Math.min(rn, gn, bn)
  const d = max - min
  let h = 0
  if (d !== 0) {
    if (max === rn) h = 60 * (((gn - bn) / d) % 6)
    else if (max === gn) h = 60 * ((bn - rn) / d + 2)
    else h = 60 * ((rn - gn) / d + 4)
  }
  if (h < 0) h += 360
  return { h, s: max === 0 ? 0 : (d / max) * 100, v: max * 100 }
}

function toLinear(channel: number): number {
  const c = channel / 255
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
}

/**
 * WCAG 相对亮度：先把 sRGB 每一通道转成线性再加权。
 * 「看起来亮」和「算出来亮」不是一回事——直接拿 0.299R+0.587G+0.114B
 * 会把 #7f7f7f 这种中灰算得过亮，浅底深字的界线就偏了。
 */
export function relativeLuminance(hex: string): number {
  const { r, g, b } = hexToRgb(hex)
  return 0.2126 * toLinear(r) + 0.7152 * toLinear(g) + 0.0722 * toLinear(b)
}

function contrastRatio(a: string, b: string): number {
  const la = relativeLuminance(a)
  const lb = relativeLuminance(b)
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05)
}

/**
 * 这块底色上，深字和浅字哪个更读得清 —— 用对比度算，不拍脑袋定阈值。
 * 顺带这也是「底色算不算亮」的判据：深字更清楚 = 底色亮。
 */
export function prefersDarkText(background: string): boolean {
  return contrastRatio(background, DARK_TEXT) > contrastRatio(background, LIGHT_TEXT)
}

/**
 * 把一套现成的主题按新的背景色重算一遍。
 *
 * - `foreground` / `cursor`：跟着背景亮暗翻，深底浅字、浅底深字。
 * - `cursorAccent`：光标是一块实心的，它底下那个字符要能看见 —— 所以取背景色本身。
 * - `selectionBackground`：背景色的半透明版。选中的时候是盖在原文上的一层色，
 *   用半透明就不用管底下是深是浅，也不用另外调一个值。
 * - **ANSI 十六色不动**：那是输出自身的颜色，用户没要求改就别改（D48）。
 *   哪一套亮、哪一套暗由 `base` 决定，调用方按背景色挑好传进来。
 */
export function withBackground(base: ITheme, background: string): ITheme {
  const darkText = prefersDarkText(background)
  const foreground = darkText ? DARK_TEXT : LIGHT_TEXT
  const { r, g, b } = hexToRgb(background)
  return {
    ...base,
    background,
    foreground,
    cursor: foreground,
    cursorAccent: background,
    selectionBackground: `rgba(${r}, ${g}, ${b}, 0.32)`,
  }
}

function readAll(): Backgrounds {
  try {
    const raw = window.localStorage.getItem(KEY)
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    // 存的东西可能被人手改过、可能是老版本留下的别的形状 —— 不是对象就当没有。
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const out: Backgrounds = {}
    for (const [id, value] of Object.entries(parsed)) {
      if (typeof value === 'string') {
        const hex = normalizeHex(value)
        if (hex) out[id] = hex
      }
    }
    return out
  } catch {
    // localStorage 不可用（隐私模式之类）时它是会抛的。存不下就不存，别的照常用。
    return {}
  }
}

let cache: Backgrounds | null = null

function all(): Backgrounds {
  if (cache === null) cache = readAll()
  return cache
}

/** 这条会话有没有设过背景色。没设过返回 null —— 不是默认色，是「别管它」 */
export function getBackground(id: string): string | null {
  return all()[id] ?? null
}

/** 传 null = 撤掉这条会话的设置，回到主题自带的配色 */
export function setBackground(id: string, hex: string | null): void {
  const next: Backgrounds = { ...all() }
  if (hex === null) delete next[id]
  else next[id] = normalizeHex(hex) ?? next[id] ?? '#000000'
  cache = next
  try {
    window.localStorage.setItem(KEY, JSON.stringify(next))
  } catch {
    // 存不进去只是下次不记住，这个会话现在照样是那个颜色
  }
}

let appCache: string | null | undefined

/** 全局默认底色。null = 没设过，跟随主题 */
export function getAppBackground(): string | null {
  if (appCache !== undefined) return appCache
  try {
    const raw = window.localStorage.getItem(APP_KEY)
    appCache = raw ? normalizeHex(raw) : null
  } catch {
    appCache = null
  }
  return appCache
}

export function setAppBackground(hex: string | null): void {
  appCache = hex === null ? null : normalizeHex(hex)
  try {
    if (appCache === null) window.localStorage.removeItem(APP_KEY)
    else window.localStorage.setItem(APP_KEY, appCache)
  } catch {
    // 同上：存不进去只是下次不记住
  }
}
