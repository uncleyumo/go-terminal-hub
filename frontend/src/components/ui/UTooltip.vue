<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useTemplateRef } from 'vue'

// 提示气泡。渲染到 body 上（Teleport），所以放在会滚动的列表里也不会被裁掉。
//
// ⚠️ 位置按触发器在哪半边来定：工具栏那几个按钮贴着窗口右边缘，按**右**边缘
// 往左展开才不出界（学习者 2026-09-28 指出过）；侧边栏里的按钮在左半边，
// 按左边缘往右展开。
//
// ⚠️ 宽度**不写死**：写死 240px 的话，「Theme」「Language」这种两三个词的提示
// 也会撑成一条 240px 长的空盒子（学习者 2026-09-28 指出过）。现在是 w-max 由内容
// 撑开、最多 240px。宽度按内容算，量之前不知道多宽，所以定位不靠猜宽度 ——
// 右半边就贴着触发器的右边缘、用 translateX(-100%) 往左展，展开后再量一次真宽度
// 把超出窗口的部分收回来。
//
// ⚠️ 触发器上**不要用原生 title 属性**：原生 tooltip 由系统画，
// 跟界面主题无关 —— 系统是深色时它就是一块深灰，跟同一主题下自绘的浅色气泡
// 摆在一起，颜色对不上（同一处两种底色）。要提示就走这个组件。
const props = withDefaults(
  defineProps<{ content: string; side?: 'top' | 'bottom'; delay?: number }>(),
  { side: 'bottom', delay: 300 },
)

const anchor = useTemplateRef<HTMLElement>('anchor')
const bubble = useTemplateRef<HTMLElement>('bubble')
const open = ref(false)
const pos = ref({ left: 0, top: 0, fromRight: false })

let timer: number | undefined

function place() {
  const el = anchor.value
  if (!el) return
  const r = el.getBoundingClientRect()
  const fromRight = r.left > window.innerWidth / 2
  pos.value = {
    left: fromRight ? r.right : r.left,
    top: props.side === 'top' ? r.top - 8 : r.bottom + 8,
    fromRight,
  }
}

// 展开之后才知道气泡多宽，窗口太窄时把它往左推回来
function clampToWindow() {
  const el = bubble.value
  if (!el) return
  const overflow = el.getBoundingClientRect().right - (window.innerWidth - 8)
  if (overflow > 0) pos.value = { ...pos.value, left: Math.max(8, pos.value.left - overflow) }
}

function show() {
  window.clearTimeout(timer)
  timer = window.setTimeout(() => {
    place()
    open.value = true
    void nextTick(clampToWindow)
  }, props.delay)
}

function hide() {
  window.clearTimeout(timer)
  open.value = false
}

onBeforeUnmount(() => window.clearTimeout(timer))
</script>

<template>
  <span
    ref="anchor"
    class="relative inline-flex"
    @mouseenter="show"
    @mouseleave="hide"
    @focusin="show"
    @focusout="hide"
  >
    <slot />
  </span>

  <Teleport to="body">
    <Transition
      enter-active-class="transition duration-100"
      enter-from-class="opacity-0"
      leave-active-class="transition duration-75"
      leave-to-class="opacity-0"
    >
      <div
        v-if="open"
        ref="bubble"
        role="tooltip"
        class="pointer-events-none fixed z-50 w-max max-w-[240px] rounded-md border border-line-strong bg-overlay px-2 py-1.5 text-[11px] leading-snug text-ink shadow-md shadow-black/15"
        :style="{
          left: `${pos.left}px`,
          top: `${pos.top}px`,
          transform: `translate(${pos.fromRight ? '-100%' : '0'}, ${side === 'top' ? '-100%' : '0'})`,
        }"
      >
        {{ content }}
      </div>
    </Transition>
  </Teleport>
</template>
