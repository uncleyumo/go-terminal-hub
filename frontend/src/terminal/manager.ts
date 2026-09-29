// 终端实例归这里持有（D19）：实例和它的容器都由本模块创建，组件只负责把容器 append 进去 / 摘回来。
//
// 为什么不直接建在组件里：选中另一行、或详情区被卸载时，组件里的 DOM 会跟着销毁，
// 挂在它上面的 Terminal 实例也就没了，之前滚过的输出全丢。把实例留在这个模块里，
// 容器只是个可插拔的壳，换回来还能看到原来的内容。
import { Terminal, type ITheme } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { resizeSession, writeSession } from '../api'
import { getAppBackground, getBackground, prefersDarkText, withBackground } from './background'

interface Entry {
  term: Terminal
  container: HTMLDivElement
  fitAddon: FitAddon
  observer: ResizeObserver
  opened: boolean
}

const entries = new Map<string, Entry>()

/*
 * 终端配色**分主题**，但两个主题下都是深底 —— 读进程输出要的是输出自身的颜色
 * 跳出来，浅底会把 ANSI 配色（尤其偏亮的黄/青）洗掉。
 * 浅色主题下用一档**偏蓝的深炭灰**而不是死黑：贴着白底看，纯黑像一个挖空的洞，
 * 这档颜色跟浅色主题的冷中性色系是一家人，看着是「一块终端」而不是「一片空白」。
 */
const PALETTES: Record<'light' | 'dark', ITheme> = {
  dark: {
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
  },
  light: {
    // 浅色主题下终端也是浅色。底色跟 :root[data-theme='light'] 的 --c-sunken
    // 取同一个值（#edeff3），终端就跟窗口是一块，不是浮在浅色界面里的一块黑。
    // ANSI 十六色按浅底重新挑过：亮色在浅底上看不见，所以 yellow/white 压暗、
    // brightBlack 提亮，两头都要顾。2026-09-28 学习者指出原来那套「死黑」不能要。
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
  },
}

/** 跟着 <html data-theme> 走；没写过就当深色 */
function appTheme(): 'light' | 'dark' {
  return document.documentElement.dataset.theme === 'light' ? 'light' : 'dark'
}

/**
 * 这条会话现在该用哪套配色。三层，后者盖前者：
 *
 *   1. 这条会话自己设过的底色      —— 会话工具条里改的，只影响这一条
 *   2. 全局默认底色                —— 顶栏主题菜单里改的，影响所有**没单独设过**的会话
 *   3. 主题自带的那套              —— 浅色 / 深色
 *
 * 底色一旦定了，**亮暗说了算**：亮底配浅色主题那套 ANSI 色，暗底配深色那套。
 * 不跟着底色走的话，浅底上那套为深底调的淡黄、淡青会洗掉看不见。
 * 用户只该挑一个颜色，剩下的由程序定（D48）。
 */
function themeFor(id: string, app: 'light' | 'dark' = appTheme()): ITheme {
  const custom = getBackground(id) ?? getAppBackground()
  if (!custom) return PALETTES[app]
  const base = prefersDarkText(custom) ? PALETTES.light : PALETTES.dark
  return withBackground(base, custom)
}

/** 切主题时把**已经建好**的终端一起换掉，否则旧会话会留着上一套颜色 */
export function setTerminalPalette(theme: 'light' | 'dark') {
  for (const [id, entry] of entries) {
    entry.term.options.theme = themeFor(id, theme)
  }
}

/**
 * 改某条会话的底色。没建过终端（还没打开过）就什么都不做 ——
 * 它下次建的时候自己会读 localStorage。
 */
export function setSessionBackground(id: string, hex: string | null) {
  const entry = entries.get(id)
  if (!entry) return
  entry.term.options.theme = themeFor(id)
}

/**
 * 改全局默认底色。**全部**已经建好的终端都重算一遍 ——
 * 单独设过底色的那几条算出来还是它们自己的（themeFor 里先看会话那份），
 * 所以这一遍不会把用户单独做的选择刷掉。
 */
export function applyAppBackground() {
  for (const [id, entry] of entries) {
    entry.term.options.theme = themeFor(id)
  }
}

