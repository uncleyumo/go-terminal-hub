<script setup lang="ts">
withDefaults(
  defineProps<{
    modelValue: string
    placeholder?: string
    rows?: number
    invalid?: boolean
    disabled?: boolean
    mono?: boolean
  }>(),
  { rows: 3, invalid: false, disabled: false, mono: false },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <textarea
    :value="modelValue"
    :placeholder="placeholder"
    :rows="rows"
    :disabled="disabled"
    spellcheck="false"
    class="w-full resize-none rounded-lg border bg-sunken px-3 py-2 text-[13px] leading-relaxed text-ink outline-none transition-colors duration-100 placeholder:text-ink-faint focus:border-accent disabled:opacity-50"
    :class="[invalid ? 'border-neg' : 'border-line-strong', mono && 'font-mono text-xs']"
    @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
  />
</template>
