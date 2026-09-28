<script setup lang="ts">
import { onBeforeUnmount, watch } from 'vue'
import UButton from './UButton.vue'
import UIcon from './UIcon.vue'
import { confirmState, resolveConfirm } from './confirm'

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') resolveConfirm(false)
  if (e.key === 'Enter') resolveConfirm(true)
}

watch(
  () => confirmState.open,
  (open) => {
    if (open) window.addEventListener('keydown', onKeydown)
    else window.removeEventListener('keydown', onKeydown)
  },
  { immediate: true },
)

// 组件整个被拆掉时（应用关窗），别让还挂着的 await 永远等下去 —— 按「取消」结掉。
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  if (confirmState.open) resolveConfirm(false)
})
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
        v-if="confirmState.open"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-6 backdrop-blur-[2px]"
        @mousedown.self="resolveConfirm(false)"
      >
        <div
          role="alertdialog"
          aria-modal="true"
          class="w-full max-w-[420px] overflow-hidden rounded-xl border border-line-strong bg-surface shadow-2xl shadow-black/40"
        >
          <div class="flex gap-3 px-5 pt-5 pb-4">
            <div
              class="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-full"
              :class="
                confirmState.tone === 'danger' ? 'bg-neg/15 text-neg' : 'bg-accent-soft text-accent'
              "
            >
              <UIcon :name="confirmState.tone === 'danger' ? 'warn' : 'info'" :size="15" />
            </div>
            <div class="min-w-0 flex-1">
              <div class="text-[13px] font-semibold text-ink">{{ confirmState.title }}</div>
              <p data-selectable class="mt-1.5 text-xs leading-relaxed text-ink-dim">
                {{ confirmState.message }}
              </p>
            </div>
          </div>

          <div class="flex justify-end gap-2 border-t border-line bg-sunken/60 px-5 py-3">
            <UButton variant="default" size="sm" @click="resolveConfirm(false)">
              {{ confirmState.cancelText }}
            </UButton>
            <UButton
              :variant="confirmState.tone === 'danger' ? 'dangerSolid' : 'primary'"
              size="sm"
              @click="resolveConfirm(true)"
            >
              {{ confirmState.confirmText }}
            </UButton>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
