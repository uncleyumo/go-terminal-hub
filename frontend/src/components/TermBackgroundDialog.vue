<script setup lang="ts">
// 每会话终端背景色（D48，方案 C）。
//
// 用户只挑**一个颜色**——背景。文字色、光标、选中底色、ANSI 十六色该配哪套，
// 全由程序按这个背景的亮暗算出来（`terminal/background.ts`）。
// 界面上不给「前景色」「配色方案」这些选项：能不给的选项就不给，
// 多给一个就多一份「我是不是该调这个」的心智负担。
//
// 拖方块 / 拖色相条 / 改 R G B / 敲 hex / 点系统取色器，改的都是**同一个值**。
// 所以内部只存一份 HSV，RGB 和 hex 每次由它算出来，反过来从 RGB / hex 进来就转成 HSV。
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import UButton from './ui/UButton.vue'
import UDialog from './ui/UDialog.vue'
import UIcon from './ui/UIcon.vue'
import {
  hexToRgb,
  hsvToRgb,
  normalizeHex,
  prefersDarkText,
  rgbToHsv,
} from '../terminal/background'

const props = withDefaults(
  defineProps<{
    modelValue: boolean
    /** 打开时这个值是什么。null = 这一层没设过，往下看 fallback */
    initial: string | null
    /**
     * 上面那层没设过时，色板打开在哪个颜色上。
     * 会话级传「全局默认底色」（全局也没设就是主题自带的），
     * 全局级传主题自带的。改的是当前这个值，不是别的层。
     */
    fallback?: string | null
    title?: string
  }>(),
  { fallback: null, title: '' },
)

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 任何一个输入变了就发一次，终端那边立刻跟着变 —— 边拖边看效果 */
  change: [hex: string | null]
}>()

const { t } = useI18n()

// —— 当前值 ——
// 一律存 hex（六位小写），HSV 和 RGB 都由它派生。
// 存 hex 而不是存 HSV：localStorage 里那一份是人可能去看的，
// 十六进制是唯一一种看一眼就知道是什么的写法。
// （HSV ↔ RGB 的换算在 terminal/background.ts 里，跟亮暗判断放在一处。）
function rgbToHex(r: number, g: number, b: number): string {
  const part = (n: number) =>
    Math.max(0, Math.min(255, Math.round(n))).toString(16).padStart(2, '0')
  return `#${part(r)}${part(g)}${part(b)}`
}

// —— 当前值 ——
// 一律存 hex（六位小写），HSV 和 RGB 都由它派生。
// 存 hex 而不是存 HSV：localStorage 里那一份是人可能去看的，
// 十六进制是唯一一种看一眼就知道是什么的写法。
const hex = ref('#0e1117')
const hsv = ref({ h: 220, s: 20, v: 8 })

function setFromHex(next: string, notify = true) {
  const value = normalizeHex(next)
  if (!value) return
  hex.value = value
  const { r, g, b } = hexToRgb(value)
  hsv.value = rgbToHsv(r, g, b)
  if (notify) emit('change', value)
}

function setFromHsv(next: { h: number; s: number; v: number }) {
  const h = Math.max(0, Math.min(360, next.h))
  const s = Math.max(0, Math.min(100, next.s))
  const v = Math.max(0, Math.min(100, next.v))
  hsv.value = { h, s, v }
  const { r, g, b } = hsvToRgb(h, s, v)
  hex.value = rgbToHex(r, g, b)
  emit('change', hex.value)
}

// 每次打开都回到打开时的样子：上次调了一半关掉，不该留在这儿
watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    setFromHex(props.initial ?? props.fallback ?? currentAppBackground(), false)
  },
  { immediate: true },
)

/** 主题自带的底色。上面两层都没设过时，色板开在这个颜色上，不是随便一个深色 */
function currentAppBackground(): string {
  const light = document.documentElement.dataset.theme === 'light'
  return light ? '#edeff3' : '#07090d'
}

const usesCustom = computed(() => props.initial !== null)

// —— 拖动 ——
// 两条都用 pointer 事件而不是 mousedown/mousemove：鼠标移出元素、或者拖到元素外，
// 事件也得继续跟着走（`setPointerCapture` 就是干这个的）。
const square = ref<HTMLElement | null>(null)

