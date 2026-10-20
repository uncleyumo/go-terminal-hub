// 唯一碰 bindings 的地方。组件只 import 这里的函数，不直接 import bindings。
import { Events } from '@wailsio/runtime'
import { AppService, HubService, RemoteService, ScreenService, StoreService, UpdateService } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/service'
import type { DataStore } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/store/models'
import type { Settings } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/store/models'
import type { RecordStatus } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/hub/models'
import type { ExitPayload } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/sink/models'
import type { OutputPayload } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/sink/models'
import type { LeasePayload } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/sink/models'
import type { SizePayload } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/sink/models'
import type { Status as RemoteStatusModel } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/remote/models'
import type { BuildInfo } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/buildinfo/models'
import type { Info as PasteCacheInfoModel } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/paste/models'
import type { ProbeResult as RemoteProbeResultModel } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/service/models'
import type { UpdateState as UpdateStateModel } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/service/models'

// 绑定是生成物，形状会跟着生成参数变：
// Taskfile 里统一带 -i（生成 interface，不是 class），返回值一律可空。
// 所以这里只用类型、只用对象字面量，不 new、不假设非空。
export type { DataStore, Settings, RecordStatus, OutputPayload, ExitPayload, BuildInfo }

// —— 应用信息：版本号 + 构建时间 ——
// 这两个值是链接器在 go build 时用 -X 写进 Go 包级变量的（见 build/windows/Taskfile.yml），
// 不是运行时算的，所以「现在几点」对它没意义 —— 它记的是这个 exe 是几点编出来的。
export function getBuildInfo(): Promise<BuildInfo> {
  return AppService.GetBuildInfo()
}

/** 在资源管理器里打开当前 exe 所在目录（并选中它）。 */
export function openExeDir(): Promise<void> {
  return AppService.OpenExeDir()
}

// —— 手机发过来的图片缓存 ——
//
// 手机在远程终端里发一张图，hub 会把它落到 ~/.go-terminal-hub/paste 下、
// 再把路径打进终端（claude code 是按路径认图片的）。文件不会自己消失，
// 所以在「关于」里给一个清空入口。

export type PasteCacheInfo = PasteCacheInfoModel

export function getPasteCacheInfo(): Promise<PasteCacheInfo> {
  return AppService.GetPasteCacheInfo()
}

/** 返回的是清掉的那部分，调用方拿它拼「清了多少」的提示。 */
export function clearPasteCache(): Promise<PasteCacheInfo> {
  return AppService.ClearPasteCache()
}

// 一行会话 = 配置（store，跨重启活着）+ 运行状态（hub，只在内存）。
// 没有 status 的意思是「这条配置从没跑过」，是正常状态，不是异常。
export interface SessionView {
  config: DataStore
  status?: RecordStatus
  running: boolean
}

export async function listSessions(): Promise<SessionView[]> {
  const [configs, hubStatuses] = await Promise.all([
    StoreService.ListSessions(),
    HubService.ListStatus(),
  ])
  const byId = new Map<string, RecordStatus>()
  for (const status of hubStatuses ?? []) {
    byId.set(status.id, status)
  }
  return (configs ?? []).map((config) => {
    const status = byId.get(config.id)
    return { config, status, running: status?.running === true }
  })
}

export function createSession(config: DataStore): Promise<string> {
  return StoreService.CreateSession(config)
}

export function updateSession(id: string, config: DataStore): Promise<string> {
  return StoreService.UpdateSession(id, config)
}

export function deleteSession(id: string): Promise<void> {
  return StoreService.DeleteSession(id)
}

export function getSettings(): Promise<Settings> {
  return StoreService.GetSettings()
}

export function saveSettings(settings: Settings): Promise<void> {
  return StoreService.SaveSettings(settings)
}

// app 进程当前的工作目录。新建配置时拿它当工作目录的默认值 ——
// 用户不填也能在详情里看见到底是哪个目录，用着踏实。
// 拿不到就返回空串，表单退回「留空 = 继承当前目录」的老行为。
export async function getAppWorkDir(): Promise<string> {
  try {
    return await AppService.GetAppWorkDir()
  } catch (error) {
    console.debug('get app work dir failed', error)
    return ''
  }
}

// —— 登录自启（D31/D32）——
// 后端 SetStartOnBoot 自己会写 settings.json（和注册表一起），前端写完不用再 saveSettings。
// 读不用单开一口：getSettings() 返回的那份里就带 startOnBoot。
export function setStartOnBoot(enabled: boolean): Promise<void> {
  return AppService.SetStartOnBoot(enabled)
}

// —— 运行控制：都是 hub 的转发，hub 里管的是运行状态 ——

