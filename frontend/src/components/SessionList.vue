<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import UButton from './ui/UButton.vue'
import UIcon from './ui/UIcon.vue'
import UTooltip from './ui/UTooltip.vue'
import type { SessionView } from '../api'

const props = defineProps<{
  sessions: SessionView[]
  selectedId: string | null
  /** 顶栏搜索框传进来的过滤词，空串 = 不过滤 */
  query: string
}>()

const emit = defineEmits<{
  select: [id: string]
  create: []
  edit: [session: SessionView]
  remove: [session: SessionView]
  toggleSidebar: []
}>()

const { t } = useI18n()

// 名称、目标路径、类型都拿来匹配 —— 用户想找的往往是他记得的那条路径，不是记得名字。
const filtered = computed(() => {
  const q = props.query.trim().toLowerCase()
  if (q === '') return props.sessions
  return props.sessions.filter((s) =>
    [s.config.name, s.config.target, s.config.kind, s.config.args]
      .filter(Boolean)
      .some((field) => String(field).toLowerCase().includes(q)),
  )
})

function label(session: SessionView): string {
  const name = session.config.name || session.config.id
  // 状态不能只靠那颗点传达（读屏看不见颜色），所以进 aria-label
  return session.running ? `${name} · ${t('list.running')}` : name
}

// kind 直接印磁盘上的值：terminal-cmd / terminal-powershell 是后端认的真类型
// （executor.go 的 KindWhiteList），不用前端再翻译回去。
function kindLabel(session: SessionView): string {
  return t(`kind.${session.config.kind}`)
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col bg-sunken">
    <div class="flex flex-none items-center gap-2 px-3 py-2.5">
      <span class="text-[11px] font-semibold tracking-wider text-ink-faint uppercase">
        {{ t('list.title') }}
      </span>
      <span
        class="rounded-full bg-line px-1.5 py-px text-[10px] leading-4 font-medium text-ink-dim tabular-nums"
      >
        {{ query.trim() ? `${filtered.length}/${sessions.length}` : sessions.length }}
      </span>
      <div class="flex-1"></div>
      <!-- 折叠按钮放在这一行，而不是顶栏：顶栏那个 268px 的盒子一旦被按钮占掉一块，
           搜索框就被推着往右挪，跟下面这列对不齐了（2026-09-29 学习者指出）。 -->
      <UTooltip :content="t('app.hideSessions')">
        <UButton
          variant="ghost"
          size="sm"
          square
          icon="panelLeft"
          :aria-label="t('app.hideSessions')"
          :aria-expanded="true"
          @click="emit('toggleSidebar')"
        />
      </UTooltip>
      <UTooltip :content="t('list.create')">
        <UButton variant="ghost" size="sm" square icon="plus" @click="emit('create')" />
      </UTooltip>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
      <div v-if="sessions.length === 0" class="px-2 py-8 text-center text-xs text-ink-faint">
        {{ t('list.empty') }}
      </div>
      <div v-else-if="filtered.length === 0" class="px-2 py-8 text-center text-xs text-ink-faint">
        {{ t('list.noMatch', { q: query.trim() }) }}
      </div>

      <!-- 行用 div 而不是 button：里面还嵌着两个真按钮，button 套 button 是非法结构，
           浏览器的 HTML 解析器会把内层那个提前关掉，点了没反应 -->
      <div
        v-for="session in filtered"
        :key="session.config.id"
        tabindex="0"
        role="option"
        :aria-selected="session.config.id === selectedId"
        :aria-label="label(session)"
        class="group mb-0.5 flex w-full cursor-pointer items-center gap-2.5 rounded-lg py-2 pr-1.5 pl-2.5 text-left transition-colors duration-100 focus-visible:outline-2 focus-visible:outline-accent"
        :class="
          session.config.id === selectedId
            ? 'bg-accent-soft text-ink'
            : 'text-ink-dim hover:bg-raised/60 hover:text-ink'
        "
        @click="emit('select', session.config.id)"
        @keydown.enter="emit('select', session.config.id)"
      >
        <!-- 状态点：颜色之外还有 aria-label，不靠颜色单独传达信息 -->
        <span
          class="h-1.5 w-1.5 shrink-0 rounded-full transition-colors"
          :class="session.running ? 'bg-pos' : 'bg-line-strong'"
        />

        <span class="min-w-0 flex-1">
          <span class="block truncate text-[13px] leading-4.5 font-medium">
            {{ session.config.name || session.config.id }}
          </span>
          <span class="mt-0.5 flex items-center gap-1 text-[10.5px] leading-4 text-ink-faint">
            <span class="font-mono">{{ kindLabel(session) }}</span>
            <template v-if="!session.running && session.status">
              <span>·</span>
              <span class="tabular-nums">
                {{ t('list.exitCode', { code: session.status.exitCode }) }}
              </span>
            </template>
          </span>
        </span>

        <!-- 操作按钮平时收起来，鼠标进这一行才出现；收起用 opacity 不占位，列表宽度不会跳 -->
        <span
          class="flex flex-none items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100"
          :class="session.config.id === selectedId && 'opacity-100'"
          @click.stop
        >
          <UButton
            variant="ghost"
            size="sm"
            square
            icon="edit"
            :aria-label="t('action.edit')"
            @click="emit('edit', session)"
          />
          <!-- 运行中不给删（D23）：后端那句 session is still running 只是兜底 -->
          <UTooltip v-if="session.running" :content="t('msg.removeRunning')">
            <UButton
              variant="ghost"
              size="sm"
              square
              icon="trash"
              disabled
              :aria-label="t('action.remove')"
            />
          </UTooltip>
          <UButton
            v-else
            variant="ghost"
            size="sm"
            square
            icon="trash"
            class="hover:text-neg"
            :aria-label="t('action.remove')"
            @click="emit('remove', session)"
          />
        </span>
      </div>
    </div>
  </div>
</template>