function ensure(id: string): Entry {
  let entry = entries.get(id)
  if (!entry) {
    const container = document.createElement('div')
    // 这个元素不是组件模板里的，scoped 样式够不到它，尺寸只能在这里写死。
    // 用 absolute + inset:0 而不是 width/height:100%：后者算的是**内容盒**，
    // xterm 自己那层还要加 padding，底边会差出一两像素露出生色。
    // 宿主（TerminalPane 的根）是 relative，绝对定位正好铺满它。
    container.style.position = 'absolute'
    container.style.inset = '0'
    const term = new Terminal({
      fontSize: 13,
      scrollback: 5000,
      fontFamily: '"Cascadia Code", "Cascadia Mono", Consolas, "Microsoft YaHei", monospace',
      theme: themeFor(id),
    })
    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    // 键盘这一路：xterm 把按键编成字节串交出来，原样送给后端。
    // xterm 自己不做回显 —— 屏幕上看到什么完全由进程的输出决定，
    // 所以对着一个已经停掉的会话打字不会有任何反应，也不会报错。
    term.onData((data) => {
      writeSession(id, data).catch((error) => {
        // 会话没在跑时后端会报错。那是用户正常操作，不值一提。
        console.debug('write to session failed', error)
      })
    })
    // Ctrl+V 得自己接一下，但**只做一半**。
    // 现象（2026-09-28 实测）：按一次 Ctrl+V 粘出来**两份**。
    // 原因：浏览器的原生 paste 事件照样落在 xterm 的隐藏 textarea 上，xterm 自己粘一份；
    // 我们再从剪贴板读一次粘一份 —— 两条路都通，就重了。
    // 而在加这段之前，Ctrl+V 是被 xterm 当普通按键、把字节 0x16 发给后端的
    // （cmd 收到控制字符，显示成 ^V，往正在输入的那行里插垃圾字符）。
    //
    // 所以这里**只**返回 false 让 xterm 别把 Ctrl+V 编成字节发出去，
    // **不调 preventDefault** —— 让浏览器那次原生 paste 照常发生，由 xterm 粘。
    // 净效果：只有一份，垃圾字符也没了。
    // Shift+Insert / Ctrl+Insert 走 xterm 自带的绑定，不受影响。
    term.attachCustomKeyEventHandler((event) => {
      if (!event.ctrlKey || event.shiftKey || event.altKey) return true
      // 用 code 而不是 key：code 是物理键位，不受输入法布局影响
      if (event.code !== 'KeyV') return true
      return false
    })
    const observer = new ResizeObserver(() => scheduleFit(id))
    entry = { term, container, fitAddon, observer, opened: false }
    entries.set(id, entry)
  }
  return entry
}

// 把这一行的容器挂进 host。第一次挂的时候才 open()：
// open() 要量元素尺寸，元素还没进 DOM 就量到 0，终端会白屏而且不报错。
export function attach(id: string, host: HTMLElement) {
  const entry = ensure(id)
  host.appendChild(entry.container)
  if (!entry.opened) {
    entry.term.open(entry.container)
    entry.opened = true
    // 观察只挂一次。容器被 detach 之后尺寸会变成 0，那次测量在 fit() 里挡掉。
    entry.observer.observe(entry.container)
  }
  // xterm 靠一个隐藏的 textarea 收键盘。不主动聚焦的话，用户得先点一下终端才敲得进去。
  entry.term.focus()
  // 容器刚进 DOM，尺寸还没落定，所以攒一下再量。
  scheduleFit(id)
}

// 只摘容器，不销毁实例 —— 下次再选中这一行，之前的输出还在。
export function detach(id: string) {
  entries.get(id)?.container.remove()
}

// 把这个画布的尺寸量出来，告诉后端的 ConPTY。
// ConPTY 建出来的时候是 80×25（go-pty 写死的默认值），不告诉它真实尺寸，
// 里面跑的程序就一直按 80 列排版 —— 界面拉宽了它也不动。
export function fit(id: string) {
  const entry = entries.get(id)
  // 没 open 过、或者容器已经被摘下来（尺寸是 0）的时候不量。
  if (!entry?.opened || !entry.container.isConnected) return
  try {
    entry.fitAddon.fit()
  } catch {
    // 容器尺寸还没落定时会抛。那是中间状态，等下一次尺寸变化再说。
    return
  }
  resizeSession(id, entry.term.cols, entry.term.rows).catch((error) => {
    // 会话没在跑的时候后端会报错。和 write 一样，那是用户正常操作。
    console.debug('resize session failed', error)
  })
}

// 拖窗口时尺寸一帧变一次，攒够 120ms 再发，别把后端打爆。
const fitTimers = new Map<string, number>()

function scheduleFit(id: string) {
  const timer = fitTimers.get(id)
  if (timer !== undefined) window.clearTimeout(timer)
  fitTimers.set(
    id,
    window.setTimeout(() => {
      fitTimers.delete(id)
      fit(id)
    }, 120),
  )
}

// 没实例的行也要收：进程一起来就吐输出，而实例是按需创建的——
// 用 entries.get(id)?. 的话，那些「没被选中过、也没自动启动过」的行，
// 输出会在到达时被静默丢掉。
// ensure() 只建实例、不 open()：内容先进 buffer，
// 等这一行被选中、attach() 里 open() 之后，buffer 里的东西照常渲染出来。
export function write(id: string, text: string) {
  ensure(id).term.write(text)
}

// 新起一轮之前清屏，别让上一轮的输出和新一轮混在一起。
// 没建过实例的行没有历史要清，这里不为它建实例。
export function clear(id: string) {
  entries.get(id)?.term.reset()
}

export function disposeAll() {
  for (const timer of fitTimers.values()) window.clearTimeout(timer)
  fitTimers.clear()
  for (const entry of entries.values()) {
    entry.observer.disconnect()
    entry.term.dispose()
    entry.container.remove()
  }
  entries.clear()
}