// 撞上还在跑的同 ID 会在 hub 里报错（D15），让用户先停
export function startSession(id: string): Promise<void> {
  return HubService.StartSession(id)
}

export function stopSession(id: string): Promise<void> {
  return HubService.StopSession(id)
}

// 只允许「已停止」的重启，跑着的不给重启
export function restartSession(id: string): Promise<void> {
  return HubService.RestartSession(id)
}

// 键盘输入这一路。返回写了多少字节，前端用不上。
// Ctrl+C 也走这里（xterm 把它编成 \x03）—— 和「停止」按钮是同一条路。
export function writeSession(id: string, data: string): Promise<number> {
  return HubService.WriteSession(id, data)
}

// 终端画布尺寸变了，告诉后端的 ConPTY。会话没在跑时后端会报错，调用方自己吞掉。
export function resizeSession(id: string, cols: number, rows: number): Promise<void> {
  return HubService.ResizeSession(id, cols, rows)
}

// —— 退出：停会话 + 退程序 ——
// 「有几个在跑的」不由后端回答，前端拿 listSessions() 的结果自己数。
export function stopAllSessions(): Promise<void> {
  return HubService.StopAllSessions()
}

export function quitApp(): Promise<void> {
  return AppService.QuitApp()
}

// —— 会话工具条的下拉菜单（D47 / D50）——
// 四个 AppService 方法，都是「拿一个会话 id 干一件事」。
//
// ⚠️ 后端 error 一律英文直出（D22 那条欠账就是这个），**不承载用户文案**。
// 所以翻译放在调用方：谁调谁负责按「哪个方法失败」给一句人话，
// 不用去猜后端那句英文是什么意思。
export function openScriptDir(id: string): Promise<void> {
  return AppService.OpenScriptDir(id)
}

export function openWorkDir(id: string): Promise<void> {
  return AppService.OpenWorkDir(id)
}

export function resolveWorkDir(id: string): Promise<string> {
  return AppService.ResolveWorkDir(id)
}

// 「这条会话实际会跑什么」—— 用户拿它核对命令行。
// ⚠️ 后端是从**运行中的**会话里读的（`hub.LaunchSpec`），
// 所以没启动过的会话会 reject，这是设计如此，不是出错。
export function resolveSpecCommandLine(id: string): Promise<string> {
  return AppService.ResolveSpecCommandLine(id)
}

// 侧栏拖动排序。**ids 必须是全部会话的 id，一个不多一个不少**——
// 后端 `Store.ReorderSessions` 拿 `len(ids)` 跟 `len(dataList)` 比，
// 少一个就整条报错。少传了不会静默出错，会 reject，前端 catch 里重新 load 一次就恢复原样。
export function reorderSessions(ids: string[]): Promise<void> {
  return StoreService.ReorderSessions(ids)
}

// —— 事件：只有这里认 wails 的事件名，组件只认这三个函数 ——
// 返回的都是取消订阅的函数，订阅方在卸载时调一下。
//
// ⚠️ 事件不补发：会话在订阅之前产生的输出，订阅之后收不到。
// 所以订阅必须发生在任何启动动作之前（AutoStart 那条尤其要注意）。

export function onSessionOutput(cb: (payload: OutputPayload) => void): () => void {
  return Events.On('session:output', (event) => cb(event.data))
}

export function onSessionExited(cb: (payload: ExitPayload) => void): () => void {
  return Events.On('session:exited', (event) => cb(event.data))
}

export function onSessionStarted(cb: (id: string) => void): () => void {
  return Events.On('session:started', (event) => cb(event.data))
}

// —— 控制权租约（手机远程接管）——
//
// 每个会话在任意时刻只有一个「控制方」：desktop 或 web。只有它能发输入、改尺寸，
// PTY 的尺寸跟着控制方走。另一方是只读旁观者，按 PTY 的真实网格渲染。
//
// 手机接管时桌面会看到：终端缩成一个 48×20 的小格子居中显示，上面挂一条提示条。
// 这不是 bug —— 尺寸属于 PTY，不属于窗口。桌面要是还按自己窗口的尺寸渲染，
// 就会和 PTY 实际在重绘的网格对不上，画面全错。

export type SessionLease = LeasePayload
export type SessionSize = SizePayload
export type RemoteStatus = RemoteStatusModel

export function onSessionLease(cb: (payload: LeasePayload) => void): () => void {
  return Events.On('session:lease', (event) => cb(event.data))
}

export function onSessionSize(cb: (payload: SizePayload) => void): () => void {
  return Events.On('session:size', (event) => cb(event.data))
}

