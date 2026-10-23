<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import SessionList from './components/SessionList.vue'
import SessionForm from './components/SessionForm.vue'
import TerminalPane from './components/TerminalPane.vue'
import TermBackgroundDialog from './components/TermBackgroundDialog.vue'
import AppInfoDialog from './components/AppInfoDialog.vue'
import UpdateDialog from './components/UpdateDialog.vue'
import RemoteSettingsDialog, { type RemoteConfig } from './components/RemoteSettingsDialog.vue'
import SessionTabs, { type TabItem } from './components/SessionTabs.vue'
import SettingsDialog from './components/SettingsDialog.vue'
import UButton from './components/ui/UButton.vue'
import UConfirmHost from './components/ui/UConfirmHost.vue'
import UDialog from './components/ui/UDialog.vue'
import UDropMenu from './components/ui/UDropMenu.vue'
import UIcon from './components/ui/UIcon.vue'
import UIconMenu from './components/ui/UIconMenu.vue'
import UToaster from './components/ui/UToaster.vue'
import UTextarea from './components/ui/UTextarea.vue'
import UTooltip from './components/ui/UTooltip.vue'
import { confirm } from './components/ui/confirm'
import { notify } from './components/ui/toast'
import { flyChips, measureChips, revealTabs, type Chip } from './components/tabs-motion'
import { featureOn } from './features'
import { Browser, Clipboard } from '@wailsio/runtime'
import {
  checkUpdate,
  deleteSession,
  getSettings,
  getUpdateState,
  onTrayAction,
  onUpdateState,
  listSessions,
  onSessionExited,
  getRemoteStatus,
  onRemoteStatus,
  onSessionLease,
  onSessionOutput,
  onSessionSize,
  onSessionStarted,
  openScriptDir,
  openWorkDir,
  quitApp,
  reorderSessions,
  resolveSpecCommandLine,
  resolveWorkDir,
  restartSession,
  saveSettings,
  setStartOnBoot,
  startSession,
  stopAllSessions,
  stopSession,
  takeControl,
  type RemoteStatus,
  type Settings,
  type SessionView,
  type UpdateState,
} from './api'
import {
  applyAppBackground,
  clear as clearTerminal,
  dispose,
  disposeAll,
  enterViewer,
  exitViewer,
  fit as fitTerminal,
  fitLocal,
  readViewport,
  setSessionBackground,
  setViewerSize,
  write as writeTerminal,
} from './terminal/manager'
import { getAppBackground, getBackground, setAppBackground, setBackground } from './terminal/background'
import { normalizeLocale, setLocale, type AppLocale } from './i18n'
import { applyTheme, normalizeTheme, watchSystemTheme, type ThemeMode } from './theme'
import brandIcon from './assets/brand/icon.svg'

const { t } = useI18n()

const sessions = ref<SessionView[]>([])
const selectedId = ref<string | null>(null)

// —— 控制权租约（手机远程接管）——
// 每个会话同一时刻只有一个控制方：desktop 或 web。没有记录就是 desktop。
const controllers = ref<Record<string, string>>({})
const ptySizes = ref<Record<string, { cols: number; rows: number }>>({})
const formVisible = ref(false)
const editing = ref<SessionView | null>(null)
const configOpen = ref(false)
const query = ref('')

// 后端会把它存的那份原样回给我们，包括我们不认识的字段，这里保持整份往回写。
const settings = ref<Settings>({
  language: 'en',
  theme: 'light',
  startOnBoot: false,
  remote: { enabled: false, url: '', key: '', secret: '', autoTakeover: false },
  skippedUpdateVersion: '',
})
const locale = ref<AppLocale>('en')
const themeMode = ref<ThemeMode>('system')
const startOnBootBusy = ref(false)
const refreshing = ref(false)

// 色板小窗（会话级 + 全局级，两个实例）+ 「最终命令」那一条命令行的只读小窗
const colorDialogOpen = ref(false)
const appColorOpen = ref(false)
const appInfoOpen = ref(false)

// 提取当前视口内容的小窗。内容是取出来之后随便改的文本框——
// 终端里选字不顺手的场合（比如要用划词翻译）就从这里拿。
const extractOpen = ref(false)
const extractText = ref('')
function openExtract() {
  if (!selected.value) return
  extractText.value = readViewport(selected.value.config.id)
  extractOpen.value = true
}

// —— 应用内更新 ——
//
// 状态只有这一份，来源是后端那条 `update:state` 事件。弹窗、顶栏圆点、
// 托盘菜单文字都从它派生 —— 弹窗关着的时候下载照跑，状态不能跟着弹窗走。
const updateOpen = ref(false)
const updateState = ref<UpdateState | null>(null)

// 有新版本（或已经下好在等重启）时，顶栏「关于」按钮上点一个小圆点。
// 被「跳过这个版本」的按没有新版处理（D60）。
const hasUpdate = computed(() => {
  const s = updateState.value
  if (!s) return false
  if (s.state === 'ready') return true
  return s.state === 'available' && !s.skipped
})

// —— 远程接管 ——
const remoteOpen = ref(false)
const remoteStatus = ref<RemoteStatus | null>(null)
const remoteSaving = ref(false)

const remoteConfig = computed<RemoteConfig>(() => ({
  enabled: settings.value.remote?.enabled ?? false,
  url: settings.value.remote?.url ?? '',
  key: settings.value.remote?.key ?? '',
  secret: settings.value.remote?.secret ?? '',
  autoTakeover: settings.value.remote?.autoTakeover ?? false,
}))

// 保存后后端会重读配置并把状态推回来（remote:status 事件），
// 所以这里不用等、也不用自己去问连接结果。
async function saveRemoteConfig(next: RemoteConfig) {
  remoteSaving.value = true
  try {
    await saveSettings({ ...settings.value, remote: next })
    settings.value = { ...settings.value, remote: next }
    remoteOpen.value = false
  } catch (error) {
    notify.error(`${t('remote.title')}: ${String(error)}`)
  } finally {
    remoteSaving.value = false
  }
}

async function refreshRemoteStatus() {
  try {
    remoteStatus.value = await getRemoteStatus()
  } catch {
    // 应用刚起来时可能还没跑 ServiceStartup，忽略即可
  }
}
const commandLineOpen = ref(false)
const commandLine = ref('')

// —— 会话栏折叠 ——
// 收起时把整条 268px 让给终端：搜索框一起收（它筛的就是这份列表，列表没了它也没用）。
// 状态存本地，这样「收起」是下次打开还在，而不是每次都要重按一遍。
// 终端那边不用管宽度变了怎么办：TerminalPane 上的 ResizeObserver 会重新量（manager.ts）。
const SIDEBAR_KEY = 'hub.sidebar.collapsed'
const sidebarCollapsed = ref(readSidebarCollapsed())

function readSidebarCollapsed(): boolean {
  try {
    return window.localStorage.getItem(SIDEBAR_KEY) === '1'
  } catch {
    // 隐私模式之类的地方 localStorage 会抛。不存就是了，默认展开。
    return false
  }
}

