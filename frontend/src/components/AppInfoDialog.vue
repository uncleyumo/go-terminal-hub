<script setup lang="ts">
// 「关于这个 app」弹窗。顶栏 GitHub 图标左边那个 info 按钮打开。
//
// 版本号和构建时间由后端给（AppService.GetBuildInfo），不是编译进前端的：
// 链接器在 go build 时把 build/windows/Taskfile.yml 里算出来的两个值用 -X
// 写进 Go 的包级字符串变量，前端只是转个手。所以这里不用、也不该去 import
// 任何 npm 上的 package.json 版本 —— 那是前端的版本，跟 exe 不是一回事。
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Browser } from '@wailsio/runtime'
import UButton from './ui/UButton.vue'
import UDialog from './ui/UDialog.vue'
import UIcon from './ui/UIcon.vue'
import UTooltip from './ui/UTooltip.vue'
import { notify } from './ui/toast'
import {
  clearPasteCache,
  getBuildInfo,
  getPasteCacheInfo,
  openExeDir,
  type BuildInfo,
  type PasteCacheInfo,
} from '../api'
import brandIcon from '../assets/brand/icon.svg'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 交给 App.vue：关掉这个弹窗、打开更新弹窗（状态和动作都在那边）。 */
  'check-update': []
}>()

const { t } = useI18n()

const info = ref<BuildInfo | null>(null)
// 取不到就显示空行而不是转圈：这是个可有可无的弹窗，
// 后端万一没绑上，用户不该被一个永远转的圈卡住。
const failed = ref(false)

// 开发者名字和邮箱写死在前端，不走后端：它们跟着人走，不跟着 exe 走，
// 而且前端是唯一会用到的地方。跟仓库地址一个道理。
const AUTHOR = 'uncleyumo'
const CONTACT_EMAIL = 'uncleyumo@163.com'

// 后端给的是 "2026-09-29T18:03:44+0800"。
// 不用 new Date(raw) 转 —— 那个时区写法是 +0800（没有冒号），
// 不是合法的 ISO 8601，解析出来可能是 Invalid Date。所以自己拆。
// 拆不出就原样显示：宁可丑也不撒谎。
function formatBuildTime(raw: string): string {
  const m = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})([+-]\d{2}):?(\d{2})?$/.exec(raw)
  if (!m) return raw
  const zone = m[3] ? ` (UTC${m[3]}:${m[4] ?? '00'})` : ''
  return `${m[1]} ${m[2]}${zone}`
}

// 图片缓存：手机在远程终端里发的图、桌面 Ctrl+V 粘进来的图，
// 都会落到 ~/.go-terminal-hub/paste 下，不会自己过期，所以给一个清空入口。空的时候按钮是灰的。
const cache = ref<PasteCacheInfo | null>(null)
const clearing = ref(false)

const cacheText = computed(() => {
  const c = cache.value
  if (!c || !c.files) return t('app.infoCacheEmpty')
  return `${c.files} · ${formatBytes(c.bytes ?? 0)}`
})

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

function refreshCache() {
  getPasteCacheInfo()
    .then((v) => (cache.value = v))
    .catch((error) => console.warn('get paste cache info failed:', error))
}

async function clearCache() {
  if (clearing.value) return
  clearing.value = true
  try {
    const removed = await clearPasteCache()
    const files = removed?.files ?? 0
    const size = formatBytes(removed?.bytes ?? 0)
    refreshCache()
    notify.success(t('app.infoCacheCleared', { files, size }))
  } catch (error) {
    notify.error(`${t('app.infoCacheClearFailed')}: ${String(error)}`)
  } finally {
    clearing.value = false
  }
}

// 打开时才去取：版本号那两个值编译完就定死了，不会变，弹窗关着的时候没必要调后端。
// 缓存大小倒是会变，所以每次打开都重新问一次。
watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    failed.value = false
    getBuildInfo()
      .then((v) => (info.value = v))
      .catch((error) => {
        console.warn('get build info failed:', error)
        failed.value = true
      })
    refreshCache()
  },
)

// 交给系统默认邮件客户端，不在 app 里做输入框：
// 写信这件事要回车、要附件、要草稿，app 里重做一个只会更差。
// Browser.OpenURL 是顶栏仓库链接用的同一个函数。
function writeEmail() {
  Browser.OpenURL(`mailto:${CONTACT_EMAIL}`).catch((error) => {
    console.warn('open mail client failed:', error)
  })
}

