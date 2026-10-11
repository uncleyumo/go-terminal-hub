<script setup lang="ts">
// 终端视图。xterm 实例的生命周期和尺寸逻辑都在这里。
//
// 尺寸规则（整个远程接管的核心）：终端尺寸属于 PTY，不属于窗口。
//   - 我是控制方 → 按容器 fit()，把量出来的尺寸报给后端
//   - 我是旁观方 → 不 fit，改成 resize 到 PTY 的真实网格，居中显示
// 旁观时绝不能报尺寸：窗口一缩放就会把控制方的 PTY 尺寸顶掉。
import { onBeforeUnmount, onMounted, ref, watch, useTemplateRef } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { HubClient, RESET_MARKER } from '../api'
import { shrinkImage } from '../image'
import { applyCtrl, type KeyDef } from '../keys'
import KeyBar from './KeyBar.vue'

const props = defineProps<{
  sessionId: string
  /** 当前控制方。'web' 表示这台手机在控制，其它值表示只读旁观。 */
  controller: string
  /** PTY 的真实网格，旁观时按它渲染。 */
  size: { cols: number; rows: number } | null
  client: HubClient
  connected: boolean
}>()

const emit = defineEmits<{
  (e: 'takeover'): void
  (e: 'release'): void
}>()

const hostRef = useTemplateRef<HTMLDivElement>('host')
const fileRef = useTemplateRef<HTMLInputElement>('file')
const fontSize = ref(13)
const ctrl = ref(false)
const shift = ref(false)
/** 视图停在回滚历史里（不在最底部）。用来提示「你现在看的不是最新的」。 */
const scrolledUp = ref(false)
/** 发图过程里的提示（正在压、存到哪了、失败了）。 */
const uploadMsg = ref('')
const uploading = ref(false)
let uploadTimer: number | null = null

let term: Terminal | null = null
let fitAddon: FitAddon | null = null
let resizeObserver: ResizeObserver | null = null
let fitTimer: number | null = null

const isController = () => props.controller === 'web'

// --- 尺寸 -------------------------------------------------------------------

/**
 * 量出容器能放下的行列数并报给后端。只有控制方该调这个。
 *
 * 旁观时调了就会把控制方的尺寸顶掉，所以闸门放在这里，而不是靠调用方自觉。
 */
function fitAndReport() {
  if (!term || !fitAddon || !hostRef.value || !isController()) return
  // 容器被隐藏或者尺寸是 0 的时候 fit() 会抛，那是中间状态
  if (!hostRef.value.isConnected || hostRef.value.clientWidth === 0) return

  try {
    fitAddon.fit()
  } catch {
    return
  }
  props.client.resize(term.cols, term.rows)
}

/** 拖窗口时尺寸一帧变一次，攒够 120ms 再报，别把后端打爆。 */
function scheduleFit() {
  if (fitTimer !== null) window.clearTimeout(fitTimer)
  fitTimer = window.setTimeout(() => {
    fitTimer = null
    fitAndReport()
  }, 120)
}

/** 旁观时把终端调成 PTY 的真实网格。 */
function applyViewerSize() {
  if (!term || !props.size) return
  if (term.cols !== props.size.cols || term.rows !== props.size.rows) {
    term.resize(props.size.cols, props.size.rows)
  }
}

// --- 上下翻 -----------------------------------------------------------------

/**
 * 上翻（dir=-1）/ 下翻（dir=1）。
 *
 * 两种程序要分开处理，不能一刀切：
 *   - **全屏程序**（vim / less / top）：画面归它自己管，xterm 里没有可滚的历史，
 *     只能把 PageUp / PageDown 这两个键发过去让它翻。
 *   - **普通输出**（shell、claude code）：历史就在 xterm 自己的回滚缓冲里，
 *     直接滚本地最可靠——发字节过去没人理。
 */
function scrollPage(dir: -1 | 1) {
  if (!term) return
  if (term.buffer.active.type === 'alternate') {
    write(dir < 0 ? '\x1b[5~' : '\x1b[6~')
    return
  }
  term.scrollPages(dir)
}

function scrollToLatest() {
  term?.scrollToBottom()
}

/** 手指拖不动 xterm 的回滚缓冲时，这两个按钮就是唯一的出路，所以一直显示。 */
function syncScrollState() {
  if (!term) return
  const b = term.buffer.active
  const up = b.viewportY < b.baseY
  if (up !== scrolledUp.value) scrolledUp.value = up
}

// --- 输入 -------------------------------------------------------------------

function write(data: string) {
  if (!data) return
  props.client.sendInput(data)
}

/** 把粘滞修饰键作用到一次按键上，然后弹起。 */
function resolveKey(k: KeyDef): string {
  if (shift.value && k.shiftData) {
    shift.value = false
    return k.shiftData
  }
  shift.value = false
  return k.data
}

function onKeyPress(k: KeyDef) {
  write(resolveKey(k))
}

