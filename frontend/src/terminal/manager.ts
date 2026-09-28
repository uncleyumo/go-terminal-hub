// 终端实例归这里持有（D19）：实例和它的容器都由本模块创建，组件只负责把容器 append 进去 / 摘回来。
//
// 为什么不直接建在组件里：选中另一行、或详情区被卸载时，组件里的 DOM 会跟着销毁，
// 挂在它上面的 Terminal 实例也就没了，之前滚过的输出全丢。把实例留在这个模块里，
// 容器只是个可插拔的壳，换回来还能看到原来的内容。
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { resizeSession, writeSession } from '../api'

interface Entry {
  term: Terminal
  container: HTMLDivElement
  fitAddon: FitAddon
  observer: ResizeObserver
  opened: boolean
}

const entries = new Map<string, Entry>()

function ensure(id: string): Entry {
  let entry = entries.get(id)
  if (!entry) {
    const container = document.createElement('div')
    // 这个元素不是组件模板里的，scoped 样式够不到它，尺寸只能在这里写死。
    container.style.width = '100%'
    container.style.height = '100%'
    const term = new Terminal({ fontSize: 13, scrollback: 5000 })
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
