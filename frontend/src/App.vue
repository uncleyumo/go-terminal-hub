<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElConfigProvider, ElMessage, ElMessageBox } from 'element-plus'
import elementEn from 'element-plus/es/locale/lang/en'
import elementZhCn from 'element-plus/es/locale/lang/zh-cn'
import { Refresh, RefreshRight, SwitchButton, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import SessionList from './components/SessionList.vue'
import SessionForm from './components/SessionForm.vue'
import TerminalPane from './components/TerminalPane.vue'
import {
  deleteSession,
  getSettings,
  listSessions,
  onSessionExited,
  onSessionOutput,
  onSessionStarted,
  quitApp,
  restartSession,
  saveSettings,
  setStartOnBoot,
  startSession,
  stopAllSessions,
  stopSession,
  type Settings,
  type SessionView,
} from './api'
import { clear as clearTerminal, disposeAll, fit as fitTerminal, write as writeTerminal } from './terminal/manager'
import { normalizeLocale, setLocale, type AppLocale } from './i18n'

const { t } = useI18n()

const sessions = ref<SessionView[]>([])
const selectedId = ref<string | null>(null)
const formVisible = ref(false)
const editing = ref<SessionView | null>(null)

// 后端会把它存的那份原样回给我们，包括我们不认识的字段，这里保持整份往回写。
const settings = ref<Settings>({ language: 'en', theme: 'light', startOnBoot: false })
const locale = ref<AppLocale>('en')
const startOnBootBusy = ref(false)

const elLocale = computed(() => (locale.value === 'zh-CN' ? elementZhCn : elementEn))

const selected = computed(() => sessions.value.find((s) => s.config.id === selectedId.value) ?? null)

async function load() {
  try {
    sessions.value = await listSessions()
    if (selectedId.value && !sessions.value.some((s) => s.config.id === selectedId.value)) {
      selectedId.value = null
    }
  } catch (error) {
    ElMessage.error(`${t('msg.loadFailed')}: ${String(error)}`)
  }
}

async function loadSettings() {
  try {
    const stored = await getSettings()
    // 整份存下来，不挑字段 —— 挑漏了哪个，往回写的时候就会把那个字段抹成零值
    settings.value = stored
    // 值为空串（文件里没写 / 后端没兜默认值）时回落到 en
    locale.value = normalizeLocale(stored.language)
    setLocale(locale.value)
  } catch (error) {
    ElMessage.error(String(error))
  }
}

async function changeLocale(value: AppLocale) {
  locale.value = value
  setLocale(value)
  try {
    await saveSettings({ ...settings.value, language: value })
    settings.value.language = value
  } catch (error) {
    ElMessage.error(`${t('msg.settingFailed')}: ${String(error)}`)
  }
}

// 开关的显示值直接读 settings，所以这里先乐观改一次、失败了再回滚 ——
// 不然要等一次来回，手指点下去开关纹丝不动。
// 后端 SetStartOnBoot 会同时写 settings.json 和注册表，前端不再调 saveSettings。
async function toggleStartOnBoot(value: string | number | boolean) {
  const next = value === true
  const previous = settings.value.startOnBoot
  settings.value.startOnBoot = next
  startOnBootBusy.value = true
  try {
    await setStartOnBoot(next)
  } catch (error) {
    settings.value.startOnBoot = previous
    ElMessage.error(`${t('msg.settingFailed')}: ${String(error)}`)
  } finally {
    startOnBootBusy.value = false
  }
}

function openCreate() {
  editing.value = null
  formVisible.value = true
}

function openEdit(session: SessionView) {
  editing.value = session
  formVisible.value = true
}

async function remove(session: SessionView) {
  try {
    await ElMessageBox.confirm(t('msg.removeConfirm'), session.config.name, { type: 'warning' })
  } catch {
    return
  }
  try {
    await deleteSession(session.config.id)
    ElMessage.success(t('msg.removed'))
    if (selectedId.value === session.config.id) selectedId.value = null
    await load()
  } catch (error) {
    ElMessage.error(`${t('msg.removeFailed')}: ${String(error)}`)
  }
}

// —— 启停 ——
// 起新一轮之前先清屏：不然上一轮的输出和新一轮的混在一起，分不清哪句是哪次跑出来的。

async function start(session: SessionView) {
  clearTerminal(session.config.id)
  try {
    await startSession(session.config.id)
  } catch (error) {
    ElMessage.error(`${t('msg.startFailed')}: ${String(error)}`)
  }
  await load()
}

async function stop(session: SessionView) {
  try {
    await stopSession(session.config.id)
  } catch (error) {
    ElMessage.error(`${t('msg.stopFailed')}: ${String(error)}`)
  }
  await load()
}

async function restart(session: SessionView) {
  clearTerminal(session.config.id)
  try {
    await restartSession(session.config.id)
  } catch (error) {
    ElMessage.error(`${t('msg.startFailed')}: ${String(error)}`)
  }
  await load()
}

// —— 退出（D27 / D28）——
// 判断全在前端：还有在跑的就弹框问一句，没有就直接退。
// 停会话由后端并发做完（Hub.StopAllSessions），前端只等它返回。
async function quit() {
  const running = sessions.value.filter((s) => s.running)

  if (running.length > 0) {
    const names = running.map((s) => s.config.name || s.config.id).join(', ')
    try {
      await ElMessageBox.confirm(
        t('msg.quitRunning', { n: running.length, names }),
        t('msg.quitTitle'),
        {
          type: 'warning',
          confirmButtonText: t('msg.stopAllAndQuit'),
          cancelButtonText: t('action.cancel'),
        },
      )
    } catch {
      return // 取消 —— 什么都不做
    }

    try {
      await stopAllSessions()
    } catch (error) {
      // 已知：会话恰好在这两步之间自己退出了，hub 会把它当成失败报回来。
      // 那正是我们想要的结果，所以只记一笔，不拦住退出。
      console.warn('stopAllSessions reported an error, quitting anyway:', error)
    }
  }

  await quitApp()
}

// 订阅必须在任何启动动作之前 —— 事件不补发，订阅之前产生的输出收不到。
let unsubscribers: Array<() => void> = []

// —— 开机自启 ——
// 只能放在三个订阅之后：事件不补发，早于订阅启动的会话，它的输出前端一句都收不到。
// 跳过已在跑的：开发时前端热重载会让 onMounted 再跑一次，那时会话还在内存里，
// 重复启动会撞上 hub 的同 ID 检查报错。
async function runAutoStart() {
  const targets = sessions.value.filter((s) => s.config.autoStart && !s.running)
  for (const session of targets) {
    try {
      await startSession(session.config.id)
    } catch (error) {
      ElMessage.error(`${t('msg.startFailed')}: ${String(error)}`)
    }
  }
}

onMounted(async () => {
  unsubscribers = [
    onSessionOutput((payload) => writeTerminal(payload.id, payload.text)),
    onSessionStarted((id) => {
      void load()
      // ConPTY 是这一刻才建出来的 —— 之前 attach 时量到的那些尺寸它一句都收不到。
      fitTerminal(id)
    }),
    onSessionExited((payload) => {
      writeTerminal(payload.id, `\r\n${t('term.exited', { code: payload.code })}\r\n`)
      if (payload.errMsg) writeTerminal(payload.id, `${payload.errMsg}\r\n`)
      void load()
    }),
  ]

  await loadSettings()
  await load()
  await runAutoStart()
})

onBeforeUnmount(() => {
  for (const off of unsubscribers) off()
  unsubscribers = []
  disposeAll()
})
</script>

<template>
  <ElConfigProvider :locale="elLocale">
    <el-container class="root">
      <el-header class="header" height="48px">
        <span class="brand">{{ t('app.title') }}</span>
        <div class="spacer"></div>

        <el-select
          :model-value="locale"
          class="locale"
          size="small"
          @update:model-value="changeLocale"
        >
          <el-option label="English" value="en" />
          <el-option label="简体中文" value="zh-CN" />
        </el-select>

        <el-tooltip :content="t('app.startOnBootHint')" placement="bottom">
          <span class="start-on-boot">
            <span>{{ t('app.startOnBoot') }}</span>
            <el-switch
              :model-value="settings.startOnBoot"
              :loading="startOnBootBusy"
              size="small"
              @change="toggleStartOnBoot"
            />
          </span>
        </el-tooltip>

        <el-button :icon="Refresh" size="small" @click="load">{{ t('app.refresh') }}</el-button>
        <el-button :icon="SwitchButton" size="small" @click="quit">{{ t('app.quit') }}</el-button>
      </el-header>

      <el-container class="main">
        <el-aside width="260px">
          <SessionList
            :sessions="sessions"
            :selected-id="selectedId"
            @select="selectedId = $event"
            @create="openCreate"
            @edit="openEdit"
            @remove="remove"
          />
        </el-aside>

        <el-main class="detail">
          <div v-if="!selected" class="placeholder">{{ t('detail.empty') }}</div>

          <div v-else class="pane-wrap">
            <div class="toolbar">
              <span class="info-title">{{ selected.config.name || selected.config.id }}</span>
              <span class="status">
                <span class="dot" :class="selected.running ? 'on' : 'off'"></span>
                <span v-if="selected.running">{{ t('list.running') }}</span>
                <span v-else-if="selected.status">
                  {{ t('list.stopped') }} · {{ t('list.exitCode', { code: selected.status.exitCode }) }}
                </span>
                <span v-else>{{ t('list.stopped') }}</span>
              </span>

              <div class="spacer"></div>

              <el-button
                type="primary"
                size="small"
                :icon="VideoPlay"
                :disabled="selected.running"
                @click="start(selected)"
              >
                {{ t('action.start') }}
              </el-button>
              <el-button
                size="small"
                :icon="VideoPause"
                :disabled="!selected.running"
                @click="stop(selected)"
              >
                {{ t('action.stop') }}
              </el-button>
              <el-button
                size="small"
                :icon="RefreshRight"
                :disabled="selected.running"
                @click="restart(selected)"
              >
                {{ t('action.restart') }}
              </el-button>
            </div>

            <el-collapse class="config">
              <el-collapse-item :title="t('detail.config')" name="config">
                <el-descriptions :column="2" border size="small">
                  <el-descriptions-item :label="t('detail.kind')">{{ selected.config.kind }}</el-descriptions-item>
                  <el-descriptions-item :label="t('detail.mode')">
                    {{ selected.config.mode }} / {{ selected.config.encoding }}
                  </el-descriptions-item>
                  <el-descriptions-item :label="t('detail.target')" :span="2">
                    {{ selected.config.target }}
                  </el-descriptions-item>
                  <el-descriptions-item :label="t('detail.args')" :span="2">
                    {{ selected.config.args || t('detail.none') }}
                  </el-descriptions-item>
                  <el-descriptions-item :label="t('detail.workDir')" :span="2">
                    {{ selected.config.workDir || t('detail.none') }}
                  </el-descriptions-item>
                  <el-descriptions-item :label="t('detail.size')">
                    {{ selected.config.cols }} × {{ selected.config.rows }}
                  </el-descriptions-item>
                  <el-descriptions-item :label="t('detail.autoStart')">
                    {{ selected.config.autoStart ? '✓' : '—' }}
                  </el-descriptions-item>
                </el-descriptions>
              </el-collapse-item>
            </el-collapse>

            <!-- key 换行时会重新挂载：manager 里旧实例留着，容器换到新的 host 上 -->
            <TerminalPane :key="selected.config.id" :session-id="selected.config.id" />
          </div>
        </el-main>
      </el-container>
    </el-container>

    <SessionForm v-model="formVisible" :session="editing" @saved="load" />
  </ElConfigProvider>
</template>

<style scoped>
.root {
  height: 100vh;
}

.header {
  display: flex;
  align-items: center;
  gap: 8px;
  border-bottom: 1px solid var(--el-border-color);
}

.brand {
  font-weight: 600;
}

.spacer {
  flex: 1;
}

.locale {
  width: 120px;
}

.start-on-boot {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.main {
  min-height: 0;
}

.detail {
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 0;
}

.placeholder {
  padding: 16px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.pane-wrap {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
}

.toolbar {
  display: flex;
  flex: none;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--el-border-color);
}

.info-title {
  font-size: 14px;
  font-weight: 600;
}

.status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.dot.on {
  background: var(--el-color-success);
}

.dot.off {
  background: var(--el-text-color-disabled);
}

/* 配置默认收起来 —— 主要看的是终端 */
.config {
  flex: none;
  padding: 0 12px;
  border-bottom: 1px solid var(--el-border-color);
}

.config :deep(.el-collapse-item__header) {
  height: 34px;
  font-size: 12px;
}

.config :deep(.el-collapse-item__wrap) {
  padding-bottom: 8px;
}
</style>
