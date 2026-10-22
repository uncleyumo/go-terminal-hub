<script setup lang="ts">
// 「手机远程接管」的设置面板。
//
// 连接方向是反的：本地 hub 主动拨到云端的 relay，所以这里填的是 relay 的地址。
// 家里这台机器不需要公网 IP、不用端口映射、不用内网穿透。
//
// 两层身份，分开填：
//
//   使用 key     中心站签发（hubkey 签出来的），粘贴进来。永远不给手机用户。
//   登陆密钥     本机生成，交给最终用户（手机）登录用。
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import UButton from './ui/UButton.vue'
import UDialog from './ui/UDialog.vue'
import UInput from './ui/UInput.vue'
import USwitch from './ui/USwitch.vue'
import { notify } from './ui/toast'
import {
  generateRemoteSecret,
  testRemoteConnection,
  type RemoteProbeResult,
  type RemoteStatus,
} from '../api'
import { Clipboard } from '@wailsio/runtime'

export interface RemoteConfig {
  enabled: boolean
  url: string
  key: string
  secret: string
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
    if (open) {
      draft.value = { ...props.config }
      probe.value = null
    }
  },
)

// 改了地址或 key，上一次的测试结果就不算数了——留着会让人以为新填的也通过了
watch(
  () => [draft.value.url, draft.value.key],
  () => (probe.value = null),
)

// 启用时才需要填地址和 key，没启用的时候这些字段是灰的
const needsConnection = computed(() => draft.value.enabled)