function persistCollapsed(collapsed: boolean) {
  try {
    window.localStorage.setItem(SIDEBAR_KEY, collapsed ? '1' : '0')
  } catch {
    // 存不进去只是下次不记住，折叠本身照样能用
  }
}

async function toggleSidebar() {
  const next = !sidebarCollapsed.value
  // 只有「标签随折叠出现/消失」的那一版才有东西要飞。功能关着、或者设成
  // 「展开也显示标签」时，标签一直在那儿，收起只是把列表让出来。
  const animate =
    featureOn(settings.value, 'sessionTabs') && !alwaysTabs.value && tabs.value.length > 0
  if (!animate) {
    sidebarCollapsed.value = next
    persistCollapsed(next)
    return
  }

  const chips: Chip[] = tabs.value.map((tab) => ({
    id: tab.id,
    label: tab.name,
    running: tab.running,
  }))
  // 起点：收起前量列表行，展开前量顶栏标签
  const source = measureChips(next ? 'row' : 'tab', chips)

  // 终点必须在「最终布局」里量：先关掉过渡把布局一步做到位，量完再退回去。
  // 不这么做量到的是过渡途中的位置，飞到一半就落空了。
  const html = document.documentElement
  html.classList.add('no-anim')
  sidebarCollapsed.value = next
  await nextTick()
  const target = measureChips(next ? 'tab' : 'row', chips)

  // 还在 no-anim 里把布局退回原样，让浏览器记下「原来的宽度」是多少；
  // 这两步之间不能露出过渡，否则退回的那一下会被当成一次真的展开动画。
  sidebarCollapsed.value = !next
  await nextTick()
  void document.body.offsetHeight
  html.classList.remove('no-anim')
  // 过渡重新可用之后先算一帧，再切过去 —— 这样起点是原宽度，不是 0
  void document.body.offsetHeight

  sidebarCollapsed.value = next
  persistCollapsed(next)
  await nextTick()

  flyChips(chips, source, target)
  // 收起时标签在飞行途中是透明的，落地那一刻才亮起来
  if (next) revealTabs(chips)
}

// —— 顶栏标签 ——
//
// 哪些会话有标签：运行中的，加上当前这条（它停着也得看得见自己）。
// 顺序只在这次运行里有效 —— 每次开机时会话的启动顺序本来就会变，
// 记下来的顺序第二天对不上号。
const ALWAYS_TABS_KEY = 'hub.tabs.always'
const alwaysTabs = ref(readAlwaysTabs())

function readAlwaysTabs(): boolean {
  try {
    return window.localStorage.getItem(ALWAYS_TABS_KEY) === '1'
  } catch {
    return false
  }
}

function setAlwaysTabs(value: boolean) {
  alwaysTabs.value = value
  try {
    window.localStorage.setItem(ALWAYS_TABS_KEY, value ? '1' : '0')
  } catch {
    // 同上：记不住就记不住，这次照样生效
  }
}

const tabOrder = ref<string[]>([])

// 会话列表一变就把新会话补到队尾、把删掉的摘掉；用户拖过的相对顺序不动
watch(
  sessions,
  (list) => {
    const alive = new Set(list.map((s) => s.config.id))
    const kept = tabOrder.value.filter((id) => alive.has(id))
    for (const s of list) if (!kept.includes(s.config.id)) kept.push(s.config.id)
    if (kept.join('\u0000') !== tabOrder.value.join('\u0000')) tabOrder.value = kept
  },
  { immediate: true },
)

const tabs = computed<TabItem[]>(() => {
  const byId = new Map(sessions.value.map((s) => [s.config.id, s]))
  return tabOrder.value
    .filter((id) => byId.has(id))
    .map((id) => {
      const s = byId.get(id)!
      return { id, name: s.config.name || id, running: s.running }
    })
    .filter((tab) => tab.running || tab.id === selectedId.value)
})

const tabsVisible = computed(
  () =>
    featureOn(settings.value, 'sessionTabs') &&
    tabs.value.length > 0 &&
    (sidebarCollapsed.value || alwaysTabs.value),
)

function onTabsReorder(ids: string[]) {
  // 拖过的这几条排前面，没上标签的（停着的会话）保持原来的相对顺序跟在后面
  const moved = new Set(ids)
  tabOrder.value = [...ids, ...tabOrder.value.filter((id) => !moved.has(id))]
}

// 功能开关。先落界面、再把整份设置写回去，失败只回滚那一个键。
async function setFeature(id: string, on: boolean) {
  const previous = settings.value.features ?? {}
  const next = { ...previous, [id]: on }
  settings.value = { ...settings.value, features: next }
  try {
    await saveSettings(settings.value)
  } catch (error) {
    settings.value = { ...settings.value, features: previous }
    notify.error(`${t('msg.settingFailed')}: ${String(error)}`)
  }
}

const settingsOpen = ref(false)

// 顺序跟 Windows「个性化 → 颜色」里的排法一致：浅色 → 深色 → 跟随系统
// computed 是必须的：t() 只在被调用那一刻取一次值，写成普通数组的话切到中文后
// 这三个菜单项还是英文（localeOptions 不用 computed —— 那两个是语言自己的名字）
const themeOptions = computed(() => [
  { value: 'light', label: t('theme.light'), icon: 'sun' },
  { value: 'dark', label: t('theme.dark'), icon: 'moon' },
  { value: 'system', label: t('theme.system'), icon: 'monitor' },
])

const selected = computed(
  () => sessions.value.find((s) => s.config.id === selectedId.value) ?? null,
)

// —— 运行时（启动时间 / 停止时间 / 跑了多久 / 哪个进程）——
// 放在配置下拉里：那是「这条会话现在什么情况」的地方。
//
// 运行时长在跑的时候要一秒一秒地走，所以开着的时候才取时间。
// 面板关着、或者这条会话没在跑，就不用再动了。
const now = ref(Date.now())
let tickTimer: number | null = null

function stopTick() {
  if (tickTimer !== null) {
    window.clearInterval(tickTimer)
    tickTimer = null
  }
}

watch(
  () => configOpen.value && selected.value?.running === true,
  (need) => {
    stopTick()
    if (!need) return
    now.value = Date.now()
    tickTimer = window.setInterval(() => {
      now.value = Date.now()
    }, 1000)
  },
  { immediate: true },
)

onBeforeUnmount(stopTick)

// 后端给的是 Unix 毫秒；0 表示「从来没发生过」这一档。
function formatMoment(ms: number): string {
  if (ms === 0) return '—'
  return new Date(ms).toLocaleString(locale.value)
}

