<script setup lang="ts">
// 顶栏的标签条：会话栏收起时，把「运行中的会话 + 当前这条」排成一排。
//
// 当前这条**不挪到最左**：点一下标签，它原地高亮就好。挪位置的话，
// 鼠标还停在原处，底下已经换成别的灰标签了，看着像没点中，于是再点一次。
// 想调顺序自己拖 —— 顺序只在这次运行里有效，不落盘：每次开机会话的
// 启动顺序本来就会变，记下来的顺序第二天对不上号。
import { nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import UIcon from './ui/UIcon.vue'

export interface TabItem {
  id: string
  name: string
  running: boolean
}

const { t } = useI18n()
const props = defineProps<{ tabs: TabItem[]; currentId: string | null }>()
const emit = defineEmits<{ select: [id: string]; reorder: [ids: string[]] }>()

const root = ref<HTMLElement | null>(null)
const draggingId = ref<string | null>(null)
const menuOpen = ref(false)

// —— 拖动换位 ——

let press: { id: string; x: number; moved: boolean } | null = null

function onPointerDown(e: PointerEvent, id: string) {
  if (e.button !== 0) return
  press = { id, x: e.clientX, moved: false }
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}

function onPointerMove(e: PointerEvent) {
  if (!press) return
  if (!press.moved) {
    // 手抖几个像素不算拖：点标签是最常做的动作，别被它抢走
    if (Math.abs(e.clientX - press.x) < 5) return
    press.moved = true
    draggingId.value = press.id
  }
  const items = tabEls()
  const from = items.findIndex((el) => el.dataset.sessionTab === press!.id)
  if (from < 0) return
  if (e.clientX > items[from].getBoundingClientRect().right - 8) {
    if (from < items.length - 1) void move(press.id, from + 1)
    return
  }
  if (e.clientX < items[from].getBoundingClientRect().left + 8) {
    if (from > 0) void move(press.id, from - 1)
  }
}

function onPointerUp(id: string) {
  const moved = press?.moved ?? false
  press = null
  draggingId.value = null
  if (moved) return
  // 没拖动就是「选中」—— 不用 @click，否则一次按下会同时发 pointerup 和 click
  emit('select', id)
}

function onPointerCancel() {
  press = null
  draggingId.value = null
}

function tabEls(): HTMLElement[] {
  return Array.from(root.value?.querySelectorAll<HTMLElement>('[data-session-tab]') ?? [])
}

/** 换位时其余标签滑过去，而不是瞬间跳 —— 不然看着像整排闪了一下。 */
async function move(id: string, to: number) {
  const ids = props.tabs.map((t) => t.id)
  const from = ids.indexOf(id)
  if (from < 0 || to < 0 || to >= ids.length || to === from) return

  const before: Record<string, number> = {}
  for (const el of tabEls()) {
    if (el.dataset.sessionTab) before[el.dataset.sessionTab] = el.getBoundingClientRect().left
  }

  const next = ids.slice()
  next.splice(from, 1)
  next.splice(to, 0, id)
  emit('reorder', next)

  await nextTick()
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
  for (const el of tabEls()) {
    const id2 = el.dataset.sessionTab
    if (!id2 || before[id2] === undefined) continue
    const dx = before[id2] - el.getBoundingClientRect().left
    if (!dx) continue
    el.animate([{ transform: `translateX(${dx}px)` }, { transform: 'none' }], {
      duration: 160,
      easing: 'cubic-bezier(.2,.8,.3,1)',
    })
  }
}

// —— 装不下时的那张名单 ——

function onDocPointer(e: PointerEvent) {
  if (!menuOpen.value) return
  if (!(e.target as HTMLElement).closest('.tabs-menu-wrap')) menuOpen.value = false
}
document.addEventListener('pointerdown', onDocPointer)
onBeforeUnmount(() => document.removeEventListener('pointerdown', onDocPointer))

function pick(id: string) {
  menuOpen.value = false
  emit('select', id)
}

// 滚轮横滚：标签条没有纵向的东西可滚，把 deltaY 借过来最顺手
function onWheel(e: WheelEvent) {
  if (!root.value) return
  root.value.scrollLeft += e.deltaY
}
</script>

<template>
  <div class="flex min-w-0 flex-1 items-center gap-1">
    <div
      ref="root"
      class="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      role="tablist"
      @wheel="onWheel"
    >
      <button
        v-for="tab in tabs"
        :key="tab.id"
        type="button"
        role="tab"
        :aria-selected="tab.id === currentId"
        :title="tab.name"
        :data-session-tab="tab.id"
        class="group flex h-7 max-w-[200px] min-w-[96px] flex-none items-center gap-1.5 overflow-hidden rounded-md border px-2.5 text-[12.5px] whitespace-nowrap transition-colors duration-100 select-none"
        :class="[
          tab.id === currentId
            ? 'border-line-strong bg-raised text-ink'
            : 'border-transparent text-ink-dim hover:bg-raised/60 hover:text-ink',
          draggingId === tab.id && 'opacity-60',
        ]"
        @pointerdown="onPointerDown($event, tab.id)"
        @pointermove="onPointerMove"
        @pointerup="onPointerUp(tab.id)"
        @pointercancel="onPointerCancel"
        @keydown.enter.prevent="emit('select', tab.id)"
        @keydown.space.prevent="emit('select', tab.id)"
      >
        <span
          class="h-1.5 w-1.5 flex-none rounded-full"
          :class="tab.running ? 'bg-pos' : 'bg-line-strong'"
        ></span>
        <span class="min-w-0 flex-1 truncate text-left">{{ tab.name }}</span>
      </button>
    </div>

    <div class="tabs-menu-wrap relative flex-none">
      <button
        type="button"
        class="flex h-7 w-6 items-center justify-center rounded-md text-ink-faint transition-colors hover:bg-raised hover:text-ink"
        :aria-label="t('tabs.all')"
        :title="t('tabs.all')"
        @click="menuOpen = !menuOpen"
      >
        <UIcon name="chevronDown" :size="13" />
      </button>
      <div
        v-if="menuOpen"
        class="absolute top-[calc(100%+6px)] right-0 z-30 flex max-h-72 w-56 flex-col overflow-y-auto rounded-lg border border-line-strong bg-overlay p-1 shadow-2xl shadow-black/50"
      >
        <button
          v-for="tab in tabs"
          :key="tab.id"
          type="button"
          class="flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12.5px] whitespace-nowrap text-ink hover:bg-raised"
          :class="tab.id === currentId && 'text-accent'"
          @click="pick(tab.id)"
        >
          <span
            class="h-1.5 w-1.5 flex-none rounded-full"
            :class="tab.running ? 'bg-pos' : 'bg-line-strong'"
          ></span>
          <span class="min-w-0 flex-1 truncate">{{ tab.name }}</span>
        </button>
      </div>
    </div>
  </div>
</template>
