<script setup lang="ts">
// 全屏屏幕取色层。
//
// 打开之前，Go 侧已经把主窗口铺满屏幕、置顶，并把「刚才那一屏」抓成了一张 PNG。
// 所以这一层铺的不是实时画面，是一张照片——但用户看过去和屏幕一模一样。
//
// 为什么读的是照片而不是「实时读屏」：照片在我们手里，读哪个像素完全可控，
// 而且点下去的那一刻画面不会继续动（实时读屏会出现「看到的和取到的差一帧」）。
//
// 坐标换算只有一次乘法：图片按**原始像素**画进 canvas，鼠标位置按
// 「元素宽度 → 图片宽度」换算过去。DPI 缩放因此自动被抹平，
// 不用在两个坐标系之间来回折腾。
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

const props = defineProps<{ image: string }>()
const emit = defineEmits<{ pick: [hex: string]; cancel: [] }>()

const imgEl = ref<HTMLImageElement | null>(null)
const loupeEl = ref<HTMLCanvasElement | null>(null)

const cursor = ref({ x: 0, y: 0 })
const hoverHex = ref('#000000')

// 离屏 canvas：图片按原始尺寸画进去，之后所有 getImageData 都在它上面取。
const canvas = document.createElement('canvas')
let ctx: CanvasRenderingContext2D | null = null

// 放大镜：源区域 11×11 像素，放大 8 倍。
const ZOOM = 8
const SAMPLE = 11

function part(n: number): string {
  return n.toString(16).padStart(2, '0')
}

function readPixelAt(clientX: number, clientY: number): { hex: string; ix: number; iy: number } | null {
  const img = imgEl.value
  if (!img || !ctx) return null
  const rect = img.getBoundingClientRect()
  if (rect.width === 0 || rect.height === 0) return null
  const ix = Math.floor(((clientX - rect.left) / rect.width) * canvas.width)
  const iy = Math.floor(((clientY - rect.top) / rect.height) * canvas.height)
  if (ix < 0 || iy < 0 || ix >= canvas.width || iy >= canvas.height) return null
  const [r, g, b] = ctx.getImageData(ix, iy, 1, 1).data
  return { hex: `#${part(r)}${part(g)}${part(b)}`, ix, iy }
}

function drawLoupe(ix: number, iy: number) {
  const loupe = loupeEl.value
  if (!loupe || !ctx) return
  const lctx = loupe.getContext('2d')
  if (!lctx) return
  const half = Math.floor(SAMPLE / 2)
  lctx.imageSmoothingEnabled = false
  lctx.clearRect(0, 0, loupe.width, loupe.height)
  lctx.drawImage(canvas, ix - half, iy - half, SAMPLE, SAMPLE, 0, 0, loupe.width, loupe.height)
  // 中心那一格描两道边（白 + 半透明黑）：深浅底色上都看得见。
  // 它标的是「点下去会取的那个像素」，不是光标本身。
  lctx.strokeStyle = '#fff'
  lctx.lineWidth = 1
  lctx.strokeRect(half * ZOOM + 0.5, half * ZOOM + 0.5, ZOOM - 1, ZOOM - 1)
  lctx.strokeStyle = 'rgba(0,0,0,.65)'
  lctx.strokeRect(half * ZOOM - 0.5, half * ZOOM - 0.5, ZOOM + 1, ZOOM + 1)
}

function onMove(e: PointerEvent) {
  cursor.value = { x: e.clientX, y: e.clientY }
  const hit = readPixelAt(e.clientX, e.clientY)
  if (!hit) return
  hoverHex.value = hit.hex
  drawLoupe(hit.ix, hit.iy)
}

function onClick(e: MouseEvent) {
  const hit = readPixelAt(e.clientX, e.clientY)
  if (hit) emit('pick', hit.hex)
  else emit('cancel')
}

// 放大镜贴着光标摆，靠近屏幕右下角时翻到另一侧，免得跑出可视区。
const loupeStyle = computed(() => {
  const flipX = cursor.value.x > window.innerWidth - 150
  const flipY = cursor.value.y > window.innerHeight - 170
  return {
    left: `${cursor.value.x + (flipX ? -18 : 18)}px`,
    top: `${cursor.value.y + (flipY ? -18 : 18)}px`,
    transform: `translate(${flipX ? '-100%' : '0'}, ${flipY ? '-100%' : '0'})`,
  }
})

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('cancel')
}

onMounted(() => {
  window.addEventListener('keydown', onKey)
  const img = imgEl.value
  if (!img) return
  const prepare = () => {
    canvas.width = img.naturalWidth
    canvas.height = img.naturalHeight
    ctx = canvas.getContext('2d', { willReadFrequently: true })
    ctx?.drawImage(img, 0, 0)
  }
  if (img.complete) prepare()
  else img.addEventListener('load', prepare, { once: true })
})

onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <div
      class="fixed inset-0 z-[9999] cursor-crosshair select-none"
      @pointermove="onMove"
      @click="onClick"
      @contextmenu.prevent="emit('cancel')"
    >
      <img ref="imgEl" :src="image" alt="" class="h-full w-full object-fill" draggable="false" />

      <div class="pointer-events-none absolute flex flex-col items-center gap-1" :style="loupeStyle">
        <canvas
          ref="loupeEl"
          :width="SAMPLE * ZOOM"
          :height="SAMPLE * ZOOM"
          class="rounded-md border-2 border-white shadow-lg"
        />
        <span class="rounded bg-black/75 px-1.5 py-0.5 font-mono text-[11px] text-white">{{ hoverHex }}</span>
      </div>

      <p class="pointer-events-none absolute bottom-6 left-1/2 -translate-x-1/2 rounded-md bg-black/70 px-3 py-1.5 text-[12px] text-white">
        {{ $t('color.pickHint') }}
      </p>
    </div>
  </Teleport>
</template>
