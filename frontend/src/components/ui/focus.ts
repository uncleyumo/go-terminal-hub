// 弹窗（确认框、对话框）共用的两件事：
//
//   1. 打开时把焦点挪进弹窗，关上时还给原来那个元素
//   2. Tab 在弹窗里转圈，别跑到背后的页面上去
//
// 为什么要管：键盘用户靠 Tab 走位。焦点留在弹窗外面，他看不见自己在哪儿，
// 也不知道回车会按到哪个按钮上——按一次「删除」可能就出去了。

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'

/** 弹窗里能聚焦的元素，按 Tab 顺序。看不见的不算（display:none 那种）。 */
export function focusableElements(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) => el.getClientRects().length > 0,
  )
}

/**
 * 记住当前的焦点，返回一个「还回去」的函数。
 *
 * 弹窗开着的时候用户按了 Tab，焦点早就跑别处去了；关的时候要还的是**开之前**
 * 那个元素，不是关的那一刻的那个。
 */
export function captureFocus(): () => void {
  const previous = document.activeElement as HTMLElement | null
  return () => {
    previous?.focus?.()
  }
}

/**
 * 把焦点挪进弹窗。
 *
 * prefer 是「优先落在哪」。表单弹窗把它设成内容区，焦点就落在第一个输入框上
 * （用户点开就能打字）；内容区里没有能聚焦的东西，就退回整个弹窗的第一个。
 */
export function focusInto(root: HTMLElement, prefer?: HTMLElement | null) {
  const preferred = prefer ? focusableElements(prefer) : []
  const items = preferred.length > 0 ? preferred : focusableElements(root)
  const target = items[0] ?? root
  target.focus()
}

/** Tab 按到边界时在弹窗里转圈。 */
export function trapFocus(root: HTMLElement, e: KeyboardEvent) {
  if (e.key !== 'Tab') return
  const items = focusableElements(root)
  if (items.length === 0) {
    // 一个能聚焦的元素都没有：把焦点钉在弹窗上，别让它跑到外面去
    e.preventDefault()
    root.focus()
    return
  }
  const first = items[0]
  const last = items[items.length - 1]
  const active = document.activeElement
  if (!root.contains(active)) {
    e.preventDefault()
    ;(e.shiftKey ? last : first).focus()
    return
  }
  if (e.shiftKey && active === first) {
    e.preventDefault()
    last.focus()
  } else if (!e.shiftKey && active === last) {
    e.preventDefault()
    first.focus()
  }
}
