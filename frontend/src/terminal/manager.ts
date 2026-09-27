// 终端实例归这里持有（D19）：实例和它的容器都由本模块创建，组件只负责把容器 append 进去 / 摘回来。
//
// 为什么不直接建在组件里：选中另一行、或详情区被卸载时，组件里的 DOM 会跟着销毁，
// 挂在它上面的 Terminal 实例也就没了，之前滚过的输出全丢。把实例留在这个模块里，
// 容器只是个可插拔的壳，换回来还能看到原来的内容。
import { Terminal } from '@xterm/xterm'
import { writeSession } from '../api'

interface Entry {
  term: Terminal
  container: HTMLDivElement
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
    // 键盘这一路：xterm 把按键编成字节串交出来，原样送给后端。
    // xterm 自己不做回显 —— 屏幕上看到什么完全由进程的输出决定，
    // 所以对着一个已经停掉的会话打字不会有任何反应，也不会报错。
    term.onData((data) => {
      writeSession(id, data).catch((error) => {
        // 会话没在跑时后端会报错。那是用户正常操作，不值一提。
        console.debug('write to session failed', error)
      })
    })
    entry = { term, container, opened: false }
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
  }
  // xterm 靠一个隐藏的 textarea 收键盘。不主动聚焦的话，用户得先点一下终端才敲得进去。
  entry.term.focus()
}

// 只摘容器，不销毁实例 —— 下次再选中这一行，之前的输出还在。
export function detach(id: string) {
  entries.get(id)?.container.remove()
}

export function write(id: string, text: string) {
  entries.get(id)?.term.write(text)
}

// 新起一轮之前清屏，别让上一轮的输出和新一轮混在一起。
// 注意只对已经建过实例的行有效；没选中过的行还没有实例，也就没有历史要清。
export function clear(id: string) {
  entries.get(id)?.term.reset()
}

export function disposeAll() {
  for (const entry of entries.values()) {
    entry.term.dispose()
    entry.container.remove()
  }
  entries.clear()
}
