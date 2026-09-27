// 唯一碰 bindings 的地方。组件只 import 这里的函数，不直接 import bindings。
import { HubService, StoreService } from '../../bindings/github.com/uncleyumo/go-terminal-hub'
import type { DataStore } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/store/models'
import type { Settings } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/store/models'
import type { RecordStatus } from '../../bindings/github.com/uncleyumo/go-terminal-hub/internal/exec/hub/models'

// 绑定是生成物，形状会跟着生成参数变：
// Taskfile 里统一带 -i（生成 interface，不是 class），返回值一律可空。
// 所以这里只用类型、只用对象字面量，不 new、不假设非空。
export type { DataStore, Settings, RecordStatus }

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