function pickFromSquare(e: PointerEvent) {
  const el = square.value
  if (!el) return
  const r = el.getBoundingClientRect()
  const x = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width))
  const y = Math.max(0, Math.min(1, (e.clientY - r.top) / r.height))
  // 横向 = 饱和度，纵向 = 明度（跟 Photoshop / 系统取色器一个方向）
  setFromHsv({ h: hsv.value.h, s: x * 100, v: (1 - y) * 100 })
}

function onSquareDown(e: PointerEvent) {
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
  pickFromSquare(e)
}

function onSquareMove(e: PointerEvent) {
  if ((e.currentTarget as HTMLElement).hasPointerCapture(e.pointerId)) pickFromSquare(e)
}

const hue = ref<HTMLElement | null>(null)

function pickFromHue(e: PointerEvent) {
  const el = hue.value
  if (!el) return
  const r = el.getBoundingClientRect()
  const x = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width))
  setFromHsv({ h: x * 360, s: hsv.value.s, v: hsv.value.v })
}

function onHueDown(e: PointerEvent) {
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
  pickFromHue(e)
}

function onHueMove(e: PointerEvent) {
  if ((e.currentTarget as HTMLElement).hasPointerCapture(e.pointerId)) pickFromHue(e)
}

// —— 文字输入 ——
// R/G/B 和 hex 都容得下乱敲：不是合法颜色就**先不改**，等用户敲完再说。
// 边打边拦的话，输入 `#ab` 的中间那一瞬间永远是「不合法」，光标就被按住了。
const rgbDraft = ref({ r: 0, g: 0, b: 0 })
const hexDraft = ref('')

watch(hex, (value) => {
  hexDraft.value = value
  const { r, g, b } = hexToRgb(value)
  rgbDraft.value = { r, g, b }
})

function commitChannel(channel: 'r' | 'g' | 'b', raw: string) {
  const n = Number.parseInt(raw, 10)
  if (Number.isNaN(n)) return
  setFromHex(rgbToHex(n, rgbDraft.value.g, rgbDraft.value.b))
}

function commitHexDraft(raw: string) {
  hexDraft.value = raw
  const value = normalizeHex(raw)
  if (value) setFromHex(value)
}

// 系统取色器。Chromium 的 `<input type="color">` 自带吸管，
// 不用自己实现「读屏幕像素」那套 —— 那个在 WebView 里既麻烦又不准。
function onNativePick(e: Event) {
  setFromHex((e.target as HTMLInputElement).value)
}

function reset() {
  emit('change', null)
  hex.value = currentAppBackground()
}

function cancel() {
  // 取消 = 回到打开时的样子。这一步不做的话，用户拖了半天再按取消，
  // 终端的颜色已经变了，界面上却写着「没改」。
  emit('change', props.initial)
  emit('update:modelValue', false)
}

function confirm() {
  emit('update:modelValue', false)
}

// 预设色：第一排深底，第二排浅底。都是「当终端底色不刺眼」的那一类，
// 不是常见的 UI 色板 —— 里面那些饱和的蓝和绿拿来当终端底会晃眼。
const PRESETS: string[][] = [
  ['#0b0e14', '#11151f', '#1a1f2b', '#0f1f1a', '#241a10', '#2a1215', '#101a24', '#241a2e'],
  ['#f5f6f8', '#eef1f3', '#e8f0ea', '#fdf3e3', '#f6e9ec', '#e9edf6', '#f0eefa', '#e6f5f5'],
]
</script>