/**
 * 软键盘敲进来的字符。
 *
 * 粘滞 Ctrl 在这里也生效——「按 Ctrl 再敲 c」应该出 \x03，
 * 不该非得去点工具条上的 C-c。
 */
function onTermData(data: string) {
  if (ctrl.value && data.length > 0) {
    ctrl.value = false
    // 控制字符（方向键之类）不参与 Ctrl 变换
    if (data.charCodeAt(0) >= 32) {
      write(applyCtrl(data[0]) + data.slice(1))
      return
    }
  }
  write(data)
}

// --- 发图 -------------------------------------------------------------------

function flashUpload(text: string, keepMs = 3000) {
  uploadMsg.value = text
  if (uploadTimer !== null) window.clearTimeout(uploadTimer)
  uploadTimer = window.setTimeout(() => {
    uploadTimer = null
    uploadMsg.value = ''
  }, keepMs)
}

/**
 * 把一张图塞进终端。
 *
 * 走的是「存成文件、把路径打进输入框」这条路：claude code 的输入里出现一个
 * 以 .png/.jpg/.jpeg/.gif/.webp 结尾的路径，它就当图片附件收下——跟把图片文件
 * 拖进终端是同一套（终端拖拽插进去的本来就是路径字符串）。
 *
 * 不直接把图片字节塞进 PTY：PTY 那头只是个终端程序，它不认识图片。
 */
function insertPath(path: string) {
  if (!term || !path) return
  // 路径要**整块**出现才被判成图片附件，所以裹成 bracketed paste。
  // 但程序没开这个模式的时候，裹了就会在屏幕上变成一串乱码转义序列，
  // 所以先问 xterm 要模式状态（它是从 PTY 输出里读到的，跟真实程序一致）。
  const text = term.modes.bracketedPasteMode ? `\x1b[200~${path}\x1b[201~ ` : `${path} `
  props.client.sendInput(text)
  flashUpload('已插入图片路径')
}

/** 点了「发图」：选图 → 压缩 → 发上去。存好之后由 App 那侧回调 insertPath。 */
async function onPickImage(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  // 选完立刻清空，否则再选同一张图不会再触发 change
  input.value = ''
  if (!file) return

  uploading.value = true
  flashUpload('正在压缩…', 60000)
  try {
    const { data, mediaType } = await shrinkImage(file)
    props.client.upload(data, mediaType)
    flashUpload('正在发送…', 60000)
  } catch (error) {
    flashUpload(error instanceof Error ? error.message : '发图失败')
  } finally {
    uploading.value = false
  }
}

// --- 生命周期 ---------------------------------------------------------------

onMounted(() => {
  const term_ = new Terminal({
    fontSize: fontSize.value,
    scrollback: 2000, // 手机内存有限，别跟桌面端一样开 5000
    fontFamily: '"Cascadia Mono", Consolas, "PingFang SC", "Microsoft YaHei", monospace',
    theme: {
      background: '#000000',
      foreground: '#e4e6eb',
      cursor: '#4f8cff',
    },
    // 手机上没有鼠标，关掉右键菜单之类的默认行为
    allowProposedApi: true,
  })
  term = term_
  fitAddon = new FitAddon()
  term_.loadAddon(fitAddon)

  if (hostRef.value) {
    term_.open(hostRef.value)
    term_.focus()
  }

  term_.onData(onTermData)
  term_.onScroll(syncScrollState)
  term_.onResize(() => {
    // xterm 自己 resize 之后容器尺寸也变了，会触发 ResizeObserver；
    // 那条路走 scheduleFit，而 scheduleFit 里有控制方闸门，所以不会乱报。
  })

  // 容器尺寸变化：横竖屏切换、软键盘弹出、工具条折叠都走这里
  resizeObserver = new ResizeObserver(() => scheduleFit())
  if (hostRef.value) resizeObserver.observe(hostRef.value)

  // 软键盘弹出时布局高度会变，但 ResizeObserver 不一定每次都能捕捉到
  window.visualViewport?.addEventListener('resize', onViewportResize)
  window.addEventListener('orientationchange', onViewportResize)

  if (isController()) {
    scheduleFit()
  } else {
    applyViewerSize()
  }
})

function onViewportResize() {
  // 等浏览器把布局算完再量
  window.setTimeout(() => (isController() ? scheduleFit() : applyViewerSize()), 120)
}

onBeforeUnmount(() => {
  window.visualViewport?.removeEventListener('resize', onViewportResize)
  window.removeEventListener('orientationchange', onViewportResize)
  resizeObserver?.disconnect()
  if (fitTimer !== null) window.clearTimeout(fitTimer)
  if (uploadTimer !== null) window.clearTimeout(uploadTimer)
  term?.dispose()
  term = null
  fitAddon = null
})

// --- 外部驱动的变化 ---------------------------------------------------------

