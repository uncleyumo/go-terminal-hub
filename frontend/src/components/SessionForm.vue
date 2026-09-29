<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Dialogs } from '@wailsio/runtime'
import UButton from './ui/UButton.vue'
import UDialog from './ui/UDialog.vue'
import UInput from './ui/UInput.vue'
import UNumber from './ui/UNumber.vue'
import USegmented from './ui/USegmented.vue'
import USelect from './ui/USelect.vue'
import USwitch from './ui/USwitch.vue'
import UTextarea from './ui/UTextarea.vue'
import { notify } from './ui/toast'
import { createSession, getAppWorkDir, updateSession, type DataStore, type SessionView } from '../api'
import { isTerminalKind, resolveKind, TERMINAL_TARGETS } from '../sessionKind'

const props = defineProps<{
  modelValue: boolean
  session: SessionView | null
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  saved: []
}>()

const { t } = useI18n()

const visible = computed({
  get: () => props.modelValue,
  set: (value: boolean) => emit('update:modelValue', value),
})

const isEdit = computed(() => props.session !== null)
const title = computed(() => (isEdit.value ? t('form.editTitle') : t('form.createTitle')))

const KINDS = ['bat', 'cmd', 'ps1', 'exe', 'shell', 'terminal-cmd', 'terminal-powershell']

// terminal-* 这两个**只存在于前端**：后端的白名单不认它们，
// 提交时在 toDataStore() 里翻译成 kind=shell + 拼好的 target（D39，后端零改动）。
// 定义和「怎么反着还原」都在 sessionKind.ts，列表和详情读的是同一份。
// log 模式后端还没做：internal/exec/hub/hub.go 的 switch 里 case "log" 直接
// 返回 error，选了必然启动失败。先摆出来但禁掉，做完再放开（2026-09-28 学习者指出）
const MODES = [
  { value: 'terminal', disabled: false },
  { value: 'log', disabled: true },
]
const ENCODINGS = ['auto', 'utf8', 'gbk']

const kindOptions = computed(() => KINDS.map((k) => ({ label: t(`kind.${k}`), value: k })))
const modeOptions = computed(() =>
  MODES.map((m) => ({ label: t(`mode.${m.value}`), value: m.value, disabled: m.disabled })),
)
const encodingOptions = ENCODINGS.map((e) => ({ label: e, value: e }))

interface FormState {
  id: string
  name: string
  kind: string
  target: string
  args: string
  workDir: string
  envText: string
  mode: string
  encoding: string
  cols: number
  rows: number
  autoStart: boolean
}

function blank(): FormState {
  return {
    id: '',
    name: '',
    kind: 'bat',
    target: '',
    args: '',
    workDir: '',
    envText: '',
    mode: 'terminal',
    encoding: 'auto',
    cols: 80,
    rows: 25,
    autoStart: false,
  }
}

const form = reactive<FormState>(blank())
const errors = reactive({ name: '', target: '' })

// 每次打开都把上一次的残留清掉：编辑时灌入这条配置，新建时回到默认值。
watch(visible, (open) => {
  if (!open) return
  const source = props.session?.config
  Object.assign(
    form,
    source
      ? {
          id: source.id,
          name: source.name,
          // 存的是 shell + 预设 target，还原成 terminal-cmd / terminal-powershell：
          // 不还原的话，编辑一条常驻终端会看到「shell」被选中，target 框里躺着一整条
          // 命令 —— 用户不知道那是界面替他填的，多半会当成普通 shell 给改掉。
          kind: resolveKind(source.kind, source.target),
          target: source.target,
          args: source.args,
          workDir: source.workDir,
          envText: (source.env ?? []).join('\n'),
          mode: source.mode,
          encoding: source.encoding,
          cols: source.cols,
          rows: source.rows,
          autoStart: source.autoStart,
        }
      : blank(),
  )
  errors.name = ''
  errors.target = ''
  // 新建时把工作目录预填成 app 当前的目录（2026-09-28 学习者要的）：
  // 留空虽然也能跑，但详情里显示「(empty)」看不出到底是哪儿。
  // 编辑旧配置时不碰 —— 那是人家自己填的。
  if (!source) {
    void getAppWorkDir().then((dir) => {
      // 弹窗关了、或者用户已经自己改过了，就别覆盖
      if (visible.value && form.workDir === '' && dir) form.workDir = dir
    })
  }
})

