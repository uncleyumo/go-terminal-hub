<script setup lang="ts">
// 顶层编排：登录 → 会话列表 → 终端，三个界面用 v-if 切，不引路由。
//
// 协议状态（控制方是谁、PTY 多大）都在这里，终端组件只负责渲染和量尺寸。
import { nextTick, onBeforeUnmount, onMounted, ref, shallowRef, useTemplateRef } from 'vue'
import { HubClient, type ConnectionState, type ControlMessage, type SessionInfo } from './api'
import LoginView from './components/LoginView.vue'
import SessionList from './components/SessionList.vue'
import TerminalView from './components/TerminalView.vue'

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
      void restoreLastSession()
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

async function openSession(s: SessionInfo, takeOver = false) {
  current.value = s
  controller.value = 'desktop'
  size.value = null
  view.value = 'terminal'
  writeStored(LAST_SESSION_KEY, s.id)
  // 等终端组件挂上再 attach：输出的写入口是那个组件，
  // 早于它挂载到达的回放会没地方落。
  await nextTick()
  client.value?.attach(s.id)
  if (takeOver) client.value?.takeover()
}

function backToList() {
  client.value?.detach()
  current.value = null
  view.value = 'list'
  // 主动退回列表 = 不想恢复这个会话了，把记录清掉
  writeStored(LAST_SESSION_KEY, null)
  writeStored(WAS_CONTROLLER_KEY, null)
  client.value?.list()
}

// 刷新之后自动回到原来那个会话；如果刷新前正握着控制权，也一并要回来。
// 只做一次——用户自己退回列表之后就不该再被拽回去。
let restorePending = true

async function restoreLastSession() {
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
  if (connState.value === 'superseded') client.value?.reconnectNow()
}

onMounted(async () => {
  document.addEventListener('visibilitychange', onVisibilityChange)
  // 先问一句 cookie 还有效吗。直接连 WebSocket 的话，未登录只会得到一个
  // 语焉不详的握手失败，用户看不到「该输令牌了」。
  const res = await fetch('/api/me').catch(() => null)
  if (!res || !res.ok) {
    view.value = 'login'
    return
  }
  startClient()
})

onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', onVisibilityChange)
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
