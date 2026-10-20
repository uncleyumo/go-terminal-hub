<script setup lang="ts">
// 应用内更新弹窗（M7）。
//
// 这个组件**不持有状态**：状态在 App.vue 里，来源是后端那条 `update:state` 事件。
// 它只做两件事——按下按钮调后端，然后把传进来的 state 画出来。
//
// 为什么状态不放在这里：弹窗关着的时候更新照跑（下载、校验都在后端），
// 顶栏那个「有新版本」的小圆点也要用同一份状态。状态跟着组件走，
// 弹窗一关就丢，圆点就没得看了。
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import UButton from './ui/UButton.vue'
import UDialog from './ui/UDialog.vue'
import UIcon from './ui/UIcon.vue'
import { confirm } from './ui/confirm'
import { notify } from './ui/toast'
import {
  cancelUpdateDownload,
  checkUpdate,
  installUpdate,
  openReleasesPage,
  restartForUpdate,
  skipUpdateVersion,
  type UpdateState,
} from '../api'

const props = defineProps<{
  modelValue: boolean
  /** 后端推过来的最新一份状态。null = 还没拉到。 */
  state: UpdateState | null
  /** 当前运行中的会话数。重启更新会把它们全关掉。 */
  runningCount: number
  /** 让 App.vue 停掉所有会话，返回没停掉的会话名（空数组 = 停干净了）。 */
  stopAllSessions: () => Promise<string[]>
}>()

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

const { t } = useI18n()

// 按钮点下去到后端推回新状态之间，界面不能还显示旧的按钮（会连点两次）。
const busy = ref(false)

const phase = computed<string>(() => props.state?.state ?? 'idle')

const progress = computed(() => {
  const s = props.state
  if (!s || !s.total) return 0
  return Math.min(100, Math.round((s.written / s.total) * 100))
})