// 打开 exe 所在目录。更新失败时用户要去找备份文件，先得知道 exe 装在哪。
function openExeFolder() {
  openExeDir().catch((error) => {
    notify.error(`${t('app.infoOpenExeDirFailed')}: ${String(error)}`)
  })
}
</script>

<template>
  <UDialog
    :model-value="modelValue"
    :title="t('app.infoTitle')"
    width="440px"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <!-- 头部：图标、名称和一句话定位。版本号做成名字后面的徽标，
         不再单独占一行 —— 版本是「这个 app 是谁」的一部分，不是一条属性。 -->
    <div class="flex items-center gap-3.5">
      <img :src="brandIcon" alt="" class="h-14 w-14 flex-none" />
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-2">
          <span class="text-[15px] font-semibold text-ink">go-terminal-hub</span>
          <span
            v-if="info?.version"
            class="rounded-full border border-line bg-raised px-2 py-px font-mono text-[10px] leading-4 text-ink-dim"
          >
            v{{ info.version }}
          </span>
        </div>
        <p class="mt-1 text-[12px] leading-relaxed text-ink-faint">
          {{ t('app.infoTagline') }}
        </p>
      </div>
    </div>

    <!-- 属性区：每行三格（标签 / 值 / 动作），行高固定 44px。
         行高固定是为了不管右边有没有按钮，横向分隔线都等距；
         动作列一律靠右，几个按钮才会落在同一条竖线上。 -->
    <dl
      data-selectable
      class="mt-4 divide-y divide-line overflow-hidden rounded-lg border border-line bg-sunken/40"
    >
      <div class="flex h-11 items-center gap-3 px-3.5">
        <dt class="w-[92px] flex-none whitespace-nowrap text-[12px] text-ink-faint">
          {{ t('app.infoBuildTime') }}
        </dt>
        <dd class="min-w-0 flex-1 truncate font-mono text-[12px] text-ink">
          {{ info ? formatBuildTime(info.buildTime) : '—' }}
        </dd>
      </div>

      <div class="flex h-11 items-center gap-3 px-3.5">
        <dt class="w-[92px] flex-none whitespace-nowrap text-[12px] text-ink-faint">{{ t('app.infoAuthor') }}</dt>
        <dd class="min-w-0 flex-1 truncate text-[12px] text-ink">{{ AUTHOR }}</dd>
      </div>

      <div class="flex h-11 items-center gap-3 px-3.5">
        <dt class="w-[92px] flex-none whitespace-nowrap text-[12px] text-ink-faint">{{ t('app.infoContact') }}</dt>
        <dd class="min-w-0 flex-1 truncate font-mono text-[12px] text-ink">{{ CONTACT_EMAIL }}</dd>
        <UTooltip :content="t('app.infoWrite')">
          <UButton
            variant="ghost"
            size="sm"
            square
            icon="edit"
            :aria-label="t('app.infoWrite')"
            @click="writeEmail"
          />
        </UTooltip>
      </div>

      <div class="flex h-11 items-center gap-3 px-3.5">
        <dt class="w-[92px] flex-none whitespace-nowrap text-[12px] text-ink-faint">{{ t('app.infoExeDir') }}</dt>
        <dd class="min-w-0 flex-1" />
        <UButton variant="ghost" size="sm" icon="folder" @click="openExeFolder">
          {{ t('app.infoOpenExeDir') }}
        </UButton>
      </div>

      <div class="flex h-11 items-center gap-3 px-3.5">
        <dt class="w-[92px] flex-none whitespace-nowrap text-[12px] text-ink-faint">{{ t('app.infoCache') }}</dt>
        <dd class="min-w-0 flex-1 truncate font-mono text-[12px] text-ink">{{ cacheText }}</dd>
        <UButton
          variant="ghost"
          size="sm"
          icon="trash"
          :disabled="!cache?.files || clearing"
          @click="clearCache"
        >
          {{ t('app.infoCacheClear') }}
        </UButton>
      </div>
    </dl>

    <p v-if="failed" class="mt-3 flex items-start gap-2 text-[12px] text-warn">
      <UIcon name="warn" :size="14" class="mt-0.5 flex-none" />
      <span>{{ t('app.infoFailed') }}</span>
    </p>

    <template #footer>
      <UButton
        variant="ghost"
        size="sm"
        icon="refresh"
        class="mr-auto"
        @click="emit('check-update')"
      >
        {{ t('update.checkButton') }}
      </UButton>
      <UButton variant="default" size="sm" @click="emit('update:modelValue', false)">
        {{ t('action.close') }}
      </UButton>
    </template>
  </UDialog>
</template>