const canSave = computed(() => {
  if (!draft.value.enabled) return true
  return draft.value.url.trim() !== '' && draft.value.key.trim() !== ''
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

// 生成登陆密钥。已有的就问一句要不要换——换了手机那边当场失效。
async function onGenerateSecret() {
  if (draft.value.secret) {
    if (!confirm(t('remote.secretRotateConfirm'))) return
  }
  try {
    draft.value.secret = await generateRemoteSecret()
  } catch (error) {
    notify.error(String(error))
  }
}

// 「测试连接」：拿当前填在框里的地址和 key 真拨一次，不落盘。
//
// 为什么值得有：地址填错、key 多复制了一个空格、反代没转发 Upgrade 头，
// 这三种错在界面上长得一模一样——都是「连接失败」。真拨一次能当场分清。
const testing = ref(false)
const probe = ref<RemoteProbeResult | null>(null)

const canTest = computed(
  () => draft.value.url.trim() !== '' && draft.value.key.trim() !== '',
)

const probeText = computed(() => {
  const r = probe.value
  if (!r) return ''
  if (!r.ok) return r.message || t('remote.status.error')
  return r.already ? t('remote.testAlready') : t('remote.testOk')
})

async function onTest() {
  if (testing.value) return
  testing.value = true
  probe.value = null
  try {
    probe.value = await testRemoteConnection(draft.value.url.trim(), draft.value.key.trim())
  } catch (error) {
    probe.value = { ok: false, already: false, message: String(error) }
  } finally {
    testing.value = false
  }
}

async function onCopy(text: string, label: string) {
  if (!text) return
  try {
    await Clipboard.SetText(text)
    notify.success(label)
  } catch (error) {
    notify.error(String(error))
  }
}

// 手机用户要的不是一个裸密钥，是一个能直接点开的链接。
// key + 密钥都放进 fragment：fragment 不进访问日志、不进 Referer。
const shareLink = computed(() => {
  if (!draft.value.url.trim() || !draft.value.secret) return ''
  try {
    const u = new URL(draft.value.url.trim().replace(/^ws/, 'http'))
    const base = `${u.protocol === 'wss:' ? 'https:' : 'http:'}//${u.host}/`
    return `${base}#key=${encodeURIComponent(draft.value.key.trim())}&secret=${encodeURIComponent(draft.value.secret)}`
  } catch {
    return ''
  }
})

function onSave() {
  emit('save', {
    ...draft.value,
    url: draft.value.url.trim(),
    key: draft.value.key.trim(),
    secret: draft.value.secret.trim(),
  })
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

      <!-- 不用 label 包开关：label 是给表单控件用的，包在 role="switch" 的
           button 外面，读屏软件读不到开关的名字。改成 aria-labelledby
           指向这段文字，名字还是这段文字。 -->
      <div class="flex items-center gap-3">
        <USwitch
          :model-value="draft.enabled"
          aria-labelledby="remoteEnableLabel"
          @update:model-value="draft.enabled = $event"
        />
        <span id="remoteEnableLabel" class="cursor-pointer" @click="draft.enabled = !draft.enabled">
          {{ t('remote.enable') }}
        </span>
      </div>

      <div v-if="needsConnection" class="space-y-3">
        <div>
          <div class="mb-1 text-xs text-ink-faint">{{ t('remote.url') }}</div>
          <UInput v-model="draft.url" placeholder="wss://hub.example.com/ws/agent" />
          <div class="mt-1 text-[11px] leading-4 text-ink-faint">{{ t('remote.urlHint') }}</div>
        </div>

        <!-- 使用 key：管理员在服务器上用 hubkey 签出来的，粘贴进来 -->
        <div>
          <div class="mb-1 text-xs text-ink-faint">{{ t('remote.key') }}</div>
          <UInput v-model="draft.key" class="flex-1" :placeholder="t('remote.keyPlaceholder')" />
          <div class="mt-1 text-[11px] leading-4 text-ink-faint">{{ t('remote.keyHint') }}</div>
        </div>

        <!-- 登陆密钥：本机生成，交给手机用户。不生成也能跑，但手机就登不进来 -->
        <div>
          <div class="mb-1 text-xs text-ink-faint">{{ t('remote.secret') }}</div>
          <div class="flex gap-2">
            <UInput v-model="draft.secret" class="flex-1" :placeholder="t('remote.secretPlaceholder')" />
            <UButton variant="default" size="sm" @click="onGenerateSecret">
              {{ t('remote.generate') }}
            </UButton>
            <UButton
              variant="default"
              size="sm"
              :disabled="!draft.secret"
              @click="onCopy(draft.secret, t('remote.secretCopied'))"
            >
              {{ t('remote.copy') }}
            </UButton>
          </div>
          <div class="mt-1 text-[11px] leading-4 text-ink-faint">{{ t('remote.secretHint') }}</div>
        </div>

        <!-- 发给手机用户：一个带凭证的链接，点开就进登录页 -->
        <div v-if="shareLink">
          <div class="flex gap-2">
            <UButton
              variant="default"
              size="sm"
              @click="onCopy(shareLink, t('remote.linkCopied'))"
            >
              {{ t('remote.copyLink') }}
            </UButton>
            <span class="min-w-0 flex-1 truncate text-[11px] text-ink-faint" :title="shareLink">
              {{ t('remote.linkHint') }}
            </span>
          </div>
        </div>

        <!-- 真拨一次。地址错、key 多个空格、反代没转发 Upgrade 头，
             这三种在界面上都是同一句「连接失败」，不试分不清。 -->
        <div class="flex items-center gap-2">
          <UButton variant="default" size="sm" :disabled="!canTest || testing" @click="onTest">
            {{ testing ? t('remote.testing') : t('remote.test') }}
          </UButton>
          <span
            v-if="probeText"
            class="min-w-0 flex-1 truncate text-[11px]"
            :class="probe?.ok ? 'text-pos' : 'text-neg'"
            :title="probeText"
          >
            {{ probeText }}
          </span>
        </div>

        <div class="flex items-center gap-3">
          <USwitch
            :model-value="draft.autoTakeover"
            aria-labelledby="remoteAutoTakeoverLabel"
            @update:model-value="draft.autoTakeover = $event"
          />
          <span
            id="remoteAutoTakeoverLabel"
            class="cursor-pointer"
            @click="draft.autoTakeover = !draft.autoTakeover"
          >
            {{ t('remote.autoTakeover') }}
          </span>
        </div>
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
