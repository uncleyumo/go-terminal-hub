<script setup lang="ts">
// 设置窗口：左栏选一项，右栏只显示那一项。
//
// 顶栏原来挂了九个按钮（语言、开机自启、主题、刷新、退出、远程、关于、仓库…），
// 每加一个功能就再挤一个进去，最后谁也不记得哪个在哪儿。这里按「这件事属于谁」
// 分栏：常用的（主题、刷新）留在顶栏，其余都归到这一扇门后面。
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Settings } from '../api'
import { FEATURES, featureOn } from '../features'
import UIcon from './ui/UIcon.vue'
import UDialog from './ui/UDialog.vue'
import USwitch from './ui/USwitch.vue'

const props = defineProps<{
  modelValue: boolean
  settings: Settings
  locale: string
  themeMode: string
  /** 会话栏展开时也显示标签（只是这台机器上的偏好，不进 settings.json） */
  alwaysTabs: boolean
  startOnBootBusy: boolean
  remoteEnabled: boolean
  remoteState: string
  hasUpdate: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  'update:alwaysTabs': [value: boolean]
  locale: [value: string]
  theme: [value: string]
  startOnBoot: []
  feature: [id: string, on: boolean]
  openRemote: []
  openAbout: []
  openBackground: []
  openRepo: []
  /** 打开更新弹窗。状态和动作都在 App.vue 那边，这里只负责指路。 */
  openUpdate: []
}>()

const { t } = useI18n()

// 「更新」单独一栏：它是「这个 app 现在是不是最新」，跟「这个 app 是谁」不是一件事。
const SECTIONS = ['general', 'appearance', 'remote', 'features', 'update', 'about'] as const
type Section = (typeof SECTIONS)[number]
const active = ref<Section>('general')

const localeOptions = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'en', label: 'English' },
]

const themeOptions = computed(() => [
  { value: 'light', label: t('theme.light'), icon: 'sun' },
  { value: 'dark', label: t('theme.dark'), icon: 'moon' },
  { value: 'system', label: t('theme.system'), icon: 'monitor' },
])

function onFeature(id: string, on: boolean) {
  emit('feature', id, on)
}
</script>

