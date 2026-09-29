<script setup lang="ts">
// 分段选择器：一排互斥选项，选中项用一块实底托住。
// 整组用同一个容器包起来（而不是每个选项各自带边框），
// 否则选项之间那道缝在深色底上会变成一条条黑线。
defineProps<{
  modelValue: string
  /** disabled 的那一项显示出来但点不动 —— 用在「还没做出来的选项」上 */
  options: Array<{ label: string; value: string; disabled?: boolean }>
}>()

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <!-- flex-wrap：选项排不下时换行，不换行的话整组会撑出容器 ——
       溢出去的部分在弹框里会变成一条横向滚动条，最右边那个选项（比如
       terminal(powershell)）直接被切掉看不见。 -->
  <div
    class="inline-flex flex-wrap items-center gap-0.5 rounded-lg border border-line-strong bg-sunken p-0.5"
  >
    <button
      v-for="opt in options"
      :key="opt.value"
      type="button"
      :disabled="opt.disabled"
      :aria-pressed="opt.value === modelValue"
      class="rounded-[5px] px-2.5 py-1 text-xs font-medium whitespace-nowrap transition-colors duration-100"
      :class="
        opt.disabled
          ? 'cursor-not-allowed text-ink-faint'
          : opt.value === modelValue
            ? 'bg-accent-solid text-accent-ink'
            : 'text-ink-dim hover:bg-raised hover:text-ink'
      "
      @click="emit('update:modelValue', opt.value)"
    >
      {{ opt.label }}
    </button>
  </div>
</template>
