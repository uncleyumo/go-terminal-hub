<script setup lang="ts">
// 「关于这个 app」弹窗。顶栏 GitHub 图标左边那个 info 按钮打开。
//
// 版本号和构建时间由后端给（AppService.GetBuildInfo），不是编译进前端的：
// 链接器在 go build 时把 build/windows/Taskfile.yml 里算出来的两个值用 -X
// 写进 Go 的包级字符串变量，前端只是转个手。所以这里不用、也不该去 import
// 任何 npm 上的 package.json 版本 —— 那是前端的版本，跟 exe 不是一回事。
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Browser } from '@wailsio/runtime'
import UButton from './ui/UButton.vue'
import UDialog from './ui/UDialog.vue'
import UIcon from './ui/UIcon.vue'
import UTooltip from './ui/UTooltip.vue'
import { getBuildInfo, type BuildInfo } from '../api'
import brandIcon from '../assets/brand/icon.svg'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

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

// 打开时才去取：这两个值编译完就定死了，不会变，弹窗关着的时候没必要调后端
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
</script>

<template>
  <UDialog
    :model-value="modelValue"
    :title="t('app.infoTitle')"
    width="420px"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="flex flex-col items-center gap-3 pt-1 pb-1 text-center">
      <img :src="brandIcon" alt="" width="72" height="72" class="h-[72px] w-[72px]" />
      <div>
        <div class="text-[15px] font-semibold text-ink">go-terminal-hub</div>
        <div class="mt-0.5 text-[12px] text-ink-faint">{{ t('app.infoTagline') }}</div>
      </div>
    </div>

    <dl class="mt-4 border-t border-line">
      <div class="flex items-baseline gap-3 border-b border-line py-2">
        <dt class="w-24 flex-none text-[12px] text-ink-faint">{{ t('app.infoVersion') }}</dt>
        <dd class="min-w-0 flex-1 font-mono text-[12px] text-ink">{{ info?.version || '—' }}</dd>
      </div>

      <div class="flex items-baseline gap-3 border-b border-line py-2">
        <dt class="w-24 flex-none text-[12px] text-ink-faint">{{ t('app.infoBuildTime') }}</dt>
        <dd class="min-w-0 flex-1 font-mono text-[12px] text-ink">
          {{ info ? formatBuildTime(info.buildTime) : '—' }}
        </dd>
      </div>

      <div class="flex items-baseline gap-3 border-b border-line py-2">
        <dt class="w-24 flex-none text-[12px] text-ink-faint">{{ t('app.infoAuthor') }}</dt>
        <dd class="min-w-0 flex-1 text-[12px] text-ink">{{ AUTHOR }}</dd>
      </div>

      <div class="flex items-baseline gap-3 py-2">
        <dt class="w-24 flex-none text-[12px] text-ink-faint">{{ t('app.infoContact') }}</dt>
        <dd class="flex min-w-0 flex-1 items-center gap-1">
          <span class="min-w-0 flex-1 truncate font-mono text-[12px] text-ink">{{
            CONTACT_EMAIL
          }}</span>
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
        </dd>
      </div>
    </dl>

    <p v-if="failed" class="mt-3 flex items-start gap-2 text-[12px] text-warn">
      <UIcon name="warn" :size="14" class="mt-0.5 flex-none" />
      <span>{{ t('app.infoFailed') }}</span>
    </p>

    <template #footer>
      <UButton variant="default" size="sm" @click="emit('update:modelValue', false)">
        {{ t('action.close') }}
      </UButton>
    </template>
  </UDialog>
</template>
