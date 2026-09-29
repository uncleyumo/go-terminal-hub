<script setup lang="ts">
import { onBeforeUnmount, watch } from 'vue'
import UIcon from './UIcon.vue'

const props = withDefaults(
  defineProps<{
    modelValue: boolean
    title: string
    /** 内容区最大高度。弹框整体不超出视口，超出的部分归内容区滚 */
    bodyMaxHeight?: string
    /** 弹框最大宽度。写成一个 prop 而不是写死在样式里：
     *  560 是当初按「确认框只有一句话」定的，会话表单那八种 kind 排不下，
     *  只能让调用方自己说要多宽。 */
    width?: string
  }>(),
  { bodyMaxHeight: '62vh', width: '560px' },
)

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

function close() {
  emit('update:modelValue', false)
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') close()
}

// 只在打开期间挂监听：弹框关着的时候，Esc 不该被它吃掉。
watch(
  () => props.modelValue,
  (open) => {
    if (open) window.addEventListener('keydown', onKeydown)
    else window.removeEventListener('keydown', onKeydown)
  },
  { immediate: true },
)

onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition duration-150 ease-out"
      enter-from-class="opacity-0"
      leave-active-class="transition duration-100 ease-in"
      leave-to-class="opacity-0"
    >
      <div
        v-if="modelValue"
        class="fixed inset-0 z-40 flex items-center justify-center bg-black/50 p-6 backdrop-blur-[2px]"
        @mousedown.self="close"
      >
        <Transition
          appear
          enter-active-class="transition duration-150 ease-out"
          enter-from-class="opacity-0 scale-95 translate-y-1"
        >
          <div
            role="dialog"
            aria-modal="true"
            :aria-label="title"
            class="flex max-h-full w-full flex-col overflow-hidden rounded-xl border border-line-strong bg-surface shadow-2xl shadow-black/40"
            :style="{ maxWidth: width }"
          >
            <div class="flex flex-none items-center gap-2 border-b border-line px-5 py-3.5">
              <span class="flex-1 text-[13px] font-semibold text-ink">{{ title }}</span>
              <button
                type="button"
                class="flex h-6 w-6 items-center justify-center rounded-md text-ink-faint transition-colors hover:bg-raised hover:text-ink"
                @click="close"
              >
                <UIcon name="x" :size="15" />
              </button>
            </div>

            <div
              class="min-h-0 flex-1 overflow-y-auto px-5 py-4"
              :style="{ maxHeight: bodyMaxHeight }"
            >
              <slot />
            </div>

            <div
              v-if="$slots.footer"
              class="flex flex-none items-center justify-end gap-2 border-t border-line bg-sunken/60 px-5 py-3"
            >
              <slot name="footer" />
            </div>
          </div>
        </Transition>
      </div>
    </Transition>
  </Teleport>
</template>
