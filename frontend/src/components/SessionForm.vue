<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { FolderOpened } from '@element-plus/icons-vue'
import { Dialogs } from '@wailsio/runtime'
import { createSession, updateSession, type DataStore, type SessionView } from '../api'

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

const KINDS = ['bat', 'cmd', 'ps1', 'exe', 'shell']
const MODES = ['terminal', 'log']
const ENCODINGS = ['auto', 'utf8', 'gbk']

const formRef = ref()

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

const form = reactive<FormState>(blank())

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

// 每次打开都把上一次的残留清掉：编辑时灌入这条配置，新建时回到默认值。
watch(visible, (open) => {
  if (!open) return
  const source = props.session?.config
  if (!source) {
    Object.assign(form, blank())
    return
  }
  Object.assign(form, {
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
  })
})

// shell 的 target 是一整条命令，不是一个文件路径，没有文件名可以拿来兜底命名，
// 所以名称只在 shell 下必填；其余 kind 留空由下面自动生成。
const isShell = computed(() => form.kind === 'shell')

const rules = computed(() => ({
  name: isShell.value
    ? [{ required: true, message: () => t('form.nameRequired'), trigger: 'blur' }]
    : [],
  target: [{ required: true, message: () => t('form.targetRequired'), trigger: 'blur' }],
}))

// —— 名称自动生成 ——
// 格式「脚本文件名(创建时间)」，例 xxx.exe(2026-09-27 21:30)。
// 时间取生成这一刻，不是脚本文件自己的时间戳。
function pad(n: number): string {
  return String(n).padStart(2, '0')
}

// target 可能是反斜杠的 Windows 路径，也可能是正斜杠，两种分隔符都要认。
function scriptName(target: string): string {
  const parts = target.split(/[\\/]/)
  return parts[parts.length - 1] || target
}

function autoName(target: string): string {
  const now = new Date()
  const date = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
  const time = `${pad(now.getHours())}:${pad(now.getMinutes())}`
  return `${scriptName(target)}(${date} ${time})`
}

// 名称还是空的、且不是 shell 时自动补上；绝不覆盖用户已经写下的名字。
watch([() => form.target, () => form.kind], () => {
  if (isShell.value || form.name.trim() !== '' || form.target.trim() === '') return
  form.name = autoName(form.target)
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
    ElMessage.error(`${t('form.pickFailed')}: ${String(error)}`)
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
    kind: form.kind,
    target: form.target,
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

async function submit() {
  // 兜底：用户手打了路径、没走「浏览」按钮，名称一直是空的。
  if (!isShell.value && form.name.trim() === '' && form.target.trim() !== '') {
    form.name = autoName(form.target)
  }
  if (formRef.value) {
    const ok = await formRef.value.validate().catch(() => false)
    if (!ok) return
  }
  saving.value = true
  try {
    if (isEdit.value) {
      await updateSession(form.id, toDataStore())
      ElMessage.success(t('msg.updated'))
    } else {
      // ID 由后端生成（UUIDv7），这里不传
      await createSession(toDataStore())
      ElMessage.success(t('msg.created'))
    }
    visible.value = false
    emit('saved')
  } catch (error) {
    ElMessage.error(String(error))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <el-dialog v-model="visible" :title="title" width="560px" append-to-body>
    <el-form
      ref="formRef"
      class="form-body"
      :model="form"
      :rules="rules"
      label-width="120px"
      label-position="left"
    >
      <el-form-item :label="t('form.name')" prop="name">
        <el-input v-model="form.name" :placeholder="namePlaceholder" />
      </el-form-item>

      <el-form-item :label="t('form.kind')" prop="kind">
        <el-radio-group v-model="form.kind">
          <el-radio-button v-for="kind in KINDS" :key="kind" :value="kind">
            {{ t(`kind.${kind}`) }}
          </el-radio-button>
        </el-radio-group>
      </el-form-item>

      <el-form-item :label="t('form.target')" prop="target">
        <el-input v-model="form.target" :placeholder="targetPlaceholder">
          <template v-if="canPick" #append>
            <el-button :icon="FolderOpened" @click="pickFile">{{ t('form.browse') }}</el-button>
          </template>
        </el-input>
      </el-form-item>

      <el-form-item :label="t('form.args')" prop="args">
        <el-input v-model="form.args" :placeholder="t('form.argsPlaceholder')" />
      </el-form-item>

      <el-form-item :label="t('form.workDir')" prop="workDir">
        <el-input v-model="form.workDir" :placeholder="t('form.workDirPlaceholder')" />
      </el-form-item>

      <el-form-item :label="t('form.env')" prop="envText">
        <el-input
          v-model="form.envText"
          type="textarea"
          :rows="3"
          :placeholder="t('form.envPlaceholder')"
        />
      </el-form-item>

      <el-form-item :label="t('form.mode')" prop="mode">
        <el-radio-group v-model="form.mode">
          <el-radio-button v-for="mode in MODES" :key="mode" :value="mode">
            {{ t(`mode.${mode}`) }}
          </el-radio-button>
        </el-radio-group>
        <el-select v-model="form.encoding" class="encoding">
          <el-option v-for="encoding in ENCODINGS" :key="encoding" :label="encoding" :value="encoding" />
        </el-select>
        <span class="hint">{{ t('form.encodingHint') }}</span>
      </el-form-item>

      <el-form-item :label="t('form.cols')" prop="cols">
        <el-input-number v-model="form.cols" :min="1" :max="1000" controls-position="right" />
      </el-form-item>

      <el-form-item :label="t('form.rows')" prop="rows">
        <el-input-number v-model="form.rows" :min="1" :max="1000" controls-position="right" />
      </el-form-item>

      <el-form-item :label="t('form.autoStart')" prop="autoStart">
        <el-switch v-model="form.autoStart" />
        <span class="hint">{{ t('form.autoStartHint') }}</span>
      </el-form-item>
    </el-form>

    <template #footer>
      <el-button @click="visible = false">{{ t('action.cancel') }}</el-button>
      <el-button type="primary" :loading="saving" @click="submit">
        {{ isEdit ? t('action.save') : t('action.create') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
/*
 * 表单竖着堆 11 项，窗口一矮就顶到底边（el-dialog 自己是 fixed 定位，不滚动）。
 * 给表单体一个上限高度、超出就滚，弹框的标题和按钮永远在视口里。
 * 上限用 vh 而不是写死 px —— 窗口越矮，能分给它的就越少。
 */
.form-body {
  max-height: 60vh;
  overflow-y: auto;
  padding-right: 6px;
}

.encoding {
  width: 100px;
  margin-left: 12px;
}

.hint {
  margin-left: 8px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
