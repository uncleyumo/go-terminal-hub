<script setup lang="ts">
// 单行输入。用原生 <input>，样式自己画 —— 组件库那套输入框的毛病是内边距和行高
// 都比这套密度高，混在 13px 的界面里会明显鼓出来。
withDefaults(
  defineProps<{
    modelValue: string
    placeholder?: string
    type?: string
    invalid?: boolean
    disabled?: boolean
    /** 输入框右边塞一个按钮（浏览…那类） */
    mono?: boolean
  }>(),
  { type: 'text', invalid: false, disabled: false, mono: false },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
  /** 用户自己敲了。跟 update:modelValue 分开发：v-model 那条是「值变了」，
   *  这条是「人动了手」—— 名字那一栏靠它区分自动填的和手打的。 */
  input: []
  enter: []
}>()
</script>

<template>
  <div
    class="flex items-stretch rounded-lg border bg-sunken transition-colors duration-100 focus-within:border-accent"
    :class="invalid ? 'border-neg' : 'border-line-strong'"
  >
    <input
      :type="type"
      :value="modelValue"
      :placeholder="placeholder"
      :disabled="disabled"
      spellcheck="false"
      autocomplete="off"
      class="min-w-0 flex-1 bg-transparent px-3 py-2 text-[13px] text-ink outline-none placeholder:text-ink-faint disabled:opacity-50"
      :class="mono && 'font-mono text-xs'"
      @input="
        emit('update:modelValue', ($event.target as HTMLInputElement).value);
        emit('input')
      "
      @keydown.enter="emit('enter')"
    />
    <!-- 附加区不跟输入框抢焦点：里面的按钮自己管。
         flex-none 不能省：输入框那边是 min-w-0 flex-1，可以一路缩到 0，
         而这个容器默认能缩（flex-shrink:1）——宽度紧张时先被压扁的是它，
         里面的按钮跟着被挤，文字截成「浏览…」。 -->
    <div v-if="$slots.suffix" class="flex flex-none items-center border-l border-line pl-1.5 pr-1.5">
      <slot name="suffix" />
    </div>
  </div>
</template>
