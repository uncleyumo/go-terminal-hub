<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElConfigProvider, ElMessage, ElMessageBox } from 'element-plus'
import elementEn from 'element-plus/es/locale/lang/en'
import elementZhCn from 'element-plus/es/locale/lang/zh-cn'
import { Refresh } from '@element-plus/icons-vue'
import SessionList from './components/SessionList.vue'
import SessionForm from './components/SessionForm.vue'
import { deleteSession, getSettings, listSessions, saveSettings, type SessionView } from './api'
import { normalizeLocale, setLocale, type AppLocale } from './i18n'

const { t } = useI18n()

const sessions = ref<SessionView[]>([])
const selectedId = ref<string | null>(null)
const formVisible = ref(false)
const editing = ref<SessionView | null>(null)

// 后端会把它存的那份原样回给我们，包括我们不认识的字段，这里保持整份往回写。
const settings = ref({ language: 'en', theme: 'light' })
const locale = ref<AppLocale>('en')

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
    settings.value = { language: stored.language, theme: stored.theme }
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
    await saveSettings({ language: value, theme: settings.value.theme })
    settings.value.language = value
  } catch (error) {
    ElMessage.error(`${t('msg.settingFailed')}: ${String(error)}`)
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

onMounted(async () => {
  await loadSettings()
  await load()
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

        <el-button :icon="Refresh" size="small" @click="load">{{ t('app.refresh') }}</el-button>
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

          <div v-else class="info">
            <div class="info-title">{{ selected.config.name }}</div>
            <el-descriptions :column="1" border size="small">
              <el-descriptions-item :label="t('detail.status')">
                <span v-if="selected.running">{{ t('list.running') }}</span>
                <span v-else-if="selected.status">
                  {{ t('list.stopped') }} ·
                  {{ t('list.exitCode', { code: selected.status.exitCode }) }}
                </span>
                <span v-else>{{ t('list.stopped') }}</span>
              </el-descriptions-item>
              <el-descriptions-item :label="t('detail.kind')">{{ selected.config.kind }}</el-descriptions-item>
              <el-descriptions-item :label="t('detail.target')">{{ selected.config.target }}</el-descriptions-item>
              <el-descriptions-item :label="t('detail.args')">
                {{ selected.config.args || t('detail.none') }}
              </el-descriptions-item>
              <el-descriptions-item :label="t('detail.workDir')">
                {{ selected.config.workDir || t('detail.none') }}
              </el-descriptions-item>
              <el-descriptions-item :label="t('detail.mode')">
                {{ selected.config.mode }} / {{ selected.config.encoding }}
              </el-descriptions-item>
              <el-descriptions-item :label="t('detail.size')">
                {{ selected.config.cols }} × {{ selected.config.rows }}
              </el-descriptions-item>
              <el-descriptions-item :label="t('detail.autoStart')">
                {{ selected.config.autoStart ? '✓' : '—' }}
              </el-descriptions-item>
            </el-descriptions>

            <div class="placeholder box">{{ t('detail.placeholder') }}</div>
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

.main {
  min-height: 0;
}

.detail {
  padding: 16px;
}

.placeholder {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.placeholder.box {
  margin-top: 16px;
  padding: 32px;
  text-align: center;
  border: 1px dashed var(--el-border-color);
  border-radius: 4px;
}

.info-title {
  margin-bottom: 12px;
  font-size: 15px;
  font-weight: 600;
}
</style>