/** 整机复位：清屏、清回滚、字符属性归零。 */
const RIS = '\x1bc'

/** 后端推来的输出。RESET_MARKER 表示「先清屏再写」。 */
function writeOutput(bytes: Uint8Array) {
  if (!term) return
  if (bytes === RESET_MARKER || bytes.length === 0) {
    // ⚠️ 走 write 队列，不用 term.reset()。
    // reset() 是**立即**生效的，而 write() 是排队的——之前排着还没画完的字节
    // 会在 reset 之后落到清干净的屏幕上，正好把旧画面又画回来。
    // RIS 是带内的转义序列，顺序天然跟着队列走。
    term.write(RIS)
    return
  }
  term.write(bytes)
}

defineExpose({ writeOutput, insertPath })

// 控制方换人：自己拿到控制权就重新 fit 并上报，交出去就切成对方的网格
watch(
  () => props.controller,
  (who) => {
    if (!term) return
    if (who === 'web') {
      // 刚接管：先把自己容器量出来的尺寸报上去
      fitAndReport()
      // 尺寸如果跟 PTY 现在的一样，上面那一步不会触发任何重绘——而接管之前
      // 我们一直在按对方的网格渲染，画面多半是旧的。补一次强制重画。
      props.client.repaint()
    } else {
      applyViewerSize()
    }
  },
)

watch(
  () => props.size,
  () => {
    if (!isController()) applyViewerSize()
  },
  { deep: true },
)

watch(fontSize, () => {
  if (!term) return
  term.options.fontSize = fontSize.value
  // 字号变了行列数也变了：控制方要重报，旁观方要重新对齐网格
  if (isController()) scheduleFit()
  else applyViewerSize()
})
</script>

<template>
  <div class="term-wrap">
    <div ref="host" class="term-host" :class="{ viewer: !isController() }"></div>

    <!-- 只在连着的时候显示。没连上时顶部那条横幅已经说明原因了，
         这里再挂一条「未连接」+ 灰掉的按钮只是噪声。 -->
    <div v-if="connected && !isController()" class="banner warn">
      <!-- 旁观时画面可能是旧的：回放的是字节日志，重建不出全屏程序的当前画面，
           而只有控制方有资格让程序重画（重画要动 PTY 尺寸）。 -->
      <span>只读旁观 · 画面可能不是最新的，接管后刷新</span>
      <span class="spacer"></span>
      <button class="btn" @click="emit('takeover')">接管</button>
    </div>

    <div class="fontbar">
      <button class="btn icon" title="上翻一页" @click="scrollPage(-1)">▲</button>
      <button class="btn icon" title="下翻一页" @click="scrollPage(1)">▼</button>
      <button v-if="scrolledUp" class="btn hot" @click="scrollToLatest">回到底部</button>
      <button class="btn icon" @click="fontSize = Math.max(8, fontSize - 1)">A-</button>
      <span class="dim size">{{ fontSize }}</span>
      <button class="btn icon" @click="fontSize = Math.min(24, fontSize + 1)">A+</button>
      <span class="spacer"></span>
      <!-- 发图：选手机相册/拍照，压好之后存到电脑上，把路径打进输入框。
           只有控制方能发——往本机写文件不该是旁观方的能力，后端也拦了一道。 -->
      <button v-if="isController()" class="btn" :disabled="uploading" @click="fileRef?.click()">
        发图
      </button>
      <button v-if="isController()" class="btn" @click="emit('release')">交还控制</button>
    </div>

    <input
      ref="file"
      type="file"
      accept="image/png,image/jpeg,image/gif,image/webp"
      style="display: none"
      @change="onPickImage"
    />

    <div v-if="uploadMsg" class="notice">{{ uploadMsg }}</div>

    <KeyBar
      :ctrl="ctrl"
      :shift="shift"
      @press="onKeyPress"
      @toggle="(m) => (m === 'ctrl' ? (ctrl = !ctrl) : (shift = !shift))"
    />
  </div>
</template>

<style scoped>
.term-wrap {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

/* 发图的进度/结果提示。浮在终端上方，不占布局高度——
   手机屏幕上每一条常驻的横条都是从终端里抢走的行数。 */
.notice {
  position: absolute;
  left: 50%;
  bottom: 96px;
  transform: translateX(-50%);
  z-index: 5;
  max-width: 80%;
  padding: 6px 14px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--bg-elev);
  color: var(--text);
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.fontbar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  background: var(--bg-elev);
  border-top: 1px solid var(--border);
}

.fontbar .btn {
  min-height: 34px;
  padding: 0 12px;
}

/* 一排按钮要挤进手机宽度，图标按钮就别留那么宽的边 */
.fontbar .btn.icon {
  padding: 0 9px;
}

.fontbar .btn.hot {
  border-color: var(--warn);
  color: var(--warn);
}

.size {
  font-size: 13px;
  min-width: 20px;
  text-align: center;
}
</style>