function formatDuration(ms: number): string {
  const total = Math.floor(ms / 1000)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${pad(m)}:${pad(s)}`
}

const runtimeInfo = computed(() => {
  const st = selected.value?.status
  if (!st || st.startedAt === 0) {
    return { started: t('detail.neverStarted'), ended: '—', duration: '—', pid: '—' }
  }
  // 还在跑就算到「现在」；跑完了就按退出那一刻算。
  const end = st.endedAt === 0 ? now.value : st.endedAt
  return {
    started: formatMoment(st.startedAt),
    ended: st.endedAt === 0 ? t('detail.stillRunning') : formatMoment(st.endedAt),
    duration: formatDuration(Math.max(0, end - st.startedAt)),
    pid: st.pid > 0 ? String(st.pid) : '—',
  }
})

// 正在跑的会话。退出前确认、更新前确认都要问「关掉它们行不行」，
// 数就是这个数。
const runningSessions = computed(() => sessions.value.filter((s) => s.running))

// 当前选中的会话是不是正被手机控制。是的话终端区上方要挂提示条。
const selectedByWeb = computed(
  () => selectedId.value !== null && controllers.value[selectedId.value] === 'web',
)

const selectedPtySize = computed(() => {
  const id = selectedId.value
  if (id === null) return null
  return ptySizes.value[id] ?? null
})

/**
 * 桌面端点「收回控制」。
 *
 * 顺序不能颠倒：先退出旁观、恢复自己的布局量出真实尺寸，再连尺寸一起交上去。
 * 旁观期间容器是按手机的网格撑开的，后端记着的 desktop 尺寸也还是接管前那个，
 * 直接把控制权要回来而不带尺寸的话，PTY 会停在手机那个尺寸上。
 */
async function takeBackControl() {
  const id = selectedId.value
  if (id === null) return

  exitViewer(id)
  await nextTick()
  const size = fitLocal(id)
  if (!size) return

  try {
    await takeControl(id, size.cols, size.rows)
  } catch (error) {
    // 没要回来：把旁观状态装回去。
    // 不装的话，界面上看着已经是「我控制着」，后端却还认为远程端在控制 ——
    // 敲进去的字会被后端拒掉，而界面上一点提示都没有。
    const web = selectedPtySize.value
    if (web) enterViewer(id, web.cols, web.rows)
    notify.error(`${t('remote.takeBackFailed')}: ${String(error)}`)
  }
}

// 交给表单做重名检查。名称重复的话列表里两条长得一模一样，
// 点哪条都分不清，启停就更没把握了。
const takenNames = computed(() =>
  sessions.value.map((s) => ({ id: s.config.id, name: s.config.name || s.config.id })),
)

// 切换会话时把配置折回去：下一条会话的展开状态默认一样，
// 否则来回点两下，每次都得再点一次「详情」。
function select(id: string) {
  selectedId.value = id
  configOpen.value = false
}

// —— 会话工具条的下拉菜单（D47 六项）——
//
// kind 的两种分法（D47 他定的）：
//   收路径 → 菜单 1、2 能用：bat cmd ps1 exe terminal-cmd terminal-powershell
//   收命令 → 菜单 1、2 置灰：  shell terminal-shell
// 这份名单在前端本地一份就够，不问后端 —— 后端只管执行，前端只管别让用户
// 去点一个注定失败的动作（置灰比「点了弹个错」好）。
// ⚠️ 别拿旧的决策记录（D45 那张表）来改这份名单，第三行当时就记错过。
const KINDS_WITH_PATH_TARGET = new Set([
  'bat',
  'cmd',
  'ps1',
  'exe',
  'terminal-cmd',
  'terminal-powershell',
])

const scriptPathDisabled = computed(() => {
  const kind = selected.value?.config.kind
  return kind === undefined || !KINDS_WITH_PATH_TARGET.has(kind)
})

// 复制路径要的是 target 里的东西。target 是空的（常驻终端不填就是开一个空终端）
// 就没有「路径」可复制 —— 这时候置灰，比复制一个空串到剪贴板强
const copyScriptDisabled = computed(
  () => scriptPathDisabled.value || (selected.value?.config.target ?? '') === '',
)

// 顺序是按**语义**分的，不是按功能凑的（学习者 2026-09-29 定的）：
// 前五项都是「看一眼 / 拿一份 / 打开一个东西」，是一类；
// 「背景颜色」是改外观，跟它们不是一回事，单独隔一条线。
// 所以「最终命令」排在「背景颜色」**上面**。
const sessionMenuItems = computed(() => [
  { key: 'extractViewport', label: t('menu.extractViewport'), icon: 'terminal' },
  { key: 'openScriptDir', label: t('menu.openScriptDir'), icon: 'folder', disabled: scriptPathDisabled.value },
  { key: 'copyScriptPath', label: t('menu.copyScriptPath'), icon: 'file', disabled: copyScriptDisabled.value },
  { key: 'openWorkDir', label: t('menu.openWorkDir'), icon: 'folder' },
  { key: 'copyWorkDir', label: t('menu.copyWorkDir'), icon: 'file' },
  { key: 'commandLine', label: t('menu.commandLine'), icon: 'terminal' },
  { key: 'color', label: t('menu.color'), icon: 'pipette', separator: true },
])

// 复制 + 提示，一步做完。空串不给复制 ——
// 剪贴板里留一个上次复制的东西、界面上却弹「已复制」，用户会照着那个错的用
async function copyToClipboard(text: string, okKey: string) {
  if (text === '') {
    notify.error(t('msg.nothingToCopy'))
    return
  }
  try {
    await Clipboard.SetText(text)
    notify.success(t(okKey))
  } catch (error) {
    notify.error(`${t('msg.copyFailed')}: ${String(error)}`)
  }
}

async function onSessionMenu(key: string) {
  const session = selected.value
  if (!session) return
  const id = session.config.id
  try {
    switch (key) {
      case 'extractViewport':
        // 纯前端：内容就在本地终端的缓冲里
        openExtract()
        break
      case 'openScriptDir':
        await openScriptDir(id)
        break
      case 'copyScriptPath':
        // 纯前端：target 前端手里就有（D50），不用绕后端再要一次
        await copyToClipboard(session.config.target, 'msg.copied')
        break
      case 'openWorkDir':
        await openWorkDir(id)
        break
      case 'copyWorkDir':
        await copyToClipboard(await resolveWorkDir(id), 'msg.copied')
        break
      case 'color':
        colorDialogOpen.value = true
        break
      case 'commandLine': {
        const line = await resolveSpecCommandLine(id)
        commandLine.value = line === '' ? t('menu.noCommand') : line
        commandLineOpen.value = true
        break
      }
    }
  } catch (error) {
    // ⚠️ 后端 error 是英文的技术信息（学习者 2026-09-29 定的：error 是 debug 用的，
    // 不承载用户文案）。所以这里按「**哪个方法**失败」给一句人话，
    // 底下再挂一句原文方便他排查 —— 不去猜后端那句英文是什么意思
    notify.error(`${t(MENU_ERROR_KEY[key] ?? 'msg.actionFailed')}: ${String(error)}`)
  }
}

const MENU_ERROR_KEY: Record<string, string> = {
  openScriptDir: 'msg.openScriptDirFailed',
  copyScriptPath: 'msg.copyFailed',
  openWorkDir: 'msg.openWorkDirFailed',
  copyWorkDir: 'msg.copyFailed',
  commandLine: 'msg.commandLineFailed',
}

// —— 终端背景色（D48）——
// 两层：全局默认（顶栏主题菜单里改）+ 每会话单独（会话工具条里改），后者盖前者。
// 都存 localStorage，不进后端 data.json（D46 其一：颜色留前端）。
// 改的瞬间就往终端上生效，色板里拖一下能立刻看见，不用点「应用」
function onBackgroundChange(id: string, hex: string | null) {
  setBackground(id, hex)
  setSessionBackground(id, hex)
}

function onAppBackgroundChange(hex: string | null) {
  setAppBackground(hex)
  // 全部终端重算一遍。没单独设过的那几条跟着变，单独设过的不动
  // （manager 的 themeFor 里先看会话那份）
  applyAppBackground()
}

// —— 侧栏拖动排序 ——
// 后端要的是**全部**会话的 id（少一个就整条报错），前端保证传全。
// 写完立刻重新读一遍：以后端的排序为准。前端本地排一遍只是给用户看个即时反馈，
// 后端要是拒了（比如哪条被别处删了），load() 会把界面拉回真实顺序
async function reorder(ids: string[]) {
  try {
    await reorderSessions(ids)
  } catch (error) {
    notify.error(`${t('msg.reorderFailed')}: ${String(error)}`)
  }
  await load()
}

async function load() {
  try {
    sessions.value = await listSessions()
    if (selectedId.value && !sessions.value.some((s) => s.config.id === selectedId.value)) {
      selectedId.value = null
    }
  } catch (error) {
    notify.error(`${t('msg.loadFailed')}: ${String(error)}`)
  }
}

// 手动点「刷新」走这个：图标转起来给出等待反馈。
// 事件推送那条路本来会自动刷新状态，但**磁盘上被别的程序改了 data.json** 时
// 界面不会知道 —— 这就是这个按钮存在的理由（学习者原话：「刷新是刷新什么？根本看不明白」）。
async function refresh() {
  refreshing.value = true
  try {
    await load()
  } finally {
    refreshing.value = false
  }
}

async function loadSettings() {
  try {
    const stored = await getSettings()
    // 整份存下来，不挑字段 —— 挑漏了哪个，往回写的时候就会把那个字段抹成零值
    settings.value = stored
    // 后端不做值校验（settings.json 里写 "fr" 也原样返回），归一只能在前端做
    if (localeTouched) {
      // 用户在这段等待里已经切过语言了 —— 后端拿回来的还是他切之前的值，
      // 盖回去就等于把刚点的选择吃掉。保留用户选的那个，其余字段照常覆盖。
      settings.value = { ...stored, language: locale.value }
    } else {
      locale.value = normalizeLocale(stored.language)
      setLocale(locale.value)
    }
    if (themeTouched) {
      // 用户在这段等待里已经点过主题了 —— 后端拿回来的还是他改之前的值，
      // 盖回去就等于把刚点的选择吃掉。保留用户选的那个，其余字段照常覆盖。
      settings.value = { ...settings.value, theme: themeMode.value }
    } else {
      themeMode.value = normalizeTheme(stored.theme)
      applyTheme(themeMode.value)
    }
  } catch (error) {
    notify.error(String(error))
  }
}

// 语言切换的竞态，跟主题切换一个道理（2026-10-21 补上）：
// ① 界面挂载时 loadSettings 还在等后端返回，这期间用户已经切过语言的话，
//    loadSettings 一回来就会把旧值盖回去，看着就是「点了没反应」。
// ② 连着切两次时，先发的那次如果后到、且失败，会拿它自己记的旧值回滚，
//    把用户后一次的选择顶掉。
let localeTouched = false
let localeChangeSeq = 0

async function changeLocale(value: string) {
  const next = value as AppLocale
  const previous = locale.value
  const seq = ++localeChangeSeq
  localeTouched = true
  locale.value = next
  setLocale(next)
  try {
    await saveSettings({ ...settings.value, language: next })
    // 已经有更新的选择在了，这次的结果作废
    if (seq !== localeChangeSeq) return
    settings.value.language = next
  } catch (error) {
    if (seq !== localeChangeSeq) return
    locale.value = previous
    setLocale(previous)
    notify.error(`${t('msg.settingFailed')}: ${String(error)}`)
  }
}

// 菜单组件发的是 string，进到这里先归一再用 —— 顺手挡掉任何不是三种值之一的输入
//
// 两个竞态都要挡（2026-09-28 学习者报的「深色切回浅色偶尔不触发」）：
// ① 界面挂载时 loadSettings 还在等后端返回。这期间用户已经点过主题的话，
//    loadSettings 一回来就会把旧值盖回去，看着就是「点了没反应」。
// ② 连着点两次时，先发的那次如果后到、且失败，会拿它自己记的旧值回滚，
//    把用户后一次的选择顶掉。
let themeTouched = false
let themeChangeSeq = 0

async function changeTheme(value: string) {
  const next = normalizeTheme(value)
  const previous = themeMode.value
  const seq = ++themeChangeSeq
  themeTouched = true
  themeMode.value = next
  // 先落界面、后落盘：主题是眼前就能看见的东西，等一次来回会让切换「没反应」
  applyTheme(next)
  try {
    await saveSettings({ ...settings.value, theme: next })
    // 已经有更新的选择在了，这次的结果作废
    if (seq !== themeChangeSeq) return
    settings.value.theme = next
  } catch (error) {
    if (seq !== themeChangeSeq) return
    themeMode.value = previous
    applyTheme(previous)
    notify.error(`${t('msg.settingFailed')}: ${String(error)}`)
  }
}

// 开关的显示值直接读 settings，所以这里先乐观改一次、失败了再回滚 ——
// 不然要等一次来回，手指点下去开关纹丝不动。
// 后端 SetStartOnBoot 会同时写 settings.json 和注册表，前端不再调 saveSettings。
async function toggleStartOnBoot() {
  const next = !settings.value.startOnBoot
  const previous = settings.value.startOnBoot
  settings.value.startOnBoot = next
  startOnBootBusy.value = true
  try {
    await setStartOnBoot(next)
  } catch (error) {
    settings.value.startOnBoot = previous
    notify.error(`${t('msg.settingFailed')}: ${String(error)}`)
  } finally {
    startOnBootBusy.value = false
  }
}

function openCreate() {
  editing.value = null
  formVisible.value = true
}

function openEdit(session: SessionView) {
  editing.value = session
  formVisible.value = true
}

async function remove(session: SessionView) {
  const ok = await confirm({
    title: t('msg.removeConfirm'),
    message: session.config.name || session.config.id,
    confirmText: t('action.remove'),
    cancelText: t('action.cancel'),
    tone: 'danger',
  })
  if (!ok) return
  try {
    await deleteSession(session.config.id)
    // 终端实例也一起销毁。留着就是一份没人看得见的终端——
    // 列表里已经没这条了，内存里还挂着一整块 buffer 和它的 DOM。
    dispose(session.config.id)
    notify.success(t('msg.removed'))
    if (selectedId.value === session.config.id) selectedId.value = null
    await load()
  } catch (error) {
    notify.error(`${t('msg.removeFailed')}: ${String(error)}`)
  }
}

// —— 启停 ——
// 起新一轮之前先清屏：不然上一轮的输出和新一轮的混在一起，分不清哪句是哪次跑出来的。

async function start(session: SessionView) {
  clearTerminal(session.config.id)
  try {
    await startSession(session.config.id)
  } catch (error) {
    notify.error(`${t('msg.startFailed')}: ${String(error)}`)
  }
  await load()
}

async function stop(session: SessionView) {
  // 停会话是不可撤销的——里面正在跑的东西直接没了，没保存的也一起没。
  // 工具条上「停止」和「重启」挨着，误点的代价太大，先问一句。
  const ok = await confirm({
    title: t('msg.stopConfirm', { name: session.config.name || session.config.id }),
    message: t('msg.stopConfirmBody'),
    confirmText: t('action.stop'),
    cancelText: t('action.cancel'),
    tone: 'danger',
  })
  if (!ok) return

  try {
    await stopSession(session.config.id)
  } catch (error) {
    notify.error(`${t('msg.stopFailed')}: ${String(error)}`)
  }
  await load()
}

async function restart(session: SessionView) {
  clearTerminal(session.config.id)
  try {
    await restartSession(session.config.id)
  } catch (error) {
    notify.error(`${t('msg.startFailed')}: ${String(error)}`)
  }
  await load()
}

// —— 退出（D27 / D28）——
// 判断全在前端：还有在跑的就弹框问一句，没有就直接退。
// 停会话由后端并发做完（Hub.StopAllSessions），前端只等它返回。
async function quit() {
  const running = sessions.value.filter((s) => s.running)

  if (running.length > 0) {
    const names = running.map((s) => s.config.name || s.config.id).join('、')
    const ok = await confirm({
      title: t('msg.quitTitle'),
      message: t('msg.quitRunning', { n: running.length, names }),
      confirmText: t('msg.stopAllAndQuit'),
      cancelText: t('action.cancel'),
      tone: 'danger',
    })
    if (!ok) return

    try {
      await stopAllSessions()
    } catch (error) {
      // 已知：会话恰好在这两步之间自己退出了，hub 会把它当成失败报回来。
      // 那正是我们想要的结果，所以只记一笔，不拦住退出。
      console.warn('stopAllSessions reported an error, quitting anyway:', error)
    }
  }

  try {
    await quitApp()
  } catch (error) {
    // 退不掉就退不掉，但得说一声。
    // 静默失败的话，界面上看着像退了，进程还在那儿跑着。
    notify.error(`${t('msg.quitFailed')}: ${String(error)}`)
  }
}

// 仓库地址写死在这儿，不从后端拿：它跟着发布地址走，前端是唯一会用到它的地方。
// 要换地址（比如 fork 了）就改这一行。
const REPOSITORY_URL = 'https://github.com/uncleyumo/go-terminal-hub'

// 交给系统浏览器打开，不在 app 里开一个内嵌页：
// 内嵌页就要处理「网页里的链接点了怎么办」「后退怎么办」，一个外链不值得
async function openRepository() {
  try {
    await Browser.OpenURL(REPOSITORY_URL)
  } catch (error) {
    // 打不开浏览器是个边缘情况（默认浏览器被卸了之类），记一笔就行，
    // 不给用户弹一个他不知道怎么处理的错
    console.warn('open repository failed:', error)
  }
}

// —— 应用内更新（M7）——
//
// 状态从后端来（`update:state`），这里只决定「什么时候去查」和「新版本怎么提醒」。
// 真正干活的三个动作（下载 / 重启 / 跳过）都在 UpdateDialog 里直接调后端。

function openUpdateDialog() {
  updateOpen.value = true
  const phase = updateState.value?.state
  // 正在下载或等重启时不要再查：那会把进度和结果覆盖掉，
  // 用户重开一次弹窗就看到「正在检查」。
  if (!phase || phase === 'idle' || phase === 'latest' || phase === 'error') {
    void checkUpdate(true).catch(() => {})
  }
}

// 启动几秒后静默查一次（D58）。等一会儿是因为启动那一刻正在建窗口、
// 连 relay，再叠一个网络请求只会让首屏更慢。
// 失败不打扰：没网、被墙、GitHub 限流都只是日志里的一行。
function scheduleAutoCheck() {
  window.setTimeout(async () => {
    try {
      const st = await checkUpdate(false)
      if (st?.state === 'available' && !st.skipped) {
        notify.info(t('update.newVersionToast', { version: st.version }))
      }
    } catch {
      // 自动检查的失败一律不弹
    }
  }, 4000)
}

// 更新重启前把所有会话停掉。返回没停掉的那些（空数组 = 干净）。
//
// 不在这里自己遍历调用 stopSession：后端 StopAllSessions 才拿得到
// 「谁真的没停下来」的信息，前端那份列表可能已经过时了。
//
// 整个调用失败时，原因是另一回事（不是「某几条停不掉」），
// 单独放在 error 里，别混进会话名里显示成一句莫名其妙的话。
async function stopAllForUpdate(): Promise<{ failed: string[]; error?: string }> {
  try {
    await stopAllSessions()
  } catch (error) {
    return { failed: [], error: String(error) }
  }
  await load()
  return { failed: [] }
}

// —— 快捷键 ——
// 本版本**不做任何键盘快捷键**（学习者 2026-09-28 定的）。
// 之前加过 Ctrl+K 聚焦搜索 + Esc 清搜索，现在连监听带提示一起删干净。
// ⚠️ 真要加回来时记得用**捕获阶段**监听：xterm 在它那个隐藏 textarea 上处理按键
// 会 stopPropagation，冒泡阶段的 window 监听器收不到（表现为「快捷键没反应，
// 字母反而被敲进了终端」）。

// 订阅必须在任何启动动作之前 —— 事件不补发，订阅之前产生的输出收不到。
let unsubscribers: Array<() => void> = []

// Go 进程非零退出时 cmd.Wait() 返回 *exec.ExitError，它的 Error() 就是这一句。
// 只认「exit status N」且 N 跟实际退出码对得上 —— 对不上说明是另一回事，照旧显示。
function isExitStatus(msg: string, code: number): boolean {
  return msg.trim() === `exit status ${code}`
}

// —— 开机自启 ——
// 只能放在三个订阅之后：事件不补发，早于订阅启动的会话，它的输出前端一句都收不到。
// 跳过已在跑的：开发时前端热重载会让 onMounted 再跑一次，那时会话还在内存里，
// 重复启动会撞上 hub 的同 ID 检查报错。
async function runAutoStart() {
  const targets = sessions.value.filter((s) => s.config.autoStart && !s.running)
  for (const session of targets) {
    try {
      await startSession(session.config.id)
    } catch (error) {
      notify.error(`${t('msg.startFailed')}: ${String(error)}`)
    }
  }
}

onMounted(async () => {
  // 「跟随系统」时系统改深浅色要跟着变；别的模式下这个回调直接返回
  watchSystemTheme(() => applyTheme(themeMode.value))

  unsubscribers = [
    onSessionOutput((payload) => writeTerminal(payload.id, payload.text)),
    onSessionStarted((id) => {
      void load()
      // ConPTY 是这一刻才建出来的 —— 之前 attach 时量到的那些尺寸它一句都收不到。
      fitTerminal(id)
    }),
    // 控制权换人：手机接管 / 桌面收回。
    onSessionLease((payload) => {
      controllers.value = { ...controllers.value, [payload.id]: payload.controller }
      if (payload.controller === 'web') {
        enterViewer(payload.id, payload.cols, payload.rows)
      } else {
        // ⚠️ 自己拿回控制权时**不要**按事件里的尺寸重新 fit —— 那会绕成
        // fit → resize → 事件 → fit 的回声。exitViewer 自己会重新量。
        exitViewer(payload.id)
      }
    }),
    // PTY 的真实网格变了。本端是控制方时 setViewerSize 是空操作，
    // 所以不会出现「自己报的尺寸又回来改自己」。
    onSessionSize((payload) => {
      ptySizes.value = { ...ptySizes.value, [payload.id]: { cols: payload.cols, rows: payload.rows } }
      setViewerSize(payload.id, payload.cols, payload.rows)
    }),
    onSessionExited((payload) => {
      // 退出的提示画成一条弱化的分隔线（\x1b[2m 是变暗），
      // 不然它跟程序自己打的输出长得一模一样，看着像程序报错。
      writeTerminal(payload.id, `\r\n\x1b[2m${t('term.exited', { code: payload.code })}\x1b[0m\r\n`)
      // errMsg 是后端把 cmd.Wait() 的错误原样送过来的。进程非零退出时 Go 返回的
      // 就是一句 "exit status 1"，跟上面那句说的是同一件事 —— 同一件事说两遍
      // 看着就像报错了。别的错（文件不存在之类的）照旧显示。
      const errMsg = payload.errMsg ?? ''
      if (errMsg !== '' && !isExitStatus(errMsg, payload.code)) {
        writeTerminal(payload.id, `${errMsg}\r\n`)
      }
      void load()
    }),
    // 远程桥的连接状态：连接中/已连接/失败，以及手机在不在线。
    onRemoteStatus((status) => {
      remoteStatus.value = status
    }),
    // 更新状态：后端把 wails:updater:* 那一堆细粒度事件收成了一条，整份推过来。
    onUpdateState((st) => {
      updateState.value = st
    }),
    // 托盘右键菜单的动作。Go 侧已经把主窗口叫出来了，这里只负责接住：
    // 检查更新 → 开弹窗；退出 → 走 quit()（有会话先确认）。
    onTrayAction((action) => {
      if (action === 'check-update') openUpdateDialog()
      else if (action === 'quit') void quit()
    }),
  ]

  await loadSettings()
  await load()
  await refreshRemoteStatus()
  await runAutoStart()

  // 先拉一次当前状态，再挂上几秒后的静默检查。
  // 拉这一步是必要的：ServiceStartup 里推第一条状态时前端可能还没订阅上，
  // 事件不补发（见 api/index.ts 顶部那条注释）。
  getUpdateState()
    .then((st) => {
      if (st) updateState.value = st
    })
    .catch(() => {})
  scheduleAutoCheck()
})

onBeforeUnmount(() => {
  for (const off of unsubscribers) off()
  unsubscribers = []
  disposeAll()
})
</script>

<template>
  <div class="flex h-screen flex-col overflow-hidden bg-canvas text-ink">
    <!-- 顶栏。左侧是开关和搜索，右侧是一排同一种图标按钮；
         应用名和图标交给系统标题栏，不在这里画第二份。 -->
    <header class="flex h-11 flex-none items-center gap-3 border-b border-line bg-surface pr-2 pl-3">
      <!-- 搜索框**不再是**「一个宽度凑出来的数」，而是跟 SessionList 那行同一个盒子：
           同样的 w-[268px]，灰底那一层再 flex-1 填满剩下的。
           之前写死 w-[244px] / w-[248px] 都是拿 268 减内边距算的，
           减来减去跟它对不上（2026-09-28 学习者两次指出没对齐）。
           这样写就**没有可算错的数**——内边距解析成多少，两边都一样。

           左内边距是 **0**，不是 px-3：<header> 自己已经带了 pl-3，盒子再加一层的话
           灰底搜索框会从 24px 开始，而下面 SESSIONS 那行从 12px 开始 —— 错开 12px，
           左边看着就是一片不知道干什么的空白（2026-09-29 学习者指出）。
           只留 pr-3，右边缘照样落在 268px 上。
           折叠按钮**不在这个盒子里**（放进来会把搜索框往右推）；它住在 SESSIONS
           那一行里，收起之后顶栏只留下一个展开按钮。 -->
      <div
        class="flex flex-none items-center transition-[width] duration-200 ease-out"
        :class="sidebarCollapsed ? 'w-9 pl-1.5' : 'w-[268px] pr-3'"
      >
        <UTooltip v-if="sidebarCollapsed" :content="t('app.showSessions')">
          <UButton
            variant="ghost"
            size="sm"
            square
            icon="panelRight"
            :aria-label="t('app.showSessions')"
            :aria-expanded="false"
            @click="toggleSidebar"
          />
        </UTooltip>

        <div
          v-else
          class="flex min-w-0 flex-1 items-center gap-2 rounded-lg border border-transparent bg-sunken px-2 transition-colors duration-100 focus-within:border-accent"
        >
          <UIcon name="search" :size="14" class="flex-none text-ink-faint" />
          <input
            v-model="query"
            type="search"
            :placeholder="t('app.searchPlaceholder')"
            :aria-label="t('app.search')"
            spellcheck="false"
            autocomplete="off"
            class="min-w-0 flex-1 bg-transparent py-1.5 text-[13px] text-ink outline-none placeholder:text-ink-faint [&::-webkit-search-cancel-button]:hidden"
          />
          <button
            v-if="query"
            type="button"
            class="flex-none text-ink-faint transition-colors hover:text-ink"
            :aria-label="t('app.searchClear')"
            @click="query = ''"
          >
            <UIcon name="x" :size="13" />
          </button>
        </div>
      </div>

      <!-- 标签条。放顶栏而不是下面那条会话栏：会话栏要留 Running/Start/Stop
           的固定位置，标签挤进去会把它们推得来回动。
           默认只有「收起了会话栏」这一版才有标签；展开时下面那排列表就够了
           （设置里可以把「展开也显示」打开）。 -->
      <SessionTabs
        v-if="tabsVisible"
        :tabs="tabs"
        :current-id="selectedId"
        class="min-w-0 flex-1"
        @select="select"
        @reorder="onTabsReorder"
      />
      <div v-else class="flex-1"></div>

      <div class="mx-0.5 h-5 w-px bg-line"></div>

      <!-- 主题：常用的留在这儿，点一下就切明暗。
           终端背景色那种「改一次就不动」的，收进设置的外观里。 -->
      <UIconMenu
        :model-value="themeMode"
        :options="themeOptions"
        :label="t('theme.label')"
        @update:model-value="changeTheme"
      />

      <!-- 提示写清楚「刷新的是什么」：光一个循环箭头看不出来，
           而且点下去立刻没反应 = 点了跟没点一样，所以图标在读的时候转起来 -->
      <UTooltip :content="t('app.refreshHint')">
        <UButton
          variant="ghost"
          size="sm"
          square
          icon="refresh"
          :spin="refreshing"
          :aria-label="t('app.refreshHint')"
          @click="refresh"
        />
      </UTooltip>

      <!-- 应用信息：版本、作者、图片缓存。跟刷新一样常用，不该埋进设置里翻三层。 -->
      <UTooltip :content="t('app.info')">
        <UButton
          variant="ghost"
          size="sm"
          square
          icon="info"
          :aria-label="t('app.info')"
          @click="appInfoOpen = true"
        />
      </UTooltip>

      <!-- 其余的都收进设置：语言、开机自启、远程接管、仓库、终端底色。
           顶栏只留「一天要按好几次」的 —— 每加一个功能就再挤一个图标进来，
           挤到最后谁也不记得哪个在哪儿。
           有新版本时齿轮右上角挂一个点，跟原来 info 按钮的做法一致。 -->
      <UTooltip :content="t('app.settings')">
        <div class="relative">
          <UButton
            variant="ghost"
            size="sm"
            square
            icon="cog"
            :aria-label="t('app.settings')"
            @click="settingsOpen = true"
          />
          <span
            v-if="hasUpdate"
            class="pointer-events-none absolute top-0.5 right-0.5 h-1.5 w-1.5 rounded-full bg-accent ring-2 ring-surface"
          />
        </div>
      </UTooltip>

      <div class="mx-0.5 h-5 w-px bg-line"></div>

      <!-- 退出：收在最右、用 danger 色，别跟常用按钮混在一起误点。
           有会话在跑时 quit() 会先确认，这里不用自己拦。 -->
      <UTooltip :content="t('app.quitHint')">
        <UButton
          variant="danger"
          size="sm"
          square
          icon="power"
          :aria-label="t('app.quitHint')"
          @click="quit"
        />
      </UTooltip>
    </header>

    <div class="flex min-h-0 flex-1">
      <!-- 左：会话列表。收起时宽度归零而不是 display:none ——
           过渡才有东西可过渡，display:none 是瞬间消失，终端会「跳」一下。 -->
      <aside
        class="flex-none overflow-hidden border-r border-line transition-[width] duration-200 ease-out"
        :class="sidebarCollapsed ? 'w-0 border-r-0' : 'w-[268px]'"
      >
        <SessionList
          :sessions="sessions"
          :selected-id="selectedId"
          :query="query"
          @select="select"
          @create="openCreate"
          @edit="openEdit"
          @remove="remove"
          @reorder="reorder"
          @toggle-sidebar="toggleSidebar"
        />
      </aside>

      <!-- 右：会话详情 + 终端。
           这里**不做卡片**：窗口本身就是那个容器，终端直接铺满，
           会话标题栏和终端之间只用一条 1px 分隔线（D34）。 -->
      <main class="flex min-w-0 flex-1 flex-col bg-canvas">
        <template v-if="!selected">
          <!-- 空状态要教会用户这里能干什么，不是只说一句「没选中」 -->
          <div class="flex min-h-0 flex-1 flex-col items-center justify-center gap-3">
            <img :src="brandIcon" alt="" width="44" height="44" class="h-11 w-11" />
            <p class="text-[13px] text-ink-dim">{{ t('detail.emptyTitle') }}</p>
            <p class="max-w-[320px] text-center text-[11px] leading-relaxed text-ink-faint">
              {{ t('detail.emptyHint') }}
            </p>
            <UButton variant="default" size="sm" icon="plus" class="mt-1" @click="openCreate">
              {{ t('list.create') }}
            </UButton>
          </div>
        </template>

        <template v-else>
          <div
            class="flex h-11 flex-none items-center gap-3 border-b border-line bg-surface px-3"
          >
            <span
              class="h-1.5 w-1.5 flex-none rounded-full"
              :class="selected.running ? 'bg-pos' : 'bg-line-strong'"
            />
            <span class="flex-none text-[13px] font-semibold">
              {{ selected.config.name || selected.config.id }}
            </span>

            <span
              class="hidden min-w-0 flex-1 truncate font-mono text-[11px] text-ink-faint lg:block"
            >
              {{ selected.config.target }}
            </span>

            <span
              v-if="selected.running"
              class="hidden flex-none text-[11px] text-pos sm:block"
            >
              {{ t('list.running') }}
            </span>
            <span
              v-else-if="selected.status"
              class="hidden flex-none text-[11px] text-ink-faint tabular-nums sm:block"
            >
              {{ t('list.stopped') }} ·
              {{ t('list.exitCode', { code: selected.status.exitCode }) }}
            </span>

            <div class="flex-1 sm:hidden"></div>

            <UTooltip :content="t('detail.config')">
              <UButton
                variant="ghost"
                size="sm"
                square
                :icon="configOpen ? 'chevronUp' : 'chevronDown'"
                :aria-label="t('detail.config')"
                :class="configOpen && 'text-ink'"
                @click="configOpen = !configOpen"
              />
            </UTooltip>
            <!-- 下拉挂在「配置」左边：两个都是「展开更多」的动作，
                 Start/Stop/Restart 是一等公民，不该被挤到折叠区里去 -->
            <UDropMenu
              :items="sessionMenuItems"
              icon="more"
              :label="t('menu.label')"
              align="right"
              @select="onSessionMenu"
            />
            <UButton
              variant="primary"
              size="sm"
              icon="play"
              :disabled="selected.running"
              @click="start(selected)"
            >
              {{ t('action.start') }}
            </UButton>
            <UButton
              variant="default"
              size="sm"
              icon="stop"
              :disabled="!selected.running"
              @click="stop(selected)"
            >
              {{ t('action.stop') }}
            </UButton>
            <UButton
              variant="default"
              size="sm"
              square
              icon="restart"
              :disabled="selected.running"
              :aria-label="t('action.restart')"
              @click="restart(selected)"
            />
          </div>

          <!-- 配置：默认收起来，主要看的是终端 -->
          <div
            v-if="configOpen"
            class="flex-none border-b border-line bg-sunken/50"
          >
            <div class="grid grid-cols-2 gap-x-8 gap-y-2.5 px-4 py-3">
              <div>
                <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                  {{ t('detail.kind') }}
                </div>
                <div class="mt-0.5 font-mono text-xs text-ink">
                  {{ t(`kind.${selected.config.kind}`) }}
                </div>
              </div>
              <div>
                <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                  {{ t('detail.mode') }}
                </div>
                <div class="mt-0.5 font-mono text-xs text-ink">
                  {{ selected.config.mode }} / {{ selected.config.encoding }}
                </div>
              </div>
              <div>
                <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                  {{ t('detail.size') }}
                </div>
                <div class="mt-0.5 font-mono text-xs text-ink tabular-nums">
                  {{ selected.config.cols }} × {{ selected.config.rows }}
                </div>
              </div>
              <div>
                <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                  {{ t('detail.autoStart') }}
                </div>
                <div class="mt-0.5 font-mono text-xs text-ink">
                  {{ selected.config.autoStart ? '✓' : '—' }}
                </div>
              </div>
              <div class="col-span-2">
                <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                  {{ t('detail.target') }}
                </div>
                <div data-selectable class="mt-0.5 font-mono text-xs break-all text-ink">
                  {{ selected.config.target }}
                </div>
              </div>
              <div class="col-span-2">
                <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                  {{ t('detail.args') }}
                </div>
                <div data-selectable class="mt-0.5 font-mono text-xs break-all text-ink">
                  {{ selected.config.args || t('detail.none') }}
                </div>
              </div>
              <div class="col-span-2">
                <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                  {{ t('detail.workDir') }}
                </div>
                <div data-selectable class="mt-0.5 font-mono text-xs break-all text-ink">
                  {{ selected.config.workDir || t('detail.none') }}
                </div>
              </div>
              <div class="col-span-2">
                <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                  {{ t('detail.env') }}
                </div>
                <div
                  data-selectable
                  class="mt-0.5 font-mono text-xs break-all whitespace-pre-line text-ink"
                >
                  {{ selected.config.env?.length ? selected.config.env.join('\n') : t('detail.none') }}
                </div>
              </div>
            </div>

            <!-- 运行时：这条会话什么时候起来的、跑到哪儿了、对应哪个进程 -->
            <div class="border-t border-line px-4 py-3">
              <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                {{ t('detail.runtime') }}
              </div>
              <div class="mt-1.5 grid grid-cols-2 gap-x-8 gap-y-2.5">
                <div>
                  <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                    {{ t('detail.startedAt') }}
                  </div>
                  <div class="mt-0.5 font-mono text-xs text-ink">
                    {{ runtimeInfo.started }}
                  </div>
                </div>
                <div>
                  <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                    {{ t('detail.endedAt') }}
                  </div>
                  <div class="mt-0.5 font-mono text-xs text-ink">
                    {{ runtimeInfo.ended }}
                  </div>
                </div>
                <div>
                  <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                    {{ t('detail.duration') }}
                  </div>
                  <div class="mt-0.5 font-mono text-xs text-ink tabular-nums">
                    {{ runtimeInfo.duration }}
                  </div>
                </div>
                <div>
                  <div class="text-[10.5px] tracking-wide text-ink-faint uppercase">
                    {{ t('detail.pid') }}
                  </div>
                  <div class="mt-0.5 font-mono text-xs text-ink tabular-nums">
                    {{ runtimeInfo.pid }}
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- 手机正在控制这个会话时的提示条。
               终端会缩成一个按 PTY 真实网格（比如 48×20）居中显示的小格子——
               那不是显示错误，尺寸本来就属于 PTY，不属于窗口。 -->
          <div
            v-if="selectedByWeb"
            class="flex flex-none items-center gap-2 border-t border-line bg-warn/10 px-3 py-2 text-xs text-ink"
          >
            <span>
              {{ t('remote.controlledByWeb') }}
              <template v-if="selectedPtySize">
                · {{ selectedPtySize.cols }} × {{ selectedPtySize.rows }}
              </template>
            </span>
            <div class="flex-1"></div>
            <UButton variant="ghost" size="sm" @click="takeBackControl">
              {{ t('remote.takeBack') }}
            </UButton>
          </div>

          <!-- key 换行时会重新挂载：manager 里旧实例留着，容器换到新的 host 上 -->
          <TerminalPane :key="selected.config.id" :session-id="selected.config.id" />
        </template>
      </main>
    </div>

    <SessionForm v-model="formVisible" :session="editing" :taken-names="takenNames" @saved="load" />
    <UToaster />
    <UConfirmHost />

    <!-- 会话级色板。没单独设过的会话 initial = null，
         色板打开在**全局默认**上（全局也没设就是主题自带的那个），
         不是随便一个深色 -->
    <TermBackgroundDialog
      v-if="selected"
      v-model="colorDialogOpen"
      :initial="getBackground(selected.config.id)"
      :fallback="getAppBackground()"
      :title="t('menu.color')"
      @change="onBackgroundChange(selected.config.id, $event)"
    />

    <!-- 全局默认色板。改的是「所有没单独设过的会话」，
         已经单独设过的那几条不受影响 -->
    <TermBackgroundDialog
      v-model="appColorOpen"
      :initial="getAppBackground()"
      :title="t('theme.terminalBackgroundTitle')"
      @change="onAppBackgroundChange"
    />

    <!-- 提取出来的视口内容：可改、可删，满意了再复制。
         终端里选字不顺手时（例如要用划词翻译），从这里拿一段干净的文字走 -->
    <UDialog v-model="extractOpen" :title="t('extract.title')" width="720px">
      <p class="mb-2 text-xs leading-relaxed text-ink-faint">{{ t('extract.hint') }}</p>
      <UTextarea v-model="extractText" :rows="14" mono />
      <template #footer>
        <UButton variant="default" size="sm" @click="extractOpen = false">
          {{ t('action.close') }}
        </UButton>
        <UButton variant="primary" size="sm" @click="copyToClipboard(extractText, 'msg.copied')">
          {{ t('extract.copy') }}
        </UButton>
      </template>
    </UDialog>

    <!-- 最终命令：只给看，不给改。用户在这里核对拼出来的那条对不对 -->
    <UDialog
      v-model="commandLineOpen"
      :title="t('menu.commandLine')"
      width="640px"
    >
      <pre
        class="max-h-[40vh] overflow-auto rounded-lg border border-line bg-sunken p-3 font-mono text-xs leading-5 break-all whitespace-pre-wrap text-ink"
        >{{ commandLine }}</pre
      >
      <template #footer>
        <UButton variant="default" size="sm" @click="commandLineOpen = false">
          {{ t('action.close') }}
        </UButton>
        <UButton variant="primary" size="sm" @click="copyToClipboard(commandLine, 'msg.copied')">
          {{ t('menu.copyCommandLine') }}
        </UButton>
      </template>
    </UDialog>
    <AppInfoDialog v-model="appInfoOpen" />
    <UpdateDialog
      v-model="updateOpen"
      :state="updateState"
      :running-count="runningSessions.length"
      :stop-all-sessions="stopAllForUpdate"
    />
    <RemoteSettingsDialog
      v-model="remoteOpen"
      :config="remoteConfig"
      :status="remoteStatus"
      :saving="remoteSaving"
      @save="saveRemoteConfig"
    />

    <!-- 设置：顶栏那排按钮里「改一次就不动」的都挪到这儿。
         左栏选一项、右栏只显示那一项，不做「一页滚到底」。 -->
    <SettingsDialog
      v-model="settingsOpen"
      :settings="settings"
      :locale="locale"
      :theme-mode="themeMode"
      :always-tabs="alwaysTabs"
      :start-on-boot-busy="startOnBootBusy"
      :remote-enabled="settings.remote.enabled"
      :remote-state="remoteStatus?.state || 'disabled'"
      :has-update="hasUpdate"
      @update:always-tabs="setAlwaysTabs"
      @locale="changeLocale"
      @theme="changeTheme"
      @start-on-boot="toggleStartOnBoot"
      @feature="setFeature"
      @open-remote="remoteOpen = true"
      @open-about="appInfoOpen = true"
      @open-background="appColorOpen = true"
      @open-repo="openRepository"
      @open-update="settingsOpen = false; openUpdateDialog()"
    />
  </div>
</template>