// 桌面端「收回控制」。
//
// cols/rows 必须显式传：旁观时容器是按手机的网格撑开的，得先恢复自己的布局、
// 量出真实尺寸再交过来，否则 PTY 会停在手机那个尺寸上。
export function takeControl(id: string, cols: number, rows: number): Promise<void> {
  return HubService.TakeControl(id, cols, rows)
}

export function controllerOf(id: string): Promise<string> {
  return HubService.Controller(id)
}

// —— 远程接管设置 ——

export function getRemoteStatus(): Promise<RemoteStatus> {
  return RemoteService.GetRemoteStatus()
}

// 32 字节随机再 base64。这个密钥的持有者能把任意按键送进本机终端，
// 强度不能靠人自己想。
export function generateRemoteSecret(): Promise<string> {
  return RemoteService.GenerateSecret()
}

// 「测试连接」的结果。Message 只有失败时有内容（后端报的错原文）；
// 成功时由前端出本地化文案，所以别拿它当用户可见的正文。
export type RemoteProbeResult = RemoteProbeResultModel

/**
 * 拨一次 relay，看地址和 key 对不对。不落盘、不启用。
 *
 * 已经连着同一个地址时后端不会真拨——relay 上 agent 只有一个位置，
 * 试拨会把正在用的那条顶掉。那种情况直接拿现成的连接状态当结果。
 */
export function testRemoteConnection(url: string, key: string): Promise<RemoteProbeResult> {
  return RemoteService.TestConnection(url, key)
}

export function onRemoteStatus(cb: (status: RemoteStatus) => void): () => void {
  return Events.On('remote:status', (event) => cb(event.data))
}

// —— 应用内更新 ——
//
// 后端把更新器那十几条 `wails:updater:*` 事件（check-started / download-progress /
// verifying / update-ready …）收成了一条 `update:state`，每次推**整份**状态。
// 前端和托盘都只订阅这一条，不用自己拼状态机。
//
// 状态取值（后端 internal/service/update_service.go）：
//   disabled    dev 构建或更新器起不来，不检查
//   idle        已初始化，还没查过
//   checking    正在查
//   latest      已是最新
//   available   有新版本，可以下载
//   downloading 下载/校验/装到暂存区（三步合并成一个状态）
//   ready       下载校验都过了，等重启
//   error       出错，Error 字段里是给用户看的原文
export type UpdateState = UpdateStateModel

export function getUpdateState(): Promise<UpdateState> {
  return UpdateService.GetState()
}

/** manual 只影响后端日志：自动检查失败不打扰用户（D58）。 */
export function checkUpdate(manual: boolean): Promise<UpdateState> {
  return UpdateService.Check(manual)
}

/** 下载 + 校验 + 装到暂存区。**不替换**，替换在 restartForUpdate 里。 */
export function installUpdate(): Promise<UpdateState> {
  return UpdateService.Install()
}

export function cancelUpdateDownload(): Promise<void> {
  return UpdateService.CancelDownload()
}

/**
 * 起 helper、退出当前进程、换 exe、再启动新版。
 *
 * 调用之前必须把会话停干净（D60）：这个方法返回之后，进程就不受控了。
 */
export function restartForUpdate(): Promise<void> {
  return UpdateService.Restart()
}

export function skipUpdateVersion(version: string): Promise<void> {
  return UpdateService.SkipVersion(version)
}

export function openReleasesPage(): Promise<void> {
  return UpdateService.OpenReleasesPage()
}

export function onUpdateState(cb: (state: UpdateState) => void): () => void {
  return Events.On('update:state', (event) => cb(event.data))
}

// 托盘右键菜单要主窗口做的事。Go 侧已经先把窗口叫出来了，
// 这里只负责把动作接进前端现有的弹窗和确认流程。
//   check-update —— 打开更新弹窗并查一次
//   quit         —— 走 App.vue 里那个 quit()（有会话先确认）
export function onTrayAction(cb: (action: string) => void): () => void {
  return Events.On('tray:action', (event) => cb(event.data))
}

// —— 屏幕取色 ——
//
// 浏览器的 `EyeDropper` API 在 WebView2 里是「接口在、功能废」：
// `window.EyeDropper` 是 defined 的，但 `.open()` 毫秒级抛 AbortError——
// 取色镜那一层 UI 必须由宿主程序提供，WebView2 没实现。
// 所以这条路改由 Go 截屏 + 前端读像素，见 internal/service/screen_service.go。

/** 抓一张屏幕图（data URL），同时把主窗口铺满屏幕并置顶。 */
export function beginScreenPick(): Promise<string> {
  return ScreenService.BeginScreenPick()
}

/** 还原窗口。**必须调**——忘了调，用户会被留在一个全屏置顶的窗口里。 */
export function endScreenPick(): Promise<void> {
  return ScreenService.EndScreenPick()
}
