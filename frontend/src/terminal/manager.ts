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

  // —— 控制权租约（手机远程接管）——
  //
  // 终端尺寸属于 PTY，不属于窗口。谁拿着控制权，PTY 就按谁的尺寸，
  // 其他人按 PTY 的真实网格渲染。
  //
  // viewer 为真表示这个会话当前由手机控制：本端必须
  //   1. 禁止键盘输入（disableStdin）
  //   2. 不再 fit()、不再上报尺寸——否则窗口一动就把手机的尺寸顶掉
  //   3. 把终端 resize 到 viewerSize，也就是 PTY 的真实网格
  viewer: boolean
  viewerSize: { cols: number; rows: number } | null

  // —— 显示大小 ——
  //
  // 字号是「这一眼」的事，不落盘、也不跨会话记：切走就把这一条恢复成默认
  // （见 detach）。所以它只活在这个 Entry 里，不进设置、不进 localStorage。
  fontSize: number
  /** 改字号时右上角闪一下的那个小标签，第一次用到才建。 */
  badge: HTMLDivElement | null
  badgeTimer: number | null
}

const entries = new Map<string, Entry>()

/** 终端字号。默认值也是「切走之后回到的那个值」。 */
const DEFAULT_FONT_SIZE = 13
const MIN_FONT_SIZE = 8
const MAX_FONT_SIZE = 32

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
      fontSize: DEFAULT_FONT_SIZE,
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
      if (event.code === 'KeyV') return false
      // 字号快捷键。这三个键在终端里没有别的含义，拿来当缩放正好。
      // 必须 preventDefault：WebView2 自己会拿它们去缩放**整个界面**，
      // 那会让菜单、侧栏一起变大，不是我们想要的。
      if (event.code === 'Equal' || event.code === 'Minus') {
        event.preventDefault()
        adjustFontSize(id, event.code === 'Equal' ? 1 : -1)
        return false
      }
      if (event.code === 'Digit0' || event.code === 'Numpad0') {
        event.preventDefault()
        applyFontSize(id, DEFAULT_FONT_SIZE)
        return false
      }
      return true
    })
    const observer = new ResizeObserver(() => scheduleFit(id))
    entry = {
      term,
      container,
      fitAddon,
      observer,
      opened: false,
      viewer: false,
      viewerSize: null,
      fontSize: DEFAULT_FONT_SIZE,
      badge: null,
      badgeTimer: null,
    }
    entries.set(id, entry)

    // Ctrl+滚轮改字号。必须用**捕获**阶段并且 stopPropagation：
    // xterm 自己也在监听滚轮（滚动回滚缓冲），而它的监听挂在更里层的
    // .xterm-viewport 上——不拦住的话一次滚轮既改字号又滚屏。
    container.addEventListener(
      'wheel',
      (event) => {
        if (!event.ctrlKey) return
        // 不拦的话 WebView2 会拿它缩放整个界面
        event.preventDefault()
        event.stopPropagation()
        adjustFontSize(id, event.deltaY < 0 ? 1 : -1)
      },
      { capture: true, passive: false },
    )
  }
  return entry
}

// —— 显示大小 ——

/** 把字号设成 size，按当前是不是旁观决定怎么重排。 */
function applyFontSize(id: string, size: number) {
  const entry = entries.get(id)
  if (!entry) return

  const next = Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, Math.round(size)))
  if (next === entry.fontSize) {
    // 已经顶到上下限了。还是闪一下——不然用户以为滚轮坏了。
    flashFontSize(entry, next)
    return
  }
  entry.fontSize = next
  entry.term.options.fontSize = next

  if (entry.viewer) {
    // 旁观时容器是按**网格的实际像素**撑开的，字号一变这个尺寸就过时了，
    // 得重新量一遍再居中。
    // 中间那次 resize 不是多余的：网格必须死死钉在 PTY 的尺寸上，
    // 不能因为换了字号就跟着容器漂走。
    if (entry.viewerSize) entry.term.resize(entry.viewerSize.cols, entry.viewerSize.rows)
    // 等一帧再量：xterm 换完字号，屏幕层的像素尺寸要下一次渲染才更新
    requestAnimationFrame(() => centerViewer(entry))
  } else {
    // 控制方：字号变了能放下的行列数也变了，重新量并上报。
    // 上报走的是 scheduleFit 里那条防抖，所以按住 Ctrl 连滚也只报最后一次。
    scheduleFit(id)
  }
  flashFontSize(entry, next)
}