// shell 的 target 是一整条命令，不是一个文件路径，没有文件名可以拿来兜底命名，
// 所以名称只在 shell 下必填；其余 kind 留空由下面自动生成。
const isShell = computed(() => form.kind === 'shell')
// 常驻终端：既不用 target 也不用 args（那条命令前端已经拼好了）
const isTerminal = computed(() => isTerminalKind(form.kind))

// —— 名称自动生成 ——
// 格式「<名字>  (<创建时间>)」，例 xxx.exe  (2026-09-27 21:30)。
// 名字和时间之间空两个，跟时间里的空格拉开距离，一眼看得出是两段。
// 时间取生成这一刻，不是脚本文件自己的时间戳。
function pad(n: number): string {
  return String(n).padStart(2, '0')
}

// target 可能是反斜杠的 Windows 路径，也可能是正斜杠，两种分隔符都要认。
function scriptName(target: string): string {
  const parts = target.split(/[\\/]/)
  return parts[parts.length - 1] || target
}

function autoName(): string {
  const now = new Date()
  const date = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
  const time = `${pad(now.getHours())}:${pad(now.getMinutes())}`
  // 常驻终端没有 target 可取名，直接拿类型当名字：terminal-cmd / terminal-powershell
  const base = isTerminal.value ? form.kind : scriptName(form.target)
  return `${base}  (${date} ${time})`
}

// 名称还是空的就自动补上；绝不覆盖用户已经写下的名字。
watch([() => form.target, () => form.kind], () => {
  if (form.name.trim() !== '') return
  if (isTerminal.value) {
    form.name = autoName()
    return
  }
  if (isShell.value || form.target.trim() === '') return
  form.name = autoName()
})

// —— 文件选择框 ——
// 走 @wailsio/runtime 的 Dialogs：它是 runtime 自带的 RPC 通道，不经 bindings，
// 所以不需要后端加任何方法。窗口由框架自动挂上，这里不用传。
// shell 没有对应的文件类型（它的 target 是命令），所以不给按钮。
const PICK_FILTERS: Record<string, string> = {
  bat: '*.bat',
  cmd: '*.cmd',
  ps1: '*.ps1',
  exe: '*.exe;*.com',
}

const canPick = computed(() => PICK_FILTERS[form.kind] !== undefined)

async function pickFile() {
  const pattern = PICK_FILTERS[form.kind]
  if (!pattern) return
  try {
    const picked = await Dialogs.OpenFile({
      Title: t('form.pickTitle'),
      Filters: [
        { DisplayName: t(`kind.${form.kind}`), Pattern: pattern },
        { DisplayName: t('form.pickAllFiles'), Pattern: '*.*' },
      ],
    })
    // 用户点了取消：返回空串，什么都不做。
    if (!picked) return
    form.target = picked
  } catch (error) {
    notify.error(`${t('form.pickFailed')}: ${String(error)}`)
  }
}

// 工作目录的按钮弹的是「选文件夹」不是「选文件」——同一个 OpenFile，
// 靠 CanChooseFiles: false / CanChooseDirectories: true 把它切成目录模式
// （@wailsio/runtime 的 OpenFileDialogOptions 上就有这两个字段，不需要后端加方法）。
async function pickDir() {
  try {
    const picked = await Dialogs.OpenFile({
      Title: t('form.pickDirTitle'),
      CanChooseFiles: false,
      CanChooseDirectories: true,
    })
    if (!picked) return
    form.workDir = picked
  } catch (error) {
    notify.error(`${t('form.pickFailed')}: ${String(error)}`)
  }
}

const namePlaceholder = computed(() =>
  isShell.value ? t('form.namePlaceholder') : t('form.nameAutoHint'),
)

const targetPlaceholder = computed(() =>
  isShell.value ? t('form.targetShellPlaceholder') : t('form.targetPlaceholder'),
)

function toDataStore(): DataStore {
  return {
    id: form.id,
    name: form.name,
    // 常驻终端在前端有自己的类型，后端只认 shell —— 这里翻译掉（D39）
    kind: isTerminal.value ? 'shell' : form.kind,
    target: isTerminal.value ? TERMINAL_TARGETS[form.kind] : form.target,
    args: form.args,
    workDir: form.workDir,
    env: form.envText
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line.length > 0),
    mode: form.mode,
    encoding: form.encoding,
    cols: form.cols,
    rows: form.rows,
    autoStart: form.autoStart,
  }
}

const saving = ref(false)

