<script setup lang="ts">
// 「手机远程接管」的设置面板。
//
// 连接方向是反的：本地 hub 主动拨到云端的 relay，所以这里填的是 relay 的地址。
// 家里这台机器不需要公网 IP、不用端口映射、不用内网穿透。
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import UButton from './ui/UButton.vue'
import UDialog from './ui/UDialog.vue'
import UInput from './ui/UInput.vue'
import USwitch from './ui/USwitch.vue'
import { notify } from './ui/toast'
import { generateRemoteToken, type RemoteStatus } from '../api'
import { Clipboard } from '@wailsio/runtime'

export interface RemoteConfig {
  enabled: boolean
  url: string
  token: string
  autoTakeover: boolean
}

const props = defineProps<{
  modelValue: boolean
  config: RemoteConfig
  status: RemoteStatus | null
  /** 面板打开期间设置会被改，但只有点「保存」才落盘 */
  saving?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  save: [config: RemoteConfig]
}>()

const { t } = useI18n()

// 本地副本：用户改到一半又关掉的话，不该污染外面那份
const draft = ref<RemoteConfig>({ ...props.config })

watch(
  () => props.modelValue,
  (open) => {
    if (open) draft.value = { ...props.config }
  },
)

// 启用时才需要填地址和令牌，没启用的时候这两个字段是灰的
const needsConnection = computed(() => draft.value.enabled)

const canSave = computed(() => {
  if (!draft.value.enabled) return true
  return draft.value.url.trim() !== '' && draft.value.token.trim() !== ''
})

const statusText = computed(() => {
  const s = props.status
  if (!s || !draft.value.enabled) return t('remote.status.disabled')
  switch (s.state) {
    case 'connecting':
      return t('remote.status.connecting')
    case 'connected':
      return s.browserOnline ? t('remote.status.connectedWithBrowser') : t('remote.status.connected')
    case 'error':
      return `${t('remote.status.error')}${s.errMsg ? `: ${s.errMsg}` : ''}`
    default:
      return t('remote.status.disabled')
  }
})

const statusTone = computed(() => {
  const s = props.status
  if (!draft.value.enabled || !s) return 'text-ink-faint'
  if (s.state === 'connected') return s.browserOnline ? 'text-pos' : 'text-ink'
  if (s.state === 'error') return 'text-neg'
  return 'text-ink-faint'
})

async function onGenerate() {
  try {
    draft.value.token = await generateRemoteToken()
  } catch (error) {
    notify.error(String(error))
  }
}

async function onCopyToken() {
  if (!draft.value.token) return
  try {
    await Clipboard.SetText(draft.value.token)
    notify.success(t('remote.tokenCopied'))
  } catch (error) {
    notify.error(String(error))
  }
}

function onSave() {
  emit('save', { ...draft.value, url: draft.value.url.trim(), token: draft.value.token.trim() })
}
</script>

<template>
  <UDialog
    :model-value="props.modelValue"
    :title="t('remote.title')"
    width="620px"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="space-y-4 text-sm">
      <p class="text-xs leading-5 text-ink-faint">
        {{ t('remote.intro') }}
      </p>

      <!-- 这是一个远程代码执行面，不是普通网页。开着的时候这台机器上
           任何程序都能被远程敲键盘，这句话必须摆在开关旁边。 -->
      <div class="rounded-lg border border-line bg-warn/10 p-3 text-xs leading-5 text-ink">
        {{ t('remote.warning') }}
      </div>

      <label class="flex items-center gap-3">
        <USwitch :model-value="draft.enabled" @update:model-value="draft.enabled = $event" />
        <span>{{ t('remote.enable') }}</span>
      </label>

      <div v-if="needsConnection" class="space-y-3">
        <div>
          <div class="mb-1 text-xs text-ink-faint">{{ t('remote.url') }}</div>
          <UInput v-model="draft.url" placeholder="wss://hub.example.com/ws/agent" />
          <div class="mt-1 text-[11px] leading-4 text-ink-faint">{{ t('remote.urlHint') }}</div>
        </div>

        <div>
          <div class="mb-1 text-xs text-ink-faint">{{ t('remote.token') }}</div>
          <div class="flex gap-2">
            <UInput v-model="draft.token" class="flex-1" :placeholder="t('remote.tokenPlaceholder')" />
            <UButton variant="default" size="sm" @click="onGenerate">
              {{ t('remote.generate') }}
            </UButton>
            <UButton variant="default" size="sm" :disabled="!draft.token" @click="onCopyToken">
              {{ t('remote.copy') }}
            </UButton>
          </div>
          <div class="mt-1 text-[11px] leading-4 text-ink-faint">{{ t('remote.tokenHint') }}</div>
        </div>

        <label class="flex items-center gap-3">
          <USwitch
            :model-value="draft.autoTakeover"
            @update:model-value="draft.autoTakeover = $event"
          />
          <span>{{ t('remote.autoTakeover') }}</span>
        </label>
        <div class="text-[11px] leading-4 text-ink-faint">{{ t('remote.autoTakeoverHint') }}</div>

        <div class="flex items-center gap-2 border-t border-line pt-3 text-xs">
          <span class="text-ink-faint">{{ t('remote.statusLabel') }}</span>
          <span :class="statusTone">{{ statusText }}</span>
        </div>
      </div>
    </div>

    <template #footer>
      <UButton variant="default" size="sm" @click="emit('update:modelValue', false)">
        {{ t('action.cancel') }}
      </UButton>
      <UButton variant="primary" size="sm" :disabled="!canSave || props.saving" @click="onSave">
        {{ t('action.save') }}
      </UButton>
    </template>
  </UDialog>
</template>
