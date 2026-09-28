// 全局确认框（替代 ElMessageBox.confirm）。
// 调用形态跟原生 confirm 一样是 await 出一个 true/false：
//   if (!(await confirm({ ... }))) return
import { reactive } from 'vue'

export interface ConfirmOptions {
  title: string
  message: string
  confirmText: string
  cancelText: string
  /** 破坏性动作（删除、停掉所有会话后退出）用 danger，主体按钮变红 */
  tone?: 'primary' | 'danger'
}

export interface ConfirmState extends ConfirmOptions {
  open: boolean
}

export const confirmState = reactive<ConfirmState>({
  open: false,
  title: '',
  message: '',
  confirmText: '',
  cancelText: '',
  tone: 'primary',
})

let settle: ((value: boolean) => void) | undefined

export function confirm(options: ConfirmOptions): Promise<boolean> {
  // 同一时刻只允许一个确认框。上一框还开着就再弹一个，settle 会被覆盖，
  // 那个 await 会永远挂着 —— 所以先把旧的按「取消」结掉。
  if (settle) settle(false)
  Object.assign(confirmState, options, { open: true })
  return new Promise<boolean>((resolve) => {
    settle = resolve
  })
}

/** 由确认框组件调用。resolve 之后清掉 settle，免得下一次误调。 */
export function resolveConfirm(value: boolean) {
  confirmState.open = false
  const done = settle
  settle = undefined
  done?.(value)
}