/** 相对调整，滚轮和 Ctrl+= / Ctrl+- 走这条。 */
function adjustFontSize(id: string, delta: number) {
  const entry = entries.get(id)
  if (!entry) return
  applyFontSize(id, entry.fontSize + delta)
}

/** 右上角闪一下当前字号。改完就没提示的话，用户不知道自己滚到几了。 */
function flashFontSize(entry: Entry, size: number) {
  const host = entry.container.parentElement
  if (!host) return

  // 宿主会被组件卸载重建，旧的那个标签跟着一起没了，这时要重新建一个
  if (!entry.badge || !entry.badge.isConnected) {
    const badge = document.createElement('div')
    badge.style.cssText = [
      'position:absolute',
      'top:10px',
      'right:12px',
      'z-index:5',
      'padding:2px 10px',
      'border-radius:999px',
      'background:rgba(0,0,0,.7)',
      'color:#fff',
      'font-size:12px',
      'line-height:18px',
      'pointer-events:none',
      'opacity:0',
      'transition:opacity .15s',
    ].join(';')
    entry.badge = badge
  }
  host.appendChild(entry.badge)

  entry.badge.textContent = `字号 ${size}`
  entry.badge.style.opacity = '1'
  if (entry.badgeTimer !== null) window.clearTimeout(entry.badgeTimer)
  entry.badgeTimer = window.setTimeout(() => {
    entry.badgeTimer = null
    if (entry.badge) entry.badge.style.opacity = '0'
  }, 1200)
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
  if (entry.viewer && entry.viewerSize) {
    // 切回一个正被手机控制的会话：按 PTY 的真实网格渲染，不抢焦点也不量尺寸
    entry.term.resize(entry.viewerSize.cols, entry.viewerSize.rows)
    centerViewer(entry)
    return
  }

  // xterm 靠一个隐藏的 textarea 收键盘。不主动聚焦的话，用户得先点一下终端才敲得进去。
  entry.term.focus()
  // 容器刚进 DOM，尺寸还没落定，所以攒一下再量。
  scheduleFit(id)
}

// 只摘容器，不销毁实例 —— 下次再选中这一行，之前的输出还在。
export function detach(id: string) {
  const entry = entries.get(id)
  if (!entry) return

  // 字号不跨会话留着：切走就回到默认。留在 Entry 里的话，下次切回来
  // 会是个莫名其妙的放大终端，而用户早就忘了自己什么时候调过。
  // 直接改字段，不走 applyFontSize —— 那会顺手弹提示、还会触发一次 fit。
  if (entry.fontSize !== DEFAULT_FONT_SIZE) {
    entry.fontSize = DEFAULT_FONT_SIZE
    entry.term.options.fontSize = DEFAULT_FONT_SIZE
  }

  entry.container.remove()
}

// —— 控制权租约：旁观 / 控制两种模式 ——

/**
 * 切成旁观：手机接管了这个会话。
 *
 * 容器保持铺满宿主，但内容居中——xterm 的网格是 cols × cellWidth，
 * 量出真实像素尺寸再设到容器上，这样 48×20 的小终端会稳稳居中，
 * 而不是被拉伸变形。
 */
export function enterViewer(id: string, cols: number, rows: number) {
  const entry = ensure(id)
  entry.viewer = true
  entry.viewerSize = { cols, rows }
  // 旁观者敲键盘不该有任何反应。后端还有一道闸兜底，但让 xterm 自己别发
  // 更直接：用户不会遇到「敲了没反应还不知道为什么」。
  entry.term.options.disableStdin = true
  entry.term.resize(cols, rows)
  centerViewer(entry)
}

