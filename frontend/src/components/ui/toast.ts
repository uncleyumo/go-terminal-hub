// 全局提示（替代 ElMessage）。
// 状态放在模块里、组件挂在 App 根上：这样任何一层（包括 api 之外的工具模块）
// 都能直接调 notify.success(...)，不用把一个函数一层层往下传。
import { reactive } from 'vue'

export type ToastKind = 'success' | 'error' | 'info'

export interface ToastItem {
  id: number
  kind: ToastKind
  text: string
}

export const toasts = reactive<ToastItem[]>([])

let seq = 0

function push(kind: ToastKind, text: string, ttl: number) {
  const id = ++seq
  toasts.push({ id, kind, text })
  window.setTimeout(() => {
    const i = toasts.findIndex((t) => t.id === id)
    if (i >= 0) toasts.splice(i, 1)
  }, ttl)
  return id
}

export function dismiss(id: number) {
  const i = toasts.findIndex((t) => t.id === id)
  if (i >= 0) toasts.splice(i, 1)
}

export const notify = {
  // 成功的提示不用停太久；报错要停够久，用户得能读完
  success: (text: string) => push('success', text, 2600),
  error: (text: string) => push('error', text, 6000),
  info: (text: string) => push('info', text, 3200),
}
