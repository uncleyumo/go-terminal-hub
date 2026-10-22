// 终端配色方案（relay-hub 网页端）。
//
// 移植自桌面端：桌面端顶栏那个「主题」菜单管的是**整窗**的亮暗，
// 这里多给几套预设的配色包。两者不共享状态——网页端和桌面端是两个进程，
// 各存各的。
//
// 存哪：localStorage。relay 那边只存登录密钥和会话，主题是这台设备自己的
// 显示偏好，跟着浏览器走才对——换台设备看同一台机器，主题跟着设备走更自然。
//
// ⚠️ 主题只管「ANSI 十六色 + 前景/背景/光标」。
// 程序自己发的真彩色（claude code 就是这样，38;2;r;g;b）不受影响——
// xterm 拿不到那一层颜色，也就改不了它。所以浅色底下跑着深色调的程序，
// 那段程序自己的文字仍然是深色的。这是终端的规矩，不是这里的 bug。
import type { ITheme } from '@xterm/xterm'

export interface ThemePreset {
  id: string
  label: string
  /** 页面外壳（顶栏、列表、弹窗）走亮色还是暗色 */
  chrome: 'light' | 'dark'
  /** 终端配色 */
  terminal: ITheme
}

const DARK: ITheme = {
  background: '#07090d',
  foreground: '#d5dae6',
  cursor: '#4c8dff',
  cursorAccent: '#07090d',
  selectionBackground: '#2c3346',
  black: '#0a0c11',
  red: '#f4695f',
  green: '#3fb950',
  yellow: '#d8a53a',
  blue: '#4c8dff',
  magenta: '#b48ef0',
  cyan: '#4ec9c9',
  white: '#d5dae6',
  brightBlack: '#7d879b',
  brightWhite: '#e8ebf2',
}

const LIGHT: ITheme = {
  background: '#edeff3',
  foreground: '#2b313d',
  cursor: '#2159c9',
  cursorAccent: '#edeff3',
  selectionBackground: '#c3d3f0',
  black: '#2b313d',
  red: '#b23c30',
  green: '#1c7a3f',
  yellow: '#8a6100',
  blue: '#2159c9',
  magenta: '#7436c8',
  cyan: '#0b6f85',
  white: '#5b6474',
  brightBlack: '#8b93a3',
  brightWhite: '#3a4150',
}

