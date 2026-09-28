<script setup lang="ts">
import UIcon from './UIcon.vue'
import { dismiss, toasts, type ToastKind } from './toast'

const ICON: Record<ToastKind, string> = {
  success: 'checkCircle',
  error: 'warn',
  info: 'info',
}

const TONE: Record<ToastKind, string> = {
  success: 'text-pos',
  error: 'text-neg',
  info: 'text-ink-dim',
}
</script>

<template>
  <Teleport to="body">
    <div class="pointer-events-none fixed right-4 bottom-4 z-50 flex flex-col items-end gap-2">
      <TransitionGroup
        enter-active-class="transition duration-200 ease-out"
        enter-from-class="opacity-0 translate-x-4"
        leave-active-class="transition duration-150 ease-in absolute"
        leave-to-class="opacity-0 translate-x-4"
        move-class="transition duration-200"
      >
        <div
          v-for="item in toasts"
          :key="item.id"
          role="status"
          class="pointer-events-auto flex max-w-[380px] items-start gap-2.5 rounded-lg border border-line-strong bg-overlay py-2.5 pr-2.5 pl-3 shadow-xl shadow-black/30"
        >
          <UIcon :name="ICON[item.kind]" :size="15" class="mt-px" :class="TONE[item.kind]" />
          <span data-selectable class="flex-1 text-xs leading-relaxed break-words text-ink">
            {{ item.text }}
          </span>
          <button
            type="button"
            class="flex h-5 w-5 shrink-0 items-center justify-center rounded text-ink-faint transition-colors hover:bg-raised hover:text-ink"
            @click="dismiss(item.id)"
          >
            <UIcon name="x" :size="12" />
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>
