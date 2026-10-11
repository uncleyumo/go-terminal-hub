<script setup lang="ts">
// 顶层编排：登录 → 会话列表 → 终端，三个界面用 v-if 切，不引路由。
//
// 协议状态（控制方是谁、PTY 多大）都在这里，终端组件只负责渲染和量尺寸。
import { nextTick, onBeforeUnmount, onMounted, ref, shallowRef, useTemplateRef } from 'vue'
import { HubClient, type ConnectionState, type ControlMessage, type SessionInfo } from './api'
import LoginView from './components/LoginView.vue'
import SessionList from './components/SessionList.vue'
import TerminalView from './components/TerminalView.vue'
import { resetAutoToken } from './token'

type View = 'login' | 'list' | 'terminal'

// 刷新页面之后自动回到原来那个会话。
//
// 不记的话，一按 F5 就掉回列表、控制权也丢了——「一刷新就断开」说的就是这个。
// 用 localStorage 而不是 sessionStorage：Quark 这类浏览器在后台回收标签页时
// sessionStorage 未必活得下来，而这恰恰是最常发生刷新的场景。
const LAST_SESSION_KEY = 'hub.remote.lastSession'
const WAS_CONTROLLER_KEY = 'hub.remote.wasController'

function readStored(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeStored(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key)
    else localStorage.setItem(key, value)
  } catch {
    // 隐私模式下会抛。恢复不了会话不影响别的。
  }
}

const view = ref<View>('login')
const connState = ref<ConnectionState>('connecting')
const connDetail = ref('')
const agentOnline = ref(false)
const sessions = ref<SessionInfo[]>([])
const current = ref<SessionInfo | null>(null)
const controller = ref('desktop')
const size = ref<{ cols: number; rows: number } | null>(null)
const lastError = ref('')

// 用 shallowRef：HubClient 是类实例，包成深层响应式会把方法里的 this 弄坏
const client = shallowRef<HubClient | null>(null)
const termRef = useTemplateRef<InstanceType<typeof TerminalView>>('term')

const connected = () => connState.value === 'open'

function startClient() {
  // 换客户端之前先关掉旧的：留着的话它会一直重连，和新连接互相顶，
  // 日志里就会出现成对出现的连接。
  client.value?.close()
  const c = new HubClient({
    onState: (state, detail) => {
      connState.value = state
      connDetail.value = detail ?? ''
      if (state === 'kicked') {
        // 被别的设备顶掉了。绝不能自动重连，否则两台设备会互相踢成死循环。
        current.value = null
        view.value = 'list'
      }
      if (state === 'superseded') {
        // 同一个浏览器的另一条连接接管了（多半是另一个标签页，或者旧页面没关）。
        // 同样不重连——重连就是把刚建立的那条再顶掉。
        current.value = null
        view.value = 'list'
      }
      if (state === 'open') {
        c.list()
      }
      if (state === 'closed') {
        // 断线有两种：网络不通（继续重连就好），和登录没了（得回登录页）。
        // 从关闭码上分不出来——服务端拒握手时浏览器只给一个 1006，和网线拔了
        // 长得一模一样。所以直接问 /api/me。
        void checkStillAuthed()
      }
    },
    onControl: handleControl,
    onOutput: (bytes) => termRef.value?.writeOutput(bytes),
    onAgentOnline: (online) => {
      agentOnline.value = online
      if (online) client.value?.list()
    },
  })
  client.value = c
  c.connect()
}

function handleControl(m: ControlMessage) {
  switch (m.t) {
    case 'sessions':
      sessions.value = m.sessions ?? []
      // 列表刷新时同步一下当前会话的状态
      if (current.value) {
        const updated = sessions.value.find((s) => s.id === current.value!.id)
        if (updated) current.value = updated
      }
      void afterSessions()
      break
    case 'attached':
      controller.value = m.controller ?? 'desktop'
      if (m.cols && m.rows) size.value = { cols: m.cols, rows: m.rows }
      break
    case 'lease':
      if (m.controller) {
        controller.value = m.controller
        // 记着「刷新前我在控制」。刷新会把控制权还给桌面，
        // 不记的话每次刷新都得重新点一遍「接管」。
        writeStored(WAS_CONTROLLER_KEY, m.controller === 'web' ? '1' : null)
      }
      if (m.cols && m.rows) size.value = { cols: m.cols, rows: m.rows }
      break
    case 'size':
      if (m.cols && m.rows) size.value = { cols: m.cols, rows: m.rows }
      break
    case 'uploaded':
      // 图已经在电脑上了，现在把它的路径打进输入框——claude code 认这个。
      // 插入这一步留给终端组件：要不要裹 bracketed paste 得看它那侧的模式状态。
      if (m.path) termRef.value?.insertPath(m.path)
      break
    case 'started':
      client.value?.list()
      break
    case 'exit':
      writeNotice(`\r\n\x1b[2m── 已退出${m.code ? `，退出码 ${m.code}` : ''}${m.errMsg ? `：${m.errMsg}` : ''} ──\x1b[0m\r\n`)
      client.value?.list()
      break
    case 'error':
      lastError.value = m.msg ?? '未知错误'
      window.setTimeout(() => (lastError.value = ''), 4000)
      break
  }
}

