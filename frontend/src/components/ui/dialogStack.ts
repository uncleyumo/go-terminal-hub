// 当前打开着的弹窗，按打开顺序排。
//
// 必须是**模块级**的一份：写在 UDialog 的 `<script setup>` 里的话，
// 每个弹窗实例会各有一份空数组，于是每层都算成第 0 层、z-index 全是 40，
// 谁在上面又退回成「组件写在模板里的先后」——从设置里开「远程访问」，
// 远程那个窗口会藏在设置底下；Esc 也会两层一起关。
import { shallowRef } from 'vue'

const stack = shallowRef<symbol[]>([])

export function pushDialog(id: symbol) {
  stack.value = [...stack.value, id]
}

export function popDialog(id: symbol) {
  const at = stack.value.indexOf(id)
  if (at < 0) return
  const next = stack.value.slice()
  next.splice(at, 1)
  stack.value = next
}

/** 第几层；没开着时 -1。 */
export function dialogDepth(id: symbol): number {
  return stack.value.indexOf(id)
}

/** 是不是最上面那层（Esc 和 Tab 只归它管）。 */
export function dialogIsTopmost(id: symbol): boolean {
  return stack.value[stack.value.length - 1] === id
}