function validate(): boolean {
  // 兜底：用户手打了路径、没走「浏览」按钮，名称一直是空的。
  if (!isShell.value && form.name.trim() === '' && form.target.trim() !== '') {
    form.name = autoName()
  }
  // 常驻终端没有 target，名字必填（跟 shell 一样），但不用去查 target 存不存在
  errors.name =
    (isShell.value || isTerminal.value) && form.name.trim() === '' ? t('form.nameRequired') : ''
  errors.target =
    !isTerminal.value && form.target.trim() === '' ? t('form.targetRequired') : ''
  return !errors.name && !errors.target
}

async function submit() {
  if (saving.value || !validate()) return
  saving.value = true
  try {
    if (isEdit.value) {
      await updateSession(form.id, toDataStore())
      notify.success(t('msg.updated'))
    } else {
      // ID 由后端生成（UUIDv7），这里不传
      await createSession(toDataStore())
      notify.success(t('msg.created'))
    }
    visible.value = false
    emit('saved')
  } catch (error) {
    notify.error(String(error))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UDialog v-model="visible" :title="title">
    <div class="flex flex-col gap-3.5">
      <!-- 标签在上、控件在下：同一列里对齐，两列并排时读起来是一条一条的 -->
      <div>
        <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
          {{ t('form.name') }}
          <span v-if="isShell" class="text-neg">*</span>
        </label>
        <UInput
          v-model="form.name"
          :placeholder="namePlaceholder"
          :invalid="!!errors.name"
          @enter="submit"
        />
        <p v-if="errors.name" class="mt-1 text-[11px] text-neg">{{ errors.name }}</p>
      </div>

      <div>
        <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
          {{ t('form.kind') }}
        </label>
        <USegmented v-model="form.kind" :options="kindOptions" />
      </div>

      <!-- 常驻终端不用 target 和 args：那条命令前端已经拼好了（D39） -->
      <template v-if="!isTerminal">
        <div>
          <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
            {{ t('form.target') }}
            <span class="text-neg">*</span>
          </label>
          <UInput
            v-model="form.target"
            :placeholder="targetPlaceholder"
            :invalid="!!errors.target"
            mono
            @enter="submit"
          >
            <template v-if="canPick" #suffix>
              <UButton variant="ghost" size="sm" icon="folder" @click="pickFile">
                {{ t('form.browse') }}
              </UButton>
            </template>
          </UInput>
          <p v-if="errors.target" class="mt-1 text-[11px] text-neg">{{ errors.target }}</p>
        </div>

        <div>
          <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
            {{ t('form.args') }}
          </label>
          <UInput v-model="form.args" :placeholder="t('form.argsPlaceholder')" mono />
        </div>
      </template>

      <div>
        <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
          {{ t('form.workDir') }}
        </label>
        <UInput v-model="form.workDir" :placeholder="t('form.workDirPlaceholder')" mono>
          <template #suffix>
            <UButton variant="ghost" size="sm" icon="folder" @click="pickDir">
              {{ t('form.browse') }}
            </UButton>
          </template>
        </UInput>
      </div>

      <div>
        <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
          {{ t('form.env') }}
        </label>
        <UTextarea v-model="form.envText" :rows="3" :placeholder="t('form.envPlaceholder')" mono />
      </div>

      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
            {{ t('form.mode') }}
          </label>
          <USegmented v-model="form.mode" :options="modeOptions" />
          <p class="mt-1.5 text-[10px] text-ink-faint">
            {{ t('mode.log') }} — {{ t('mode.logUnavailable') }}
          </p>
        </div>
        <div>
          <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
            {{ t('form.encoding') }}
            <span class="ml-1 normal-case text-ink-faint">{{ t('form.encodingHint') }}</span>
          </label>
          <USelect v-model="form.encoding" :options="encodingOptions" class="w-full" />
        </div>
      </div>

      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
            {{ t('form.cols') }}
          </label>
          <UNumber v-model="form.cols" :aria-label="t('form.cols')" />
        </div>
        <div>
          <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
            {{ t('form.rows') }}
          </label>
          <UNumber v-model="form.rows" :aria-label="t('form.rows')" />
        </div>
      </div>

      <div class="flex items-center gap-2.5">
        <USwitch v-model="form.autoStart" />
        <div>
          <div class="text-[13px] text-ink">{{ t('form.autoStart') }}</div>
          <div class="text-[11px] text-ink-faint">{{ t('form.autoStartHint') }}</div>
        </div>
      </div>
    </div>

    <template #footer>
      <UButton variant="default" size="sm" @click="visible = false">
        {{ t('action.cancel') }}
      </UButton>
      <UButton variant="primary" size="sm" :disabled="saving" @click="submit">
        {{ isEdit ? t('action.save') : t('action.create') }}
      </UButton>
    </template>
  </UDialog>
</template>