function writeNotice(text: string) {
  termRef.value?.writeOutput(new TextEncoder().encode(text))
}

// 断线之后问一句「我还进得去吗」。
//
// relay 的会话存在内存里（`authenticator.sessions` 是个 map），它重启一次，
// 所有人的 cookie 就同时失效了。这时候 WebSocket 握手会被 401 拒掉，可浏览器
// 只看到一个 1006 异常关闭 —— 和「网线断了」完全一样。光看重连状态是分不出来的，
// 而这两种要处理的方式正相反：网络问题该继续等，登录没了该回登录页换 cookie。
//
// 网络不通时 fetch 自己会抛，那种情况什么都不做，接着重连。
let checkingAuth = false
async function checkStillAuthed() {
  if (checkingAuth) return
  checkingAuth = true
  try {
    const res = await fetch('/api/me').catch(() => null)
    if (!res || res.status !== 401) return

    client.value?.close()
    // 令牌还是好的，坏的只是 cookie。放行让登录页拿它重新换一次。
    resetAutoToken()
    current.value = null
    view.value = 'login'
  } finally {
    checkingAuth = false
  }
}

async function openSession(s: SessionInfo, takeOver = false) {
  current.value = s
  controller.value = 'desktop'
  size.value = null
  view.value = 'terminal'
  // 补一条历史记录，理由见 onPopState
  history.pushState({ view: 'terminal' }, '')
  writeStored(LAST_SESSION_KEY, s.id)
  // 等终端组件挂上再 attach：输出的写入口是那个组件，
  // 早于它挂载到达的回放会没地方落。
  await nextTick()
  client.value?.attach(s.id)
  if (takeOver) client.value?.takeover()
}

// 安卓的返回键 / 返回手势。
//
// 页面本来只有一条历史记录，所以在终端界面按返回，系统没有可回退的条目，
// 直接把 App 关掉——用户想回会话列表，结果退出了整个应用。包成 App 的 WebView
// 和「加到主屏」的 PWA 都按这套规则走，浏览器标签页里也一样。
//
// 进终端时补一条历史，返回时浏览器先回退它，落到列表上。
function onPopState() {
  if (view.value === 'terminal') doBackToList()
}

// 界面上那个 ‹ 也走历史，让按钮和返回键是同一条路。
// 分开走的话会出现「点了按钮之后，返回键要多按一次才退出去」这种错位。
function backToList() {
  if (history.state?.view === 'terminal') history.back()
  else doBackToList()
}

function doBackToList() {
  client.value?.detach()
  current.value = null
  view.value = 'list'
  // 主动退回列表 = 不想恢复这个会话了，把记录清掉
  writeStored(LAST_SESSION_KEY, null)
  writeStored(WAS_CONTROLLER_KEY, null)
  client.value?.list()
}

// 会话列表到手之后，决定要不要绑定会话。两条路：
//
// 1. 正在看某个会话 —— 必须重新 attach。socket 一断 attachedId 就被清空，
//    不重新绑定的话终端画面一直冻着，敲键盘没有任何反应，而且看不出哪里不对。
//    手机上切 WiFi/蜂窝、锁屏回来、relay 重启都会断一次，这条比第 2 条常用得多。
// 2. 刚打开页面（或刷新）—— 回到上次看的那个会话，刷新前握着控制权也一并要回来。
//    只做一次：用户自己退回列表之后，不该再被拽回去。
let restorePending = true

async function afterSessions() {
  if (view.value === 'terminal' && current.value) {
    // 已经绑着就别再绑：重复 attach 会让服务端重放一遍字节日志，画面会闪一下
    if (client.value?.attached) return
    await nextTick()
    client.value?.attach(current.value.id)
    return
  }

  if (!restorePending) return
  restorePending = false

  const id = readStored(LAST_SESSION_KEY)
  if (!id) return

  const target = sessions.value.find((s) => s.id === id)
  if (!target) {
    // 会话已经被删了，别留着这条记录反复试
    writeStored(LAST_SESSION_KEY, null)
    return
  }
  await openSession(target, readStored(WAS_CONTROLLER_KEY) === '1')
}

