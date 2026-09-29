<script setup lang="ts">
// 图标按钮 + 弹出菜单。语言和主题共用这一个 —— 它们是同一类控件
// （「从几个固定值里选一个，当前值用图标表示」），做成两个组件只会长得不一样。
// 菜单 Teleport 到 body：顶栏贴着窗口顶端，fixed 定位不受任何 overflow 祖先裁剪。
import { onBeforeUnmount, onMounted, ref, useTemplateRef } from 'vue'
import UIcon from './UIcon.vue'
import UTooltip from './UTooltip.vue'

export interface MenuOption {
  value: string
  label: string
  /** 选中项在触发按钮上显示的图标 */
  icon: string
}

const props = defineProps<{
  modelValue: string
  options: MenuOption[]
  /** 无障碍名，也是 tooltip 文案 */
  label: string
}>()

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const trigger = useTemplateRef<HTMLElement>('trigger')
// 菜单本体 Teleport 到 body，不在 trigger 里面 —— 点菜单项时必须单独认它，
// 否则 pointerdown 会当成「点了外面」把菜单关掉，click 落在已移除的元素上，choose() 不跑。
const panel = useTemplateRef<HTMLElement>('panel')
const open = ref(false)
// 位置只在打开那一刻量一次：这个界面里触发器不会移动
const pos = ref({ left: 0, top: 0 })

const current = () => props.options.find((o) => o.value === props.modelValue) ?? props.options[0]

function toggle() {
  if (open.value) {
    close()
    return
  }
  const r = trigger.value?.getBoundingClientRect()
  if (r) {
    // 按触发器的**右**边缘往左展开：语言和主题两个按钮都贴着窗口右边缘，
    // 按左边缘定位的话 176px 宽的菜单有一半在窗口外面（学习者 2026-09-28 指出过）。
    // 最后再夹一次窗口宽度，窗口拉窄也不会被裁掉。
    const w = 176
    const aligned = Math.round(r.right - w)
    pos.value = {
      left: Math.max(8, Math.min(aligned, window.innerWidth - w - 8)),
      top: Math.round(r.bottom) + 6,
    }
  }
  open.value = true
}

function close() {
  open.value = false
}

function choose(value: string) {
  emit('update:modelValue', value)
  close()
}

function onDocPointer(e: PointerEvent) {
  if (!open.value) return
  const target = e.target as Node
  // trigger 和 panel 都不算「点到外面」：
  // ① panel 不排掉的话，点菜单项会被 pointerdown 抢先关掉（菜单带 leave 动画，
  //    元素约 90ms 后才真正移除；手按得慢一点，click 就打在一个已经不在的节点上，
  //    choose() 不跑 —— 表现是「点语言/主题不生效，偶尔成一次」）。
  // ② trigger 不排掉的话，菜单开着时点图标按钮，pointerdown 先关、click 又开，关不掉。
  if (trigger.value?.contains(target) || panel.value?.contains(target)) return
  close()
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') close()
}

onMounted(() => {
  document.addEventListener('pointerdown', onDocPointer)
  window.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocPointer)
  window.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div ref="trigger" class="relative">
    <!-- 提示走 UTooltip 而不是原生 title：原生那个由系统画，颜色跟着系统主题走，
         跟同主题下自绘的气泡对不上（见 UTooltip 的注释） -->
    <UTooltip :content="props.label">
      <button
        type="button"
        class="flex h-7 w-7 items-center justify-center rounded-md text-ink-dim transition-colors duration-100 hover:bg-raised hover:text-ink"
        :aria-label="props.label"
        :aria-expanded="open"
        aria-haspopup="menu"
        @click="toggle"
      >
        <UIcon :name="current().icon" :size="15" />
      </button>
    </UTooltip>

    <Teleport to="body">
      <Transition
        enter-active-class="transition duration-100 ease-out"
        enter-from-class="opacity-0"
        leave-active-class="transition duration-75 ease-in"
        leave-to-class="opacity-0"
      >
        <div
          v-if="open"
          ref="panel"
          role="menu"
          class="fixed z-50 w-[176px] overflow-hidden rounded-lg border border-line-strong bg-overlay p-1 shadow-lg shadow-black/15"
          :style="{ left: `${pos.left}px`, top: `${pos.top}px` }"
        >
          <button
            v-for="opt in options"
            :key="opt.value"
            type="button"
            role="menuitemradio"
            :aria-checked="opt.value === props.modelValue"
            class="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left text-xs transition-colors duration-100"
            :class="
              opt.value === props.modelValue
                ? 'bg-accent-soft text-ink'
                : 'text-ink-dim hover:bg-raised hover:text-ink'
            "
            @click="choose(opt.value)"
          >
            <UIcon :name="opt.icon" :size="14" />
            <span class="flex-1">{{ opt.label }}</span>
            <UIcon
              v-if="opt.value === props.modelValue"
              name="check"
              :size="13"
              class="text-accent"
            />
          </button>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>
