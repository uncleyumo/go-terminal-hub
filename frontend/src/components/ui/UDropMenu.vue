<script setup lang="ts">
// 图标按钮 + 弹出菜单，**点一项做一件事**。
//
// 跟 UIconMenu 的区别：那个是「从固定几个值里选一个」，选中项在按钮上显示图标、
// 菜单里带对勾；这个是「点一下就跑一个动作」，没有当前值、没有对勾，
// 而且要能置灰（D47 的菜单 1、2 对 `shell` / `terminal-shell` 就是灰的）。
// 两种形状差得够多，硬塞进一个组件只会两边都别扭，所以分开。
//
// 菜单 Teleport 到 body，fixed 定位不受任何 overflow 祖先裁剪。
import { onBeforeUnmount, onMounted, ref, useTemplateRef } from 'vue'
import UIcon from './UIcon.vue'
import UTooltip from './UTooltip.vue'

export interface DropMenuItem {
  key: string
  label: string
  icon?: string
  /** 灰掉：不响应点击，也不自动关菜单（点了得让人知道为什么没反应） */
  disabled?: boolean
  /** 这一项上面画一条分隔线 */
  separator?: boolean
}

const props = withDefaults(
  defineProps<{
    items: DropMenuItem[]
    /** 触发按钮的图标名 */
    icon: string
    /** 无障碍名，也是 tooltip 文案 */
    label: string
    width?: number
    align?: 'left' | 'right'
  }>(),
  { width: 224, align: 'left' },
)

const emit = defineEmits<{ select: [key: string] }>()

const trigger = useTemplateRef<HTMLElement>('trigger')
// 菜单本体 Teleport 到 body，不在 trigger 里面 —— 点菜单项时必须单独认它，
// 否则 pointerdown 会当成「点了外面」把菜单关掉，click 落在已移除的元素上，select 不跑。
// 这是 UIconMenu 上修过的那个坑（PROGRESS.md D46 上方那条），那边怎么写的这边就怎么写。
const panel = useTemplateRef<HTMLElement>('panel')
const open = ref(false)
// 位置只在打开那一刻量一次：这个界面里触发器不会移动
const pos = ref({ left: 0, top: 0 })

function toggle() {
  if (open.value) {
    close()
    return
  }
  const r = trigger.value?.getBoundingClientRect()
  if (r) {
    // 贴窗口右边缘的按钮按左边缘展开会被裁掉一半（UIconMenu 里记着同一个坑）。
    // 两头都夹一次：菜单再宽，窗口再窄，也不会有一部分跑到外面看不见。
    const aligned = props.align === 'right' ? r.right - props.width : r.left
    pos.value = {
      left: Math.max(8, Math.min(aligned, window.innerWidth - props.width - 8)),
      top: Math.round(r.bottom) + 6,
    }
  }
  open.value = true
}

function close() {
  open.value = false
}

function choose(item: DropMenuItem) {
  if (item.disabled) return
  emit('select', item.key)
  close()
}

function onDocPointer(e: PointerEvent) {
  if (!open.value) return
  const target = e.target as Node
  // trigger 和 panel 都不算「点到外面」——理由同 UIconMenu 的注释。
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
    <UTooltip :content="props.label">
      <button
        type="button"
        class="flex h-7 w-7 items-center justify-center rounded-md text-ink-dim transition-colors duration-100 hover:bg-raised hover:text-ink"
        :aria-label="props.label"
        :aria-expanded="open"
        aria-haspopup="menu"
        @click="toggle"
      >
        <UIcon :name="props.icon" :size="15" />
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
          class="fixed z-50 overflow-hidden rounded-lg border border-line-strong bg-overlay p-1 shadow-lg shadow-black/15"
          :style="{ left: `${pos.left}px`, top: `${pos.top}px`, width: `${props.width}px` }"
        >
          <template v-for="item in items" :key="item.key">
            <div v-if="item.separator" class="my-1 h-px bg-line" />
            <button
              type="button"
              role="menuitem"
              :aria-disabled="item.disabled"
              class="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left text-xs transition-colors duration-100"
              :class="
                item.disabled
                  ? 'cursor-not-allowed text-ink-faint opacity-60'
                  : 'text-ink-dim hover:bg-raised hover:text-ink'
              "
              @click="choose(item)"
            >
              <UIcon v-if="item.icon" :name="item.icon" :size="14" />
              <span class="flex-1 truncate">{{ item.label }}</span>
            </button>
          </template>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>
