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
/** 停在历史上时，视口顶部离底部还有多少行。给「回到底部」当副标题。 */
const linesAbove = ref(0)
/** 停在历史上时底下又来了输出。不清掉的话，你不知道自己正在漏看。 */
const freshOutput = ref(false)
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

/** 一次点按滚的行数。整页一跳在手机上跨 20~40 行，点完不知道自己停在哪。 */
const SCROLL_STEP = 3

/**
 * 上翻（dir=-1）/ 下翻（dir=1）。
 *
 * 两种程序要分开处理，不能一刀切：
 *   - **全屏程序**（vim / less / top）：画面归它自己管，xterm 里没有可滚的历史，
 *     只能把 PageUp / PageDown 这两个键发过去让它翻——翻多少是它说了算。
 *   - **普通输出**（shell、claude code）：历史就在 xterm 自己的回滚缓冲里，
 *     直接滚本地最可靠——发字节过去没人理。
 */
function scrollStep(dir: -1 | 1) {
  if (!term) return
  if (term.buffer.active.type === 'alternate') {
    write(dir < 0 ? '\x1b[5~' : '\x1b[6~')
    return
  }
  term.scrollLines(dir * SCROLL_STEP)
}

function scrollToLatest() {
  term?.scrollToBottom()
  freshOutput.value = false
}

function syncScrollState() {
  if (!term) return
  const b = term.buffer.active
  const up = b.viewportY < b.baseY
  if (up !== scrolledUp.value) scrolledUp.value = up
  linesAbove.value = up ? b.baseY - b.viewportY : 0
  if (!up) freshOutput.value = false
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
  // 停在历史里时底下又来了东西：记一笔，「回到底部」上亮起来。
  // 不自动滚到底——你正看着的那段会被顶走，那比漏看还难受。
  if (scrolledUp.value) freshOutput.value = true
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
    <!-- .term-area 只负责给「回到底部」当定位锚，.term-host 里只有 xterm 一个人往里塞 DOM。
         把 Vue 的按钮直接塞进 .term-host 会跟 xterm 追加的节点抢兄弟索引。 -->
    <div class="term-area">
      <div ref="host" class="term-host" :class="{ viewer: !isController() }"></div>

      <!-- 上翻时的「回到底部」。绝对定位浮在终端右下角，不进布局流。
           它以前插在下面那排按钮中间，一出现就把「发图 / 交还控制」挤成两行：
           底栏变高 → 终端区变矮 → ResizeObserver 立刻重新 fit → 向 PTY 重报行列数
           → 桌面端整屏重排。点一次上翻的代价变成远端重画一整屏。 -->
      <Transition name="jump">
        <button
          v-if="scrolledUp"
          class="jump-bottom"
          :class="{ fresh: freshOutput }"
          @click="scrollToLatest"
        >
          <span class="jump-arrow">↓</span>
          <span>回到底部</span>
          <span v-if="linesAbove > 0" class="jump-count">{{ linesAbove }} 行</span>
        </button>
      </Transition>
    </div>

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
      <button class="btn icon" :title="`上翻 ${SCROLL_STEP} 行`" @click="scrollStep(-1)">▲</button>
      <button class="btn icon" :title="`下翻 ${SCROLL_STEP} 行`" @click="scrollStep(1)">▼</button>
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
  /* 底栏高度是常量。任何瞬时状态都不得改变它——
     它一变，终端区跟着变，ResizeObserver 就把新行列数报给 PTY，远端整屏重排。 */
  --fontbar-h: 47px;
}

/* 终端区 + 浮在它上面的「回到底部」。定位锚是这层，
   .term-host 保持只装 xterm，两者互不干扰。 */
.term-area {
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
  /* 高度钉死，宽度永不换行。加东西改这里，别让它自己长高。 */
  flex: 0 0 var(--fontbar-h);
  height: var(--fontbar-h);
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  flex-wrap: nowrap;
  overflow: hidden;
  background: var(--bg-elev);
  border-top: 1px solid var(--border);
}

.fontbar .btn {
  min-height: 34px;
  padding: 0 12px;
  /* 挤不下就先吃边距，绝不让文字折行——一折行整条底栏就变高了 */
  white-space: nowrap;
  flex-shrink: 0;
}

/* 一排按钮要挤进手机宽度，图标按钮就别留那么宽的边 */
.fontbar .btn.icon {
  padding: 0 9px;
}

.fontbar .spacer {
  min-width: 0;
}

.size {
  font-size: 13px;
  min-width: 20px;
  text-align: center;
  flex-shrink: 0;
}

/* 窄屏再收一档边距，保证「交还控制」不被挤掉 */
@media (max-width: 380px) {
  .fontbar {
    gap: 6px;
    padding: 6px 8px;
  }
  .fontbar .btn {
    padding: 0 8px;
  }
  .fontbar .btn.icon {
    padding: 0 7px;
  }
}

/* --- 回到底部：浮在终端上，不进布局流 ------------------------------------- */

.jump-bottom {
  position: absolute;
  right: 12px;
  /* 锚在 .term-area：它的底边就是底栏的上边，贴上去正好 */
  bottom: 12px;
  z-index: 6;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 40px;
  padding: 0 16px;
  border-radius: 999px;
  background: var(--bg-elev);
  border: 1px solid var(--border);
  color: var(--text);
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.55);
  font-size: 14px;
  white-space: nowrap;
}

.jump-bottom:active {
  background: #232838;
}

/* 底下又来了输出。整颗药丸换成强调色，比一个小圆点在手机上好认得多 */
.jump-bottom.fresh {
  border-color: var(--accent);
  color: var(--accent);
}

.jump-arrow {
  font-size: 15px;
  line-height: 1;
}

.jump-count {
  color: var(--text-dim);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.jump-bottom.fresh .jump-count {
  color: var(--accent);
  opacity: 0.75;
}

/* 出现/消失是这一屏唯一一处动效。从轻微下落进位，回位快于出现。 */
.jump-enter-active {
  transition:
    opacity 0.18s cubic-bezier(0.16, 1, 0.3, 1),
    transform 0.18s cubic-bezier(0.16, 1, 0.3, 1);
}
.jump-leave-active {
  transition:
    opacity 0.12s ease-in,
    transform 0.12s ease-in;
}
.jump-enter-from,
.jump-leave-to {
  opacity: 0;
  transform: translateY(8px) scale(0.96);
}
</style>