// 被同一浏览器的另一条连接顶掉之后，切回这个标签页就自动要回来。
//
// 手机上很容易开好几个标签页（截图里就是 3 个）。只让用户手动点
// 「在这里继续」的话，每换一次标签页都要点一下，而且他多半不知道为什么。
// 「哪个标签页在看，哪个就接管」——这才符合直觉。
//
// 之所以不能干脆让多个标签页并存：二进制帧里不带会话 id，
// 一条浏览器连接只能绑一个会话，多开没法各自路由。
function onVisibilityChange() {
  if (document.visibilityState !== 'visible') return
  if (connState.value === 'superseded') {
    client.value?.reconnectNow()
    return
  }
  // 从后台或锁屏回来。安卓会挂起后台的 WebView，服务端那侧多半已经把 socket
  // 掐了；浏览器随后会补一个 onclose，重连和重新绑定都走那条路。
  // 但 close 事件有可能还在路上，这一小段时间里界面看着还是「已连接」，
  // 敲键盘却没有任何反应。所以顺手确认一次登录状态：只有 401 才动。
  if (connState.value !== 'open') void checkStillAuthed()
}

onMounted(async () => {
  document.addEventListener('visibilitychange', onVisibilityChange)
  window.addEventListener('popstate', onPopState)
  // 先问一句 cookie 还有效吗。直接连 WebSocket 的话，未登录只会得到一个
  // 语焉不详的握手失败，用户看不到「该输令牌了」。
  const res = await fetch('/api/me').catch(() => null)
  if (!res || !res.ok) {
    view.value = 'login'
    return
  }
  // cookie 还有效就直接进列表。
  //
  // 少了这一句会停在登录页上：view 的初始值就是 'login'，而除了这里，
  // 只有「登录成功」和「恢复上次那个会话」会把它推到 'list'。
  // 没记住会话时（比如刚从终端退回列表又刷新）两条都不成立，页面就卡在
  // 登录页——明明已经登录着，却还在让人输令牌。
  view.value = 'list'
  startClient()
})

onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', onVisibilityChange)
  window.removeEventListener('popstate', onPopState)
  client.value?.close()
})

function onLoginSuccess() {
  view.value = 'list'
  startClient()
}
</script>

<template>
  <LoginView v-if="view === 'login'" @success="onLoginSuccess" />

  <template v-else>
    <div class="topbar">
      <button v-if="view === 'terminal'" class="btn" @click="backToList">‹</button>
      <span class="dot" :class="agentOnline ? 'on' : 'off'"></span>
      <span class="title">
        {{ view === 'terminal' ? (current?.name || current?.id || '') : 'terminal hub' }}
      </span>
      <span class="spacer"></span>
      <span v-if="view === 'terminal'" class="dim" style="font-size: 12px">
        {{ controller === 'web' ? '控制中' : '旁观' }}
      </span>
    </div>

    <!-- 同一个账号只允许一个浏览器在线。被顶掉时要说清「为什么断」和
         「点了会怎样」——只说「已断开」的话，用户会以为是自己网络的问题。 -->
    <div v-if="connState === 'kicked'" class="banner error">
      <span>已在其他设备打开，这里已断开</span>
      <span class="spacer"></span>
      <button class="btn" @click="client?.reconnectNow()">在这里继续</button>
    </div>
    <div v-else-if="connState === 'superseded'" class="banner warn">
      <span>另一个标签页在接管。切回这个页面会自动接过来</span>
      <span class="spacer"></span>
      <button class="btn" @click="client?.reconnectNow()">立刻接管</button>
    </div>
    <div v-else-if="connState === 'closed'" class="banner warn">
      连接断开，正在重连…
    </div>
    <div v-if="lastError" class="banner error">{{ lastError }}</div>

    <!-- 电脑那头的 hub 掉线了。终端画面会冻在最后一帧，不说一句的话
         用户只会以为卡住了。它回来时 client 会重新 list，会话列表一到
         就自动重新绑定（见 afterSessions）。 -->
    <div v-if="view === 'terminal' && !agentOnline" class="banner warn">
      电脑上的 hub 没在线，画面停在最后一帧。它回来会自动接上
    </div>

    <SessionList
      v-if="view === 'list'"
      :sessions="sessions"
      :agent-online="agentOnline"
      @open="openSession"
      @start="(id) => client?.start(id)"
      @stop="(id) => client?.stop(id)"
    />

    <TerminalView
      v-else-if="view === 'terminal' && current && client"
      ref="term"
      :session-id="current.id"
      :controller="controller"
      :size="size"
      :client="client"
      :connected="connected()"
      @takeover="client.takeover()"
      @release="client.release()"
    />
  </template>
</template>
