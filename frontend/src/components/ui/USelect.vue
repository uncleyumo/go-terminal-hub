<script setup lang="ts">
import UIcon from './UIcon.vue'

// 用原生 <select>：选项少、下拉是系统画的（浅色/深色会跟 color-scheme 走），
// 自己画下拉面板要处理滚动、贴边翻转、系统缩放，收益和成本不成比例。
// 原生控件的箭头和 padding 在各平台不一样，一律 appearance-none 掉自己补。
defineProps<{
  modelValue: string
  options: Array<{ label: string; value: string }>
  disabled?: boolean
}>()

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <div class="relative inline-flex items-center">
    <select
      :value="modelValue"
      :disabled="disabled"
      class="h-7 w-full appearance-none rounded-md border border-line-strong bg-raised pr-7 pl-2.5 text-xs text-ink outline-none transition-colors duration-100 hover:border-ink-faint focus:border-accent disabled:opacity-40"
      @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value)"
    >
      <option v-for="opt in options" :key="opt.value" :value="opt.value" class="bg-overlay">
        {{ opt.label }}
      </option>
    </select>
    <UIcon
      name="chevronDown"
      :size="13"
      class="pointer-events-none absolute right-2 text-ink-faint"
    />
  </div>
</template>
