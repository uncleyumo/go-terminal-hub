// 唯一碰 bindings 的地方。组件只 import 这里的函数，不直接 import bindings。
import { Events } from '@wailsio/runtime'
import { AppService, HubService, StoreService } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/service'
import type { DataStore } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/store/models'
import type { Settings } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/store/models'
import type { RecordStatus } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/hub/models'
import type { ExitPayload } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/sink/models'
import type { OutputPayload } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/sink/models'

// 绑定是生成物，形状会跟着生成参数变：
// Taskfile 里统一带 -i（生成 interface，不是 class），返回值一律可空。
// 所以这里只用类型、只用对象字面量，不 new、不假设非空。
export type { DataStore, Settings, RecordStatus, OutputPayload, ExitPayload }

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
// ⚠️ 后端是从**运行中的**会话里读的（`hub.GetSession` → `GetLaunchSpec`），
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
