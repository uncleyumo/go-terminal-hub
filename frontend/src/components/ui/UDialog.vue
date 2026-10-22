<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue'
import UIcon from './UIcon.vue'
import { confirmState } from './confirm'
import { captureFocus, focusInto, trapFocus } from './focus'

const props = withDefaults(
  defineProps<{
    modelValue: boolean
    title: string
    /** 内容区最大高度。弹框整体不超出视口，超出的部分归内容区滚 */
    bodyMaxHeight?: string
    /** 弹框最大宽度。写成一个 prop 而不是写死在样式里：
     *  560 是当初按「确认框只有一句话」定的，会话表单那八种 kind 排不下，
     *  只能让调用方自己说要多宽。 */
    width?: string
    /** 内容区不要内边距、也不要自己滚。
     *  设置窗口是左右两栏，滚动归右栏自己管 —— 外面再套一层 padding 和滚动条，
     *  左边那条竖线就会浮在半空中。 */
    flush?: boolean
  }>(),
  { bodyMaxHeight: '62vh', width: '560px', flush: false },
)

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

const panel = ref<HTMLElement | null>(null)
const body = ref<HTMLElement | null>(null)
let restoreFocus: (() => void) | null = null

// 叠着开的弹窗（设置里再点「远程访问」这类）。Esc 和 Tab 只归最上面那一层管：
// 不排这个队的话，一按 Esc 会把两层一起关掉 —— 用户想的是「退回设置」。
const dialogStack = reactive<symbol[]>([])
const dialogId = Symbol('dialog')
const isTopmost = () => dialogStack[dialogStack.length - 1] === dialogId

// 叠着开的时候按打开顺序排层，不能只靠先后顺序：
// 所有弹窗都 Teleport 到 body，而 Teleport 的先后由组件写在模板里的位置决定，
// 不按用户点的先后 —— 从设置里再开「关于」，关于会盖在设置底下。
// 40 起步、第几层就 +几，始终低于确认框和提示条的 50。
const zIndex = computed(() => 40 + Math.max(0, dialogStack.indexOf(dialogId)))

function close() {
  emit('update:modelValue', false)
}

function onKeydown(e: KeyboardEvent) {
  // 确认框开着的时候，键盘归它管。
  //
  // 不挡的话，在确认框里按 Esc 会把确认框和底下这个弹框一起关掉 ——
  // 用户想的是「取消这一步」，结果连整个表单都没了。
  if (confirmState.open) return
  if (!isTopmost()) return

  // Tab 在弹框里转圈，别跑到背后的页面上去
  if (panel.value) trapFocus(panel.value, e)
  if (e.key === 'Escape') close()
}

// 只在打开期间挂监听：弹框关着的时候，Esc 不该被它吃掉。
watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      dialogStack.push(dialogId)
      window.addEventListener('keydown', onKeydown)
      // 焦点挪进弹框；关的时候还回去
      restoreFocus = captureFocus()
      void nextTick(() => {
        // 优先落在内容区的第一个能聚焦的东西上（表单弹窗就是第一个输入框），
        // 落点跟用户看到的顺序一致。
        if (panel.value) focusInto(panel.value, body.value)
      })
    } else {
      const at = dialogStack.indexOf(dialogId)
      if (at >= 0) dialogStack.splice(at, 1)
      window.removeEventListener('keydown', onKeydown)
      restoreFocus?.()
      restoreFocus = null
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  const at = dialogStack.indexOf(dialogId)
  if (at >= 0) dialogStack.splice(at, 1)
  window.removeEventListener('keydown', onKeydown)
  restoreFocus?.()
  restoreFocus = null
})
</script>

<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition duration-150 ease-out"
      enter-from-class="opacity-0"
      leave-active-class="transition duration-100 ease-in"
      leave-to-class="opacity-0"
    >
      <div
        v-if="modelValue"
        class="fixed inset-0 flex items-center justify-center bg-black/50 p-6 backdrop-blur-[2px]"
        :style="{ zIndex }"
        @mousedown.self="close"
      >
        <Transition
          appear
          enter-active-class="transition duration-150 ease-out"
          enter-from-class="opacity-0 scale-95 translate-y-1"
        >
          <div
            ref="panel"
            role="dialog"
            aria-modal="true"
            :aria-label="title"
            tabindex="-1"
            class="flex max-h-full w-full flex-col overflow-hidden rounded-xl border border-line-strong bg-surface shadow-2xl shadow-black/40 focus:outline-none"
            :style="{ maxWidth: width }"
          >
            <div class="flex flex-none items-center gap-2 border-b border-line px-5 py-3.5">
              <span class="flex-1 text-[13px] font-semibold text-ink">{{ title }}</span>
              <button
                type="button"
                class="flex h-6 w-6 items-center justify-center rounded-md text-ink-faint transition-colors hover:bg-raised hover:text-ink"
                @click="close"
              >
                <UIcon name="x" :size="15" />
              </button>
            </div>

            <div
              ref="body"
              :class="
                flush
                  ? 'min-h-0 flex-1 overflow-hidden'
                  : 'min-h-0 flex-1 overflow-y-auto px-5 py-4'
              "
              :style="flush ? undefined : { maxHeight: bodyMaxHeight }"
            >
              <slot />
            </div>

            <div
              v-if="$slots.footer"
              class="flex flex-none items-center justify-end gap-2 border-t border-line bg-sunken/60 px-5 py-3"
            >
              <slot name="footer" />
            </div>
          </div>
        </Transition>
      </div>
    </Transition>
  </Teleport>
</template>
