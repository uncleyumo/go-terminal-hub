<script setup lang="ts">
import UIcon from './UIcon.vue'

// 四档外观 × 两档尺寸，全部靠 class 拼。
// 之所以不做成配置文件（Tailwind 那样）：就这几个组合，写死比抽象好读。
const VARIANT: Record<string, string> = {
  // 主操作：整个界面同一时刻只应该有一个。
  // 用 accent-solid 不是 accent：白字压 accent 只有 3.20:1，达不到 4.5:1（见 style.css）
  primary:
    'bg-accent-solid text-accent-ink hover:bg-accent-solid-hover active:bg-accent-solid disabled:hover:bg-accent-solid',
  // 次操作：默认动作
  default:
    'bg-raised text-ink border border-line-strong hover:border-ink-faint hover:bg-line active:bg-line-strong disabled:hover:bg-raised',
  // 工具栏/列表里那种次要动作，平时不抢眼
  ghost: 'text-ink-dim hover:bg-raised hover:text-ink active:bg-line disabled:hover:bg-transparent',
  // 破坏性动作的两种形态：danger 是平时的图标按钮，dangerSolid 是确认框里的主按钮
  danger: 'text-neg hover:bg-neg/12 hover:text-neg active:bg-neg/20',
  dangerSolid: 'bg-neg text-white hover:brightness-110 active:brightness-95',
}

const SIZE: Record<string, string> = {
  sm: 'h-7 px-2.5 text-xs gap-1.5 rounded-md',
  md: 'h-8.5 px-3.5 text-[13px] gap-2 rounded-lg',
}

withDefaults(
  defineProps<{
    variant?: 'primary' | 'default' | 'ghost' | 'danger' | 'dangerSolid'
    size?: 'sm' | 'md'
    /** 只放图标，不放文字。给了它就必须给 icon */
    square?: boolean
    icon?: string
    disabled?: boolean
    /** 图标转起来。给「刷新」这种点下去要等一会儿才有反应的动作一个可见的反馈 */
    spin?: boolean
    type?: 'button' | 'submit'
  }>(),
  { variant: 'default', size: 'md', square: false, disabled: false, spin: false, type: 'button' },
)
</script>

<template>
  <button
    :type="type"
    :disabled="disabled"
    class="inline-flex items-center justify-center font-medium whitespace-nowrap transition-colors duration-100 select-none"
    :class="[
      VARIANT[variant],
      SIZE[size],
      square && (size === 'sm' ? 'w-7 px-0' : 'w-8.5 px-0'),
      disabled && 'opacity-40 pointer-events-none',
    ]"
  >
    <UIcon
      v-if="icon"
      :name="icon"
      :size="size === 'sm' ? 14 : 15"
      :class="spin && 'animate-spin'"
    />
    <slot />
  </button>
</template>