function formatBytes(bytes: number): string {
  if (!bytes) return '—'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

function formatRate(rate: number): string {
  if (!rate) return ''
  return `${formatBytes(rate)}/s`
}

async function run(action: () => Promise<unknown>) {
  if (busy.value) return
  busy.value = true
  try {
    await action()
  } catch (error) {
    // 后端在出错时会把 error 状态推过来，界面自己会变成错误页。
    // 这里只兜住那些「连事件都没发出来」的情况（比如绑定没生成）。
    notify.error(`${t('update.failed')}: ${String(error)}`)
  } finally {
    busy.value = false
  }
}

function retry() {
  void run(() => checkUpdate(true))
}

// 「立即更新」只下载，不重启。但下完迟早要重启，而重启会关掉所有终端，
// 所以这里就先说清楚（D57：更新前告诉用户「会关闭所有终端」）。
async function install() {
  if (props.runningCount > 0) {
    const ok = await confirm({
      title: t('update.confirmTitle'),
      message: t('update.confirmRunning', { n: props.runningCount }),
      confirmText: t('update.confirmContinue'),
      cancelText: t('action.cancel'),
      tone: 'danger',
    })
    if (!ok) return
  }
  void run(() => installUpdate())
}

// 真正要退出应用的那一步。先停会话，停不干净就中止——
// 带着一个还占着 PTY 的进程去退出，helper 会等不到旧进程结束（D60）。
async function restart() {
  if (props.runningCount > 0) {
    const ok = await confirm({
      title: t('update.confirmTitle'),
      message: t('update.confirmRestart', { n: props.runningCount }),
      confirmText: t('update.restart'),
      cancelText: t('action.cancel'),
      tone: 'danger',
    })
    if (!ok) return

    const failed = await props.stopAllSessions()
    if (failed.length > 0) {
      notify.error(t('update.stopFailed', { names: failed.join(', ') }))
      return
    }
  }
  await run(() => restartForUpdate())
}

async function skip() {
  const version = props.state?.version
  if (!version) return
  await run(() => skipUpdateVersion(version))
  emit('update:modelValue', false)
}

const close = () => emit('update:modelValue', false)
</script>

<template>
  <UDialog
    :model-value="modelValue"
    :title="t('update.title')"
    width="480px"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <!-- 检查中 -->
    <div v-if="phase === 'checking' || phase === 'idle'" class="flex items-center gap-3 py-6 text-[13px] text-ink-faint">
      <span class="h-4 w-4 animate-spin rounded-full border-2 border-line-strong border-t-accent" />
      <span>{{ t('update.checking') }}</span>
    </div>

    <!-- 已是最新 -->
    <div v-else-if="phase === 'latest'" class="flex items-start gap-2 py-5 text-[13px] text-ink">
      <UIcon name="check" :size="16" class="mt-0.5 flex-none text-accent" />
      <span>{{ t('update.latest', { version: state?.currentVersion || '—' }) }}</span>
    </div>

    <!-- dev 构建 / 更新器起不来 -->
    <div v-else-if="phase === 'disabled'" class="flex items-start gap-2 py-5 text-[13px] text-ink-faint">
      <UIcon name="warn" :size="16" class="mt-0.5 flex-none text-warn" />
      <span>{{ t('update.disabled') }}</span>
    </div>

    <!-- 有新版本 -->
    <div v-else-if="phase === 'available'" class="flex flex-col gap-3">
      <div class="flex items-baseline gap-2 text-[13px]">
        <span class="font-mono text-ink-faint">v{{ state?.currentVersion }}</span>
        <span class="text-ink-faint">→</span>
        <span class="font-mono font-semibold text-accent">v{{ state?.version }}</span>
        <span class="ml-auto text-[12px] text-ink-faint">{{ formatBytes(state?.size ?? 0) }}</span>
      </div>
      <!-- 更新说明按纯文本渲染：Release 正文来自外部，不 v-html -->
      <div
        v-if="state?.notes"
        class="max-h-52 overflow-y-auto whitespace-pre-wrap rounded-md border border-line bg-sunken p-3 text-[12px] leading-relaxed text-ink"
      >{{ state.notes }}</div>
      <p v-if="state?.skipped" class="text-[12px] text-warn">{{ t('update.skippedHint') }}</p>
    </div>

    <!-- 下载中 -->
    <div v-else-if="phase === 'downloading'" class="flex flex-col gap-2 py-2">
      <div class="flex items-baseline justify-between text-[12px] text-ink-faint">
        <span>{{ t('update.downloading') }}</span>
        <span class="font-mono">
          {{ formatBytes(state?.written ?? 0) }} / {{ formatBytes(state?.total ?? 0) }}
          <span v-if="state?.rate" class="ml-1">· {{ formatRate(state.rate) }}</span>
        </span>
      </div>
      <div class="h-1.5 w-full overflow-hidden rounded-full bg-sunken">
        <div class="h-full rounded-full bg-accent transition-all duration-150" :style="{ width: `${progress}%` }" />
      </div>
    </div>

    <!-- 待重启 -->
    <div v-else-if="phase === 'ready'" class="flex items-start gap-2 py-4 text-[13px] text-ink">
      <UIcon name="check" :size="16" class="mt-0.5 flex-none text-accent" />
      <span>{{ t('update.ready', { version: state?.version }) }}</span>
    </div>

    <!-- 出错 -->
    <div v-else-if="phase === 'error'" class="flex flex-col gap-2">
      <div class="flex items-start gap-2 text-[13px] text-warn">
        <UIcon name="warn" :size="16" class="mt-0.5 flex-none" />
        <span class="min-w-0 break-words">{{ state?.error || t('update.failed') }}</span>
      </div>
      <p class="text-[12px] text-ink-faint">{{ t('update.errorHint') }}</p>
    </div>

    <template #footer>
      <!-- 出错：重试 + 打开下载页 -->
      <template v-if="phase === 'error'">
        <UButton variant="ghost" size="sm" @click="openReleasesPage">{{ t('update.openPage') }}</UButton>
        <UButton variant="default" size="sm" @click="close">{{ t('action.close') }}</UButton>
        <UButton variant="primary" size="sm" :disabled="busy" @click="retry">{{ t('update.retry') }}</UButton>
      </template>

      <!-- 有新版本：立即更新 / 跳过 / 稍后 -->
      <template v-else-if="phase === 'available'">
        <UButton variant="ghost" size="sm" class="mr-auto" @click="skip">{{ t('update.skip') }}</UButton>
        <UButton variant="default" size="sm" @click="close">{{ t('update.later') }}</UButton>
        <UButton variant="primary" size="sm" :disabled="busy" @click="install">{{ t('update.install') }}</UButton>
      </template>

      <!-- 下载中：取消 -->
      <template v-else-if="phase === 'downloading'">
        <UButton variant="default" size="sm" @click="close">{{ t('update.later') }}</UButton>
        <UButton variant="primary" size="sm" @click="cancelUpdateDownload">{{ t('update.cancel') }}</UButton>
      </template>

      <!-- 待重启：立即重启 / 稍后 -->
      <template v-else-if="phase === 'ready'">
        <UButton variant="default" size="sm" class="mr-auto" @click="close">{{ t('update.later') }}</UButton>
        <UButton variant="primary" size="sm" :disabled="busy" @click="restart">{{ t('update.restart') }}</UButton>
      </template>

      <!-- 其余（checking / latest / disabled / idle）：只能关 -->
      <template v-else>
        <UButton variant="default" size="sm" @click="close">{{ t('action.close') }}</UButton>
      </template>
    </template>
  </UDialog>
</template>
