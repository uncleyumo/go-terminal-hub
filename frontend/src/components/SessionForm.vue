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

const props = defineProps<{
  modelValue: boolean
  session: SessionView | null
  /** 现存会话的 id + 名字。名称不许重复（学习者 2026-09-29 定的），
   *  重名的话列表里两条长得一模一样，用户根本分不出点的是哪条。 */
  takenNames?: Array<{ id: string; name: string }>
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

const KINDS = [
  'bat',
  'cmd',
  'ps1',
  'exe',
  'shell',
  'terminal-cmd',
  'terminal-shell',
  'terminal-powershell',
]

// terminal-* 这几个是**后端认的类型**（executor.go 的 KindWhiteList 里有），
// 命令行由后端按 kind 拼。共同点是「target 可以不填」：
// 不填就是开一个空终端等你敲，填了就先跑它再停在提示符（cmd 的 /k、PowerShell 的 -NoExit）。
// 区别在 target 接什么：
//   terminal-cmd        接**脚本路径**。后端在外面包一对引号，路径带空格才不会断，
//                       所以填命令会被 cmd 去找一个叫「dir /b」的文件而失败。
//   terminal-shell      接**命令**，原样丢给 cmd，不加引号。
//   terminal-powershell 接**命令**，走 -Command。
const TERMINAL_KINDS = new Set(['terminal-cmd', 'terminal-shell', 'terminal-powershell'])
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
          kind: source.kind,
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
  // 新建：名字交给界面自动填，用户没动过就一直跟着 kind 变。
  // 编辑：这个名字是人家的，一个字都不能碰，nameAuto 直接置 false。
  nameAuto.value = !source
  if (!source) fillAutoName()
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
// 常驻终端：target 是选填的，所以名字跟 shell 一样必填（下面 validate 认这个）
const isTerminal = computed(() => TERMINAL_KINDS.has(form.kind))

// —— 名称 ——
// 统一格式「<kind>-<6 位哈希>」，例 bat-3f9a2c、terminal-cmd-8e14b0。
// 学习者定的规则（2026-09-29）：默认名一律这个形状，且不允许重名。
// 哈希的作用是让「同一种 kind 建了两条」能分得开 —— 光叫 bat-1 / bat-2
// 还得记着自己建过几个，六位哈希不用数。
const NAME_HASH_LEN = 6

// FNV-1a 32 位。取十六进制前六位 —— 只为了一个短且稳定的编号，
// 不是安全哈希，不用 SHA。
function shortHash(input: string): string {
  let h = 0x811c9dc5
  for (let i = 0; i < input.length; i++) {
    h ^= input.charCodeAt(i)
    h = Math.imul(h, 0x01000193) >>> 0
  }
  return h.toString(16).padStart(8, '0').slice(0, NAME_HASH_LEN)
}

// 现在这个 name 还是不是界面自己填的？
// 这是「切换 Kind 之后名字跟不跟着走」的全部依据：
// 用户自己打的名字永远不动（nameAuto = false），自动填的跟着 kind 重算。
// 早先没有这个标记，只看 name 空不空，于是自动填的那个名字跟用户打的
// 名字长得一模一样，分不出来 —— 结果切了 Kind 名字还停在旧的那条上。
const nameAuto = ref(true)

const taken = computed(() => props.takenNames ?? [])

/** 名字撞了没有。比对时忽略大小写和首尾空白，
 *  否则「BAT-3F9A2C」和「bat-3f9a2c」在列表里几乎分不出来，却能各建一条。 */
function nameTaken(candidate: string, ignoreId: string | null): boolean {
  const key = candidate.trim().toLowerCase()
  if (key === '') return false
  return taken.value.some(
    (entry) => entry.id !== ignoreId && entry.name.trim().toLowerCase() === key,
  )
}

function autoName(): string {
  // 带上时间戳和随机数：同一毫秒里连着建两条，kind 一样的话
  // 光拿 kind 去哈希会撞出同一个名字。
  const seed = `${form.kind}:${Date.now()}:${Math.random()}`
  return `${form.kind}-${shortHash(seed)}`
}

// 自动生成的名字在撞名时重掷一次。还在撞就照原样返回 ——
// 6 位十六进制有 1600 多万种，连着撞两次不可能，
// 真撞了让用户在输入框里改个字就行，不在这儿跟他较劲。
function uniqueAutoName(): string {
  let name = autoName()
  for (let i = 0; i < 5 && nameTaken(name, props.session?.config.id ?? null); i++) {
    name = autoName()
  }
  return name
}

function fillAutoName() {
  form.name = uniqueAutoName()
  nameAuto.value = true
}

// kind 变了、名字还是自动填的 → 重算，让前缀跟着新的 kind 走。
// 用户打过的名字一个字都不动。
watch(
  () => form.kind,
  () => {
    if (nameAuto.value) fillAutoName()
  },
)

// —— 文件选择框 ——
// 走 @wailsio/runtime 的 Dialogs：它是 runtime 自带的 RPC 通道，不经 bindings，
// 所以不需要后端加任何方法。窗口由框架自动挂上，这里不用传。
// shell 没有对应的文件类型（它的 target 是命令），所以不给按钮。
const PICK_FILTERS: Record<string, string> = {
  bat: '*.bat',
  cmd: '*.cmd',
  ps1: '*.ps1',
  exe: '*.exe;*.com',
  // terminal-cmd 的 target 是**脚本路径**（外面会被后端包上引号），
  // 所以给的是脚本后缀。
  'terminal-cmd': '*.bat;*.cmd',
  // terminal-powershell 的 target 严格说是**命令**（-Command "%s"），
  // 但挑一个脚本让 PowerShell 跑是最常用的那个用法，所以给脚本后缀而不是不给。
  'terminal-powershell': '*.ps1;*.psm1;*.bat;*.cmd',
  // terminal-shell 不给：它的 target 是一条命令，没有「文件」可挑。
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
    reportDialogFailure(error)
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
    reportDialogFailure(error)
  }
}

