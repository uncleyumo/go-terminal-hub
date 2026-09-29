<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import SessionList from './components/SessionList.vue'
import SessionForm from './components/SessionForm.vue'
import TerminalPane from './components/TerminalPane.vue'
import TermBackgroundDialog from './components/TermBackgroundDialog.vue'
import UButton from './components/ui/UButton.vue'
import UConfirmHost from './components/ui/UConfirmHost.vue'
import UDialog from './components/ui/UDialog.vue'
import UDropMenu from './components/ui/UDropMenu.vue'
import UIcon from './components/ui/UIcon.vue'
import UIconMenu from './components/ui/UIconMenu.vue'
import USwitch from './components/ui/USwitch.vue'
import UToaster from './components/ui/UToaster.vue'
import UTooltip from './components/ui/UTooltip.vue'
import { confirm } from './components/ui/confirm'
import { notify } from './components/ui/toast'
import { Browser, Clipboard } from '@wailsio/runtime'
import {
  deleteSession,
  getSettings,
  listSessions,
  onSessionExited,
  onSessionOutput,
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
  type Settings,
  type SessionView,
} from './api'
import {
  applyAppBackground,
  clear as clearTerminal,
  disposeAll,
  fit as fitTerminal,
  setSessionBackground,
  write as writeTerminal,
} from './terminal/manager'
import { getAppBackground, getBackground, setAppBackground, setBackground } from './terminal/background'
import { normalizeLocale, setLocale, type AppLocale } from './i18n'
import { applyTheme, normalizeTheme, watchSystemTheme, type ThemeMode } from './theme'
import brandIcon from './assets/brand/icon.svg'

const { t } = useI18n()

const sessions = ref<SessionView[]>([])
const selectedId = ref<string | null>(null)
const formVisible = ref(false)
const editing = ref<SessionView | null>(null)
const configOpen = ref(false)
const query = ref('')

// 后端会把它存的那份原样回给我们，包括我们不认识的字段，这里保持整份往回写。
const settings = ref<Settings>({ language: 'en', theme: 'light', startOnBoot: false })
const locale = ref<AppLocale>('en')
const themeMode = ref<ThemeMode>('system')
const startOnBootBusy = ref(false)
const refreshing = ref(false)

// 色板小窗（会话级 + 全局级，两个实例）+ 「最终命令」那一条命令行的只读小窗
const colorDialogOpen = ref(false)
const appColorOpen = ref(false)
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

function toggleSidebar() {
  sidebarCollapsed.value = !sidebarCollapsed.value
  try {
    window.localStorage.setItem(SIDEBAR_KEY, sidebarCollapsed.value ? '1' : '0')
  } catch {
    // 存不进去只是下次不记住，折叠本身照样能用
  }
}

const localeOptions = [
  { value: 'en', label: 'English', icon: 'globe' },
  { value: 'zh-CN', label: '简体中文', icon: 'globe' },
]

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
    // 值为空串（文件里没写 / 后端没兜默认值）时回落到默认值
    locale.value = normalizeLocale(stored.language)
    setLocale(locale.value)
    // 后端不做值校验（settings.json 里写 "fr" 也原样返回），归一只能在前端做
    if (themeTouched) {
      // 用户在这段等待里已经点过主题了 —— 后端拿回来的还是他改之前的值，
      // 盖回去就等于把刚点的选择吃掉。保留用户选的那个，其余字段照常覆盖。
      settings.value = { ...stored, theme: themeMode.value }
    } else {
      themeMode.value = normalizeTheme(stored.theme)
      applyTheme(themeMode.value)
    }
  } catch (error) {
    notify.error(String(error))
  }
}

async function changeLocale(value: string) {
  const next = value as AppLocale
  locale.value = next
  setLocale(next)
  try {
    await saveSettings({ ...settings.value, language: next })
    settings.value.language = next
  } catch (error) {
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

  await quitApp()
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
  ]

  await loadSettings()
  await load()
  await runAutoStart()
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

      <div class="flex-1"></div>

      <UIconMenu
        :model-value="locale"
        :options="localeOptions"
        :label="t('app.language')"
        @update:model-value="changeLocale"
      />

      <!-- 「登录时启动」放在主题按钮左边、用开关而不是图标（图标那个 →| 看不出是什么）。
           它原来被放在顶栏最左边的搜索框前面，把搜索框往右顶了约 200px，
           搜索框左边缘就跟下面的 SESSIONS 栏对不上了（2026-09-28 学习者指出）。 -->
      <UTooltip :content="t('app.startOnBootHint')">
        <label
          class="flex flex-none cursor-pointer items-center gap-2 rounded-md px-1.5 py-1 transition-colors duration-100 hover:bg-raised"
        >
          <span class="text-[11px] whitespace-nowrap text-ink-dim">{{ t('app.startOnBoot') }}</span>
          <USwitch
            :model-value="settings.startOnBoot"
            :disabled="startOnBootBusy"
            @update:model-value="toggleStartOnBoot"
          />
        </label>
      </UTooltip>

      <!-- 主题菜单底下挂「终端背景色…」：改的是**全局默认**，
           会话工具条里那个改的是**这一条**。两层分开存、后者盖前者 ——
           单独改过的那几条不受全局影响，全局改完没单独改过的跟着变。 -->
      <UIconMenu
        :model-value="themeMode"
        :options="themeOptions"
        :label="t('theme.label')"
        :action="{ label: t('theme.terminalBackground'), icon: 'pipette' }"
        @update:model-value="changeTheme"
        @action="appColorOpen = true"
      />

      <div class="mx-0.5 h-5 w-px bg-line"></div>

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
      <UTooltip :content="t('app.quitHint')">
        <UButton
          variant="ghost"
          size="sm"
          square
          icon="power"
          :aria-label="t('app.quitHint')"
          @click="quit"
        />
      </UTooltip>

      <div class="mx-0.5 h-5 w-px bg-line"></div>

      <!-- 仓库链接排在最右、单独隔一条线：它是「离开这个 app」的动作，
           跟左边那排操作 app 的按钮不是一类，混在里面用户会当成某个设置 -->
      <UTooltip :content="t('app.repository')">
        <UButton
          variant="ghost"
          size="sm"
          square
          icon="github"
          :aria-label="t('app.repository')"
          @click="openRepository"
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
            class="grid flex-none grid-cols-2 gap-x-8 gap-y-2.5 border-b border-line bg-sunken/50 px-4 py-3"
          >
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
  </div>
</template>