<template>
  <UDialog
    :model-value="props.modelValue"
    :title="props.title || t('color.title')"
    width="440px"
    @update:model-value="cancel"
  >
    <div class="flex gap-4">
      <!-- 饱和度 × 明度。两层渐变叠出来，不用 canvas：
           底层是纯色相（hsl(h,100%,50%)），上面盖一层白的横向渐变（去饱和）、
           再盖一层黑的纵向渐变（压明度）。 -->
      <div
        ref="square"
        class="relative h-[168px] w-[240px] flex-none cursor-crosshair rounded-lg"
        :style="{ backgroundColor: `hsl(${hsv.h}, 100%, 50%)` }"
        @pointerdown="onSquareDown"
        @pointermove="onSquareMove"
      >
        <div class="absolute inset-0 rounded-lg" style="background: linear-gradient(to right, #fff, transparent)" />
        <div class="absolute inset-0 rounded-lg" style="background: linear-gradient(to top, #000, transparent)" />
        <span
          class="pointer-events-none absolute h-3 w-3 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-white shadow-md"
          :style="{ left: `${hsv.s}%`, top: `${100 - hsv.v}%` }"
        />
      </div>

      <!-- 色相 + 系统取色器 -->
      <div class="flex w-[124px] flex-none flex-col gap-2">
        <div
          ref="hue"
          class="relative h-[168px] w-6 flex-none cursor-pointer rounded-full"
          style="background: linear-gradient(to bottom, #f00, #ff0 17%, #0f0 33%, #0ff 50%, #00f 67%, #f0f 83%, #f00)"
          @pointerdown="onHueDown"
          @pointermove="onHueMove"
        >
          <span
            class="pointer-events-none absolute left-1/2 h-3.5 w-3.5 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-white shadow-md"
            :style="{ top: `${(hsv.h / 360) * 100}%` }"
          />
        </div>

        <label class="relative mt-1 block h-9 w-full cursor-pointer overflow-hidden rounded-lg border border-line-strong">
          <span class="absolute inset-0" :style="{ background: hex }" />
          <span
            class="absolute inset-0 flex items-center justify-center gap-1.5 text-[11px] font-medium"
            :class="prefersDarkText(hex) ? 'text-black/70' : 'text-white/85'"
          >
            <UIcon name="pipette" :size="13" />
            {{ t('color.pick') }}
          </span>
          <!-- 原生的 color input 自己就带吸管。盖在上面的那一层是透明的，
               看着是我们画的色块，点下去调出来的是系统那套。 -->
          <input
            type="color"
            class="absolute inset-0 h-full w-full cursor-pointer opacity-0"
            :value="hex"
            @input="onNativePick"
          />
        </label>
      </div>
    </div>

    <!-- R / G / B + hex -->
    <div class="mt-4 flex items-center gap-2">
      <div v-for="channel in (['r', 'g', 'b'] as const)" :key="channel" class="flex-1">
        <div class="mb-1 text-[10px] tracking-wide text-ink-faint uppercase">{{ channel }}</div>
        <input
          type="number"
          min="0"
          max="255"
          class="w-full rounded-md border border-line bg-sunken px-2 py-1 font-mono text-xs text-ink outline-none focus:border-accent"
          :value="rgbDraft[channel]"
          @change="commitChannel(channel, ($event.target as HTMLInputElement).value)"
        />
      </div>
      <div class="flex-[1.4]">
        <div class="mb-1 text-[10px] tracking-wide text-ink-faint uppercase">hex</div>
        <input
          type="text"
          spellcheck="false"
          class="w-full rounded-md border border-line bg-sunken px-2 py-1 font-mono text-xs text-ink outline-none focus:border-accent"
          :value="hexDraft"
          @change="commitHexDraft(($event.target as HTMLInputElement).value)"
        />
      </div>
    </div>

    <!-- 预设色 -->
    <div class="mt-4 space-y-2">
      <div v-for="(row, index) in PRESETS" :key="index" class="flex gap-1.5">
        <button
          v-for="preset in row"
          :key="preset"
          type="button"
          class="h-7 flex-1 rounded-md border transition-transform duration-100 hover:scale-105"
          :class="preset === hex ? 'border-accent ring-1 ring-accent' : 'border-line-strong'"
          :style="{ background: preset }"
          :aria-label="preset"
          :title="preset"
          @click="setFromHex(preset)"
        />
      </div>
    </div>

    <p class="mt-3 text-[11px] leading-relaxed text-ink-faint">
      {{ t('color.derived') }}
      <span class="font-mono">{{ prefersDarkText(hex) ? t('color.darkText') : t('color.lightText') }}</span>
    </p>

    <template #footer>
      <UButton
        v-if="usesCustom"
        variant="ghost"
        size="sm"
        class="mr-auto"
        @click="reset"
      >
        {{ t('color.reset') }}
      </UButton>
      <UButton variant="default" size="sm" @click="cancel">{{ t('action.cancel') }}</UButton>
      <UButton variant="primary" size="sm" @click="confirm">{{ t('color.apply') }}</UButton>
    </template>
  </UDialog>
</template>