// 系统文件对话框 reject 的绝大多数情况是**用户点了「取消」**，不是失败：
// `IFileDialog::Show` 取消时返回 HRESULT 0x800704C7（= ERROR_CANCELLED），
// Wails 原样往上抛（`internal/go-common-file-dialog/cfd/vtblCommonFunc.go` 的
// `hresultToError` → `ole.NewError`），`PromptForSingleSelection` 也只是 `return "", err`。
//
// 弹 toast 骂用户「打开文件失败」是错的。真正打不开对话框（COM 挂了）几乎不会发生，
// 所以这里一律不打扰用户，只记进 console。
// ⚠️ 这是「不区分」的处理：真失败也一起吞了。要精确区分得在 Go 那边接
// `application.Get().Dialog.OpenFile().PromptForSingleSelection()`，
// 用 `errors.As` 取 `*ole.OleError` 比 `Code() == 0x800704C7`，取消就返回 `("", nil)`。
function reportDialogFailure(error: unknown) {
  console.warn('[pick] file dialog rejected:', error)
}

const namePlaceholder = computed(() =>
  isShell.value ? t('form.namePlaceholder') : t('form.nameAutoHint'),
)

const targetPlaceholder = computed(() => {
  if (form.kind === 'terminal-cmd') return t('form.targetCmdPathPlaceholder')
  if (isTerminal.value) return t('form.targetTerminalPlaceholder')
  return isShell.value ? t('form.targetShellPlaceholder') : t('form.targetPlaceholder')
})

function toDataStore(): DataStore {
  return {
    id: form.id,
    name: form.name,
    // kind 和 target 都是后端认的东西，原样存。前端不再替后端拼命令行。
    kind: form.kind,
    target: form.target.trim(),
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
    // 编辑时必须把原来的顺序号原样带回去：`updateSession` 走的是 Store.Update，
    // 那是**整个替换**不是合并，这里填 0 会把这条会话的侧栏位置抹掉。
    // 新建填 0 无所谓——后端按 ID 排的兜底分支会接管。
    sortOrder: props.session?.config.sortOrder ?? 0,
  }
}

const saving = ref(false)

function validate(): boolean {
  // 名字：空、重名，都不让存。重名比对时把自己排除掉 ——
  // 编辑一条会话时它自己就躺在 takenNames 里，不排除的话永远重名。
  errors.name =
    form.name.trim() === ''
      ? t('form.nameRequired')
      : nameTaken(form.name, props.session?.config.id ?? null)
        ? t('form.nameTaken', { name: form.name.trim() })
        : ''
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
  <!-- 720：Kind 那一排八个选项在 560 宽里排不下（最后一个 terminal(powershell)
       会被切掉，底部还多一条横向滚动条）。 -->
  <UDialog v-model="visible" :title="title" width="720px">
    <div class="flex flex-col gap-3.5">
      <!-- 标签在上、控件在下：同一列里对齐，两列并排时读起来是一条一条的 -->
      <div>
        <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
          {{ t('form.name') }}
          <span class="text-neg">*</span>
        </label>
        <UInput
          v-model="form.name"
          :placeholder="namePlaceholder"
          :invalid="!!errors.name"
          @input="nameAuto = false"
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

      <!-- 常驻终端的 target 是选填的：不填 = 空终端，填了 = 先跑它再停在提示符。
           所以这里不标红星，提示语也跟脚本类不一样。 -->
      <div>
        <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
          {{ t('form.target') }}
          <span v-if="!isTerminal" class="text-neg">*</span>
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
        <p v-else-if="isTerminal" class="mt-1 text-[11px] text-ink-faint">
          {{ form.kind === 'terminal-cmd' ? t('form.targetCmdPathHint') : t('form.targetTerminalHint') }}
        </p>
      </div>

      <!-- args 对常驻终端先藏着：它会被塞进 target 外面那层引号里，
           命令里本来就有引号的话套起来是什么样没验证过，宁可不给这个入口。 -->
      <div v-if="!isTerminal">
        <label class="mb-1.5 block text-[11px] font-medium tracking-wide text-ink-dim">
          {{ t('form.args') }}
        </label>
        <UInput v-model="form.args" :placeholder="t('form.argsPlaceholder')" mono />
      </div>

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