/** 拿回控制权：手机断开或桌面点了「收回控制」。 */
export function exitViewer(id: string) {
  const entry = entries.get(id)
  if (!entry || !entry.viewer) return
  entry.viewer = false
  entry.viewerSize = null
  entry.term.options.disableStdin = false
  uncenterViewer(entry)
  scheduleFit(id)
}

/**
 * PTY 的网格变了（控制方改了尺寸）。
 *
 * 只有本端不是控制方时才需要跟着变——本端是控制方的话，尺寸本来就是
 * 自己量出来报上去的，再跟着改会绕成回声。
 */
export function setViewerSize(id: string, cols: number, rows: number) {
  const entry = entries.get(id)
  if (!entry || !entry.viewer) return
  entry.viewerSize = { cols, rows }
  if (entry.term.cols === cols && entry.term.rows === rows) return
  entry.term.resize(cols, rows)
  centerViewer(entry)
}

export function isViewer(id: string): boolean {
  return entries.get(id)?.viewer ?? false
}

/** 让容器按网格的实际像素尺寸居中。 */
function centerViewer(entry: Entry) {
  // xterm 的屏幕层是 cols × cellWidth 的实际像素，量它才准。
  // 别指望 width:100% —— 那个百分比在 fit-content 的父级下算不出东西。
  const screen = entry.container.querySelector('.xterm-screen') as HTMLElement | null
  if (!screen) return
  const rect = screen.getBoundingClientRect()
  if (rect.width === 0 || rect.height === 0) return

  const host = entry.container.parentElement
  if (!host) return
  host.style.display = 'flex'
  host.style.alignItems = 'center'
  host.style.justifyContent = 'center'
  host.style.overflow = 'auto'

  entry.container.style.position = 'relative'
  entry.container.style.inset = 'auto'
  entry.container.style.width = `${Math.ceil(rect.width) + 16}px`
  entry.container.style.height = `${Math.ceil(rect.height) + 16}px`
  entry.container.style.outline = '1px dashed var(--color-border, #888)'
  entry.container.style.outlineOffset = '4px'
}

/** 恢复铺满宿主。 */
function uncenterViewer(entry: Entry) {
  const host = entry.container.parentElement
  if (host) {
    host.style.display = ''
    host.style.alignItems = ''
    host.style.justifyContent = ''
    host.style.overflow = ''
  }
  entry.container.style.position = 'absolute'
  entry.container.style.inset = '0'
  entry.container.style.width = ''
  entry.container.style.height = ''
  entry.container.style.outline = ''
  entry.container.style.outlineOffset = ''
}

// 把这个画布的尺寸量出来，告诉后端的 ConPTY。
// ConPTY 建出来的时候是 80×25（go-pty 写死的默认值），不告诉它真实尺寸，
// 里面跑的程序就一直按 80 列排版 —— 界面拉宽了它也不动。
export function fit(id: string) {
  const entry = entries.get(id)
  // 没 open 过、或者容器已经被摘下来（尺寸是 0）的时候不量。
  if (!entry?.opened || !entry.container.isConnected) return
  // ⚠️ 旁观时绝不量、绝不报。
  //
  // 这一行是整个远程接管里最容易漏、也最要命的一行：ResizeObserver 是 120ms
  // 防抖的，只要不停，桌面窗口一动就会把手机的 PTY 尺寸顶掉，两边来回抢。
  // 闸门放在 fit() 里而不是调用方，是因为调用点太多（observer、attach、
  // session:started、主题切换…），漏一个就前功尽弃。
  if (entry.viewer) return
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

/**
 * 只量尺寸、不上报。给桌面端「收回控制」用。
 *
 * 收回控制时必须显式把尺寸带过去：旁观期间容器是按手机的网格撑开的，
 * 后端记着的 desktop 尺寸也还是接管前那个旧值。所以流程是
 * 「先退出旁观恢复布局 → fitLocal 量出真实尺寸 → 连尺寸一起交上去」。
 */
export function fitLocal(id: string): { cols: number; rows: number } | null {
  const entry = entries.get(id)
  if (!entry?.opened || !entry.container.isConnected) return null
  try {
    entry.fitAddon.fit()
  } catch {
    return null
  }
  return { cols: entry.term.cols, rows: entry.term.rows }
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
