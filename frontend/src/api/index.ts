// 唯一碰 bindings 的地方。组件只 import 这里的函数，不直接 import bindings。
import { Events } from '@wailsio/runtime'
import { AppService, HubService, StoreService } from '../../bindings/github.com/uncleyumo/go-terminal-hub'
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

// —— 退出：停会话 + 退程序 ——
// 「有几个在跑的」不由后端回答，前端拿 listSessions() 的结果自己数。
export function stopAllSessions(): Promise<void> {
  return HubService.StopAllSessions()
}

export function quitApp(): Promise<void> {
  return AppService.QuitApp()
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
