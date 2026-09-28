<script setup lang="ts">
import UIcon from './UIcon.vue'

// 数字输入（列/行）。左右各一个步进按钮，中间是可编辑的数字。
// 不用原生 number 控件：它的上下箭头在深色底上要单独隐藏，而且步进粒度固定。
const props = withDefaults(
  defineProps<{
    modelValue: number
    min?: number
    max?: number
    step?: number
    disabled?: boolean
    /** 外面那行文字只是 <label>，没有 for，所以读屏够不着 —— 名字从这里给 */
    ariaLabel?: string
  }>(),
  { min: 1, max: 1000, step: 1, disabled: false, ariaLabel: '' },
)

const emit = defineEmits<{ 'update:modelValue': [value: number] }>()

function clamp(n: number): number {
  return Math.min(props.max, Math.max(props.min, n))
}

function bump(delta: number) {
  emit('update:modelValue', clamp(props.modelValue + delta * props.step))
}

function onInput(raw: string) {
  const n = Number(raw)
  // 输入框被清空的那一瞬间 Number('') 是 0，直接夹到下限会把光标位置冲掉。
  // 先放过，等失焦再夹。
  if (Number.isNaN(n)) return
  emit('update:modelValue', clamp(n))
}
</script>

<template>
  <div
    class="inline-flex h-7 items-stretch overflow-hidden rounded-md border border-line-strong bg-sunken transition-colors duration-100 focus-within:border-accent"
    :class="disabled && 'opacity-40'"
  >
    <button
      type="button"
      class="flex w-7 items-center justify-center text-ink-faint transition-colors hover:bg-raised hover:text-ink disabled:pointer-events-none"
      :disabled="disabled || modelValue <= min"
      @click="bump(-1)"
    >
      <UIcon name="minus" :size="12" />
    </button>
    <input
      :value="modelValue"
      type="number"
      :min="min"
      :max="max"
      :disabled="disabled"
      :aria-label="props.ariaLabel"
      class="w-12 border-x border-line bg-transparent text-center text-xs text-ink outline-none [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none"
      @input="onInput(($event.target as HTMLInputElement).value)"
    />
    <button
      type="button"
      class="flex w-7 items-center justify-center text-ink-faint transition-colors hover:bg-raised hover:text-ink disabled:pointer-events-none"
      :disabled="disabled || modelValue >= max"
      @click="bump(1)"
    >
      <UIcon name="plus" :size="12" />
    </button>
  </div>
</template>
