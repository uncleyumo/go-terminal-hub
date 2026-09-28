<script setup lang="ts">
// 开关。用 button + role="switch" 而不是 checkbox —— 原生 checkbox 在深色底上
// 要靠 appearance-none 整个重画，还要另外补一个"已勾选"的状态源。
const props = defineProps<{ modelValue: boolean; disabled?: boolean }>()

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
</script>

<template>
  <button
    type="button"
    role="switch"
    :aria-checked="props.modelValue"
    :disabled="disabled"
    class="relative h-[18px] w-8 shrink-0 rounded-full transition-colors duration-150 disabled:opacity-40"
    :class="props.modelValue ? 'bg-accent-solid' : 'bg-line-strong'"
    @click="emit('update:modelValue', !props.modelValue)"
  >
    <span
      class="absolute top-[2px] left-[2px] h-3.5 w-3.5 rounded-full bg-white shadow-sm transition-transform duration-150"
      :class="props.modelValue && 'translate-x-3.5'"
    />
  </button>
</template>