export const THEME_PRESETS: ThemePreset[] = [
  { id: 'dark', label: '深色（默认）', chrome: 'dark', terminal: DARK },
  { id: 'light', label: '浅色（默认）', chrome: 'light', terminal: LIGHT },
  {
    id: 'dracula',
    label: 'Dracula',
    chrome: 'dark',
    terminal: {
      background: '#282a36',
      foreground: '#f8f8f2',
      cursor: '#f8f8f2',
      cursorAccent: '#282a36',
      selectionBackground: '#44475a',
      black: '#21222c',
      red: '#ff5555',
      green: '#50fa7b',
      yellow: '#f1fa8c',
      blue: '#bd93f9',
      magenta: '#ff79c6',
      cyan: '#8be9fd',
      white: '#bbbbbb',
      brightBlack: '#6272a4',
      brightRed: '#ff6e67',
      brightGreen: '#5aff5c',
      brightYellow: '#ffffa5',
      brightBlue: '#d6acff',
      brightMagenta: '#ff92df',
      brightCyan: '#a4ffff',
      brightWhite: '#ffffff',
    },
  },
  {
    id: 'nord',
    label: 'Nord',
    chrome: 'dark',
    terminal: {
      background: '#2e3440',
      foreground: '#d8dee9',
      cursor: '#d8dee9',
      cursorAccent: '#2e3440',
      selectionBackground: '#434c5e',
      black: '#3b4252',
      red: '#bf616a',
      green: '#a3be8c',
      yellow: '#ebcb8b',
      blue: '#81a1c1',
      magenta: '#b48ead',
      cyan: '#88c0d0',
      white: '#e5e9f0',
      brightBlack: '#4c566a',
      brightRed: '#bf616a',
      brightGreen: '#a3be8c',
      brightYellow: '#ebcb8b',
      brightBlue: '#81a1c1',
      brightMagenta: '#b48ead',
      brightCyan: '#8fbcbb',
      brightWhite: '#eceff4',
    },
  },
  {
    id: 'solarized-dark',
    label: 'Solarized Dark',
    chrome: 'dark',
    terminal: {
      background: '#002b36',
      foreground: '#839496',
      cursor: '#93a1a1',
      cursorAccent: '#002b36',
      selectionBackground: '#073642',
      black: '#073642',
      red: '#dc322f',
      green: '#859900',
      yellow: '#b58900',
      blue: '#268bd2',
      magenta: '#d33682',
      cyan: '#2aa198',
      white: '#eee8d5',
      brightBlack: '#002b36',
      brightRed: '#cb4b16',
      brightGreen: '#586e75',
      brightYellow: '#657b83',
      brightBlue: '#839496',
      brightMagenta: '#6c71c4',
      brightCyan: '#93a1a1',
      brightWhite: '#fdf6e3',
    },
  },
  {
    id: 'solarized-light',
    label: 'Solarized Light',
    chrome: 'light',
    terminal: {
      background: '#fdf6e3',
      foreground: '#657b83',
      cursor: '#586e75',
      cursorAccent: '#fdf6e3',
      selectionBackground: '#eee8d5',
      black: '#073642',
      red: '#dc322f',
      green: '#859900',
      yellow: '#b58900',
      blue: '#268bd2',
      magenta: '#d33682',
      cyan: '#2aa198',
      white: '#eee8d5',
      brightBlack: '#002b36',
      brightRed: '#cb4b16',
      brightGreen: '#586e75',
      brightYellow: '#657b83',
      brightBlue: '#839496',
      brightMagenta: '#6c71c4',
      brightCyan: '#93a1a1',
      brightWhite: '#fdf6e3',
    },
  },
  {
    id: 'gruvbox-dark',
    label: 'Gruvbox Dark',
    chrome: 'dark',
    terminal: {
      background: '#282828',
      foreground: '#ebdbb2',
      cursor: '#ebdbb2',
      cursorAccent: '#282828',
      selectionBackground: '#3c3836',
      black: '#282828',
      red: '#cc241d',
      green: '#98971a',
      yellow: '#d79921',
      blue: '#458588',
      magenta: '#b16286',
      cyan: '#689d6a',
      white: '#a89984',
      brightBlack: '#928374',
      brightRed: '#fb4934',
      brightGreen: '#b8bb26',
      brightYellow: '#fabd2f',
      brightBlue: '#83a598',
      brightMagenta: '#d3869b',
      brightCyan: '#8ec07c',
      brightWhite: '#ebdbb2',
    },
  },
]

const STORAGE_KEY = 'hub.remote.theme'

export function themeById(id: string): ThemePreset {
  return THEME_PRESETS.find((p) => p.id === id) ?? THEME_PRESETS[0]
}

/** 读上次选的那套。读不到（第一次来、隐私模式）就用默认深色。 */
export function loadThemeId(): string {
  try {
    return localStorage.getItem(STORAGE_KEY) ?? THEME_PRESETS[0].id
  } catch {
    return THEME_PRESETS[0].id
  }
}

/** 存下来。下次打开还是这一套。 */
export function saveThemeId(id: string) {
  try {
    localStorage.setItem(STORAGE_KEY, id)
  } catch {
    // 存不进去只是下次不记住，主题本身照样能用
  }
}

/** 把页面外壳的亮暗挂到 <html data-theme> 上，CSS 那边按它取色。 */
export function applyChrome(preset: ThemePreset) {
  document.documentElement.dataset.theme = preset.chrome
}