<template>
  <UDialog
    :model-value="modelValue"
    :title="t('app.settings')"
    width="760px"
    flush
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="flex h-[520px]">
      <nav class="flex w-[168px] flex-none flex-col gap-0.5 border-r border-line bg-sunken p-2">
        <button
          v-for="s in SECTIONS"
          :key="s"
          type="button"
          class="rounded-md px-2.5 py-1.5 text-left text-[12.5px] transition-colors duration-100"
          :class="
            active === s
              ? 'bg-raised font-medium text-ink'
              : 'text-ink-dim hover:bg-raised/60 hover:text-ink'
          "
          @click="active = s"
        >
          {{ t(`settings.section.${s}`) }}
        </button>
      </nav>

      <div class="min-w-0 flex-1 overflow-y-auto px-5 py-4">
        <!-- 通用 -->
        <template v-if="active === 'general'">
          <p class="mb-2 text-[11.5px] text-ink-faint">{{ t('settings.generalHint') }}</p>

          <div class="flex items-center gap-3 border-t border-line py-3">
            <div class="min-w-0 flex-1">
              <div class="text-[12.5px] text-ink">{{ t('app.startOnBoot') }}</div>
              <div class="text-[11.5px] text-ink-faint">{{ t('app.startOnBootHint') }}</div>
            </div>
            <USwitch
              :model-value="settings.startOnBoot"
              :disabled="startOnBootBusy"
              :aria-label="t('app.startOnBoot')"
              @update:model-value="emit('startOnBoot')"
            />
          </div>

          <div class="flex items-center gap-3 border-t border-line py-3">
            <div class="min-w-0 flex-1">
              <div class="text-[12.5px] text-ink">{{ t('app.language') }}</div>
              <div class="text-[11.5px] text-ink-faint">{{ t('settings.languageHint') }}</div>
            </div>
            <div class="flex flex-none gap-1">
              <button
                v-for="o in localeOptions"
                :key="o.value"
                type="button"
                class="rounded-md border px-2.5 py-1 text-[12px] transition-colors duration-100"
                :class="
                  locale === o.value
                    ? 'border-accent bg-accent-soft text-ink'
                    : 'border-line text-ink-dim hover:bg-raised'
                "
                @click="emit('locale', o.value)"
              >
                {{ o.label }}
              </button>
            </div>
          </div>
        </template>

        <!-- 外观 -->
        <template v-else-if="active === 'appearance'">
          <p class="mb-2 text-[11.5px] text-ink-faint">{{ t('settings.appearanceHint') }}</p>

          <div class="flex items-center gap-3 border-t border-line py-3">
            <div class="min-w-0 flex-1">
              <div class="text-[12.5px] text-ink">{{ t('theme.label') }}</div>
            </div>
            <div class="flex flex-none gap-1">
              <button
                v-for="o in themeOptions"
                :key="o.value"
                type="button"
                class="flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-[12px] transition-colors duration-100"
                :class="
                  themeMode === o.value
                    ? 'border-accent bg-accent-soft text-ink'
                    : 'border-line text-ink-dim hover:bg-raised'
                "
                @click="emit('theme', o.value)"
              >
                <UIcon :name="o.icon" :size="13" />
                {{ o.label }}
              </button>
            </div>
          </div>

          <div class="flex items-center gap-3 border-t border-line py-3">
            <div class="min-w-0 flex-1">
              <div class="text-[12.5px] text-ink">{{ t('theme.terminalBackground') }}</div>
              <div class="text-[11.5px] text-ink-faint">{{ t('settings.backgroundHint') }}</div>
            </div>
            <button
              type="button"
              class="flex flex-none items-center gap-1.5 rounded-md border border-line px-2.5 py-1 text-[12px] text-ink-dim transition-colors hover:bg-raised hover:text-ink"
              @click="emit('openBackground')"
            >
              <UIcon name="pipette" :size="13" />
              {{ t('settings.open') }}
            </button>
          </div>
        </template>

        <!-- 远程访问 -->
        <template v-else-if="active === 'remote'">
          <p class="mb-2 text-[11.5px] text-ink-faint">{{ t('settings.remoteHint') }}</p>

          <div class="flex items-center gap-3 border-t border-line py-3">
            <span
              class="h-1.5 w-1.5 flex-none rounded-full"
              :class="
                !remoteEnabled ? 'bg-line-strong' : remoteState === 'connected' ? 'bg-pos' : 'bg-warn'
              "
            ></span>
            <div class="min-w-0 flex-1">
              <div class="text-[12.5px] text-ink">{{ t('remote.title') }}</div>
              <div class="text-[11.5px] text-ink-faint">
                {{ remoteEnabled ? t(`remote.status.${remoteState}`) : t('settings.remoteOff') }}
              </div>
            </div>
            <button
              type="button"
              class="flex flex-none items-center gap-1.5 rounded-md border border-line px-2.5 py-1 text-[12px] text-ink-dim transition-colors hover:bg-raised hover:text-ink"
              @click="emit('openRemote')"
            >
              <UIcon name="link" :size="13" />
              {{ t('settings.open') }}
            </button>
          </div>
        </template>

        <!-- 功能开关 -->
        <template v-else-if="active === 'features'">
          <p class="mb-2 text-[11.5px] text-ink-faint">{{ t('settings.featuresHint') }}</p>

          <div v-for="f in FEATURES" :key="f.id" class="border-t border-line">
            <div class="flex items-center gap-3 py-3">
              <div class="min-w-0 flex-1">
                <div class="text-[12.5px] text-ink">{{ t(`feature.${f.id}.name`) }}</div>
                <div class="text-[11.5px] text-ink-faint">{{ t(`feature.${f.id}.desc`) }}</div>
              </div>
              <USwitch
                :model-value="featureOn(settings, f.id)"
                :aria-label="t(`feature.${f.id}.name`)"
                @update:model-value="onFeature(f.id, $event)"
              />
            </div>

            <div
              v-if="f.id === 'sessionTabs' && featureOn(settings, f.id)"
              class="flex items-center gap-3 border-t border-line/60 py-3 pl-3"
            >
              <div class="min-w-0 flex-1">
                <div class="text-[12px] text-ink-dim">{{ t('feature.sessionTabs.always') }}</div>
              </div>
              <USwitch
                :model-value="alwaysTabs"
                :aria-label="t('feature.sessionTabs.always')"
                @update:model-value="emit('update:alwaysTabs', $event)"
              />
            </div>
          </div>
        </template>

        <!-- 更新 -->
        <template v-else-if="active === 'update'">
          <p class="mb-2 text-[11.5px] text-ink-faint">{{ t('settings.updateHint') }}</p>

          <div class="flex items-center gap-3 border-t border-line py-3">
            <div class="min-w-0 flex-1">
              <div class="text-[12.5px] text-ink">{{ t('update.title') }}</div>
              <div class="text-[11.5px] text-ink-faint">
                {{ hasUpdate ? t('settings.updateAvailable') : t('settings.updateIdle') }}
              </div>
            </div>
            <button
              type="button"
              class="flex flex-none items-center gap-1.5 rounded-md border border-line px-2.5 py-1 text-[12px] text-ink-dim transition-colors hover:bg-raised hover:text-ink"
              @click="emit('openUpdate')"
            >
              <UIcon name="refresh" :size="13" />
              {{ t('update.checkButton') }}
            </button>
          </div>
        </template>

        <!-- 关于 -->
        <template v-else>
          <p class="mb-2 text-[11.5px] text-ink-faint">{{ t('settings.aboutHint') }}</p>

          <div class="flex items-center gap-3 border-t border-line py-3">
            <div class="min-w-0 flex-1">
              <div class="text-[12.5px] text-ink">{{ t('app.info') }}</div>
              <div class="text-[11.5px] text-ink-faint">{{ t('settings.infoHint') }}</div>
            </div>
            <button
              type="button"
              class="flex flex-none items-center gap-1.5 rounded-md border border-line px-2.5 py-1 text-[12px] text-ink-dim transition-colors hover:bg-raised hover:text-ink"
              @click="emit('openAbout')"
            >
              <UIcon name="info" :size="13" />
              {{ t('settings.open') }}
            </button>
          </div>

          <div class="flex items-center gap-3 border-t border-line py-3">
            <div class="min-w-0 flex-1">
              <div class="text-[12.5px] text-ink">{{ t('app.repository') }}</div>
            </div>
            <button
              type="button"
              class="flex flex-none items-center gap-1.5 rounded-md border border-line px-2.5 py-1 text-[12px] text-ink-dim transition-colors hover:bg-raised hover:text-ink"
              @click="emit('openRepo')"
            >
              <UIcon name="github" :size="13" />
              {{ t('settings.open') }}
            </button>
          </div>

        </template>
      </div>
    </div>
  </UDialog>
</template>
