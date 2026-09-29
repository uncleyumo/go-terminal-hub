<script setup lang="ts">
import { computed, ref } from 'vue'
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
  /** 拖动排完了，回传**排好的完整 id 列表**（顺序 = 屏幕上从上到下） */
  reorder: [ids: string[]]
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

// —— 拖动排序 ——
// 拖的是原生 HTML5 拖放，不引库：整个交互就是「拖一行、在某一行上放开」，
// 一个库背不动的复杂度反倒要花时间读它的文档。
//
// ⚠️ **搜索词非空时整份列表不许拖**。搜出来的只是全部会话里的一部分，
// 后端 `Store.ReorderSessions` 拿 `len(ids)` 跟 `len(dataList)` 比对，
// 少传一个就直接整条报错 —— 拖一半报个错，比不许拖更糟。
// 这里的 `dndDisabled` 顺手也把 draggable 关掉，鼠标样式也跟着变，
// 用户不会试了半天才发现拖不动。

const dndDisabled = computed(() => props.query.trim() !== '')

const dragId = ref<string | null>(null)
// 拖着的时候，光标落在哪一行上。null = 没落在任何一行上
const dropIndex = ref<number | null>(null)

function onDragStart(session: SessionView, e: DragEvent) {
  if (dndDisabled.value) return
  dragId.value = session.config.id
  // 不给 effectAllowed 的话，Windows 上拖起来是「禁止」那个圈
  if (e.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    // Firefox 不给 dataTransfer 塞点东西就不肯开拖。塞的是 id，不是给谁看的
    e.dataTransfer.setData('text/plain', session.config.id)
  }
}

function onDragOver(index: number, e: DragEvent) {
  if (dragId.value === null) return
  // 不 preventDefault 的话 dragover 不被认，drop 根本不触发
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  dropIndex.value = index
}

function onDragLeave(index: number) {
  // dragleave 在**进入子元素**时也会冒泡上来，不比一下的话，
  // 指示线会在行内乱闪
  if (dropIndex.value === index) dropIndex.value = null
}

function onDrop(index: number) {
  const from = dragId.value
  dragId.value = null
  dropIndex.value = null
  if (from === null) return
  const ids = props.sessions.map((s) => s.config.id)
  const current = ids.indexOf(from)
  if (current === -1) return
  ids.splice(current, 1)
  // 插在第 index 行**之前**。列表长度少了一个，拖到最后一行时 index 会等于长度，
  // splice 越界是安全的，直接传进去就行
  ids.splice(index, 0, from)
  emit('reorder', ids)
}

function onDragEnd() {
  dragId.value = null
  dropIndex.value = null
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
      <!-- 搜索时拖不动，明说为什么：只把 draggable 关掉的话，
           用户会拖了半天发现没反应，只能猜（2026-09-29 学习者的偏好：别让人猜） -->
      <p
        v-if="dndDisabled && filtered.length > 0"
        class="px-2 pt-1 pb-2 text-[10.5px] leading-4 text-ink-faint"
      >
        {{ t('list.reorderHintFiltered') }}
      </p>
      <div v-if="sessions.length === 0" class="px-2 py-8 text-center text-xs text-ink-faint">
        {{ t('list.empty') }}
      </div>
      <div v-else-if="filtered.length === 0" class="px-2 py-8 text-center text-xs text-ink-faint">
        {{ t('list.noMatch', { q: query.trim() }) }}
      </div>

      <!-- 行用 div 而不是 button：里面还嵌着两个真按钮，button 套 button 是非法结构，
           浏览器的 HTML 解析器会把内层那个提前关掉，点了没反应 -->
      <div
        v-for="(session, index) in filtered"
        :key="session.config.id"
        tabindex="0"
        role="option"
        :aria-selected="session.config.id === selectedId"
        :aria-label="label(session)"
        :draggable="!dndDisabled"
        class="group relative mb-0.5 flex w-full items-center gap-2.5 rounded-lg py-2 pr-1.5 pl-2.5 text-left transition-colors duration-100 focus-visible:outline-2 focus-visible:outline-accent"
        :class="[
          session.config.id === selectedId
            ? 'bg-accent-soft text-ink'
            : 'text-ink-dim hover:bg-raised/60 hover:text-ink',
          dndDisabled ? 'cursor-pointer' : 'cursor-grab active:cursor-grabbing',
          // 正在被拖的那行压暗一半。半透明是有意的：留着底色，用户还看得见
          // 自己拖的是哪一条
          dragId === session.config.id && 'opacity-40',
        ]"
        @click="emit('select', session.config.id)"
        @keydown.enter="emit('select', session.config.id)"
        @dragstart="onDragStart(session, $event)"
        @dragover="onDragOver(index, $event)"
        @dragleave="onDragLeave(index)"
        @drop.prevent="onDrop(index)"
        @dragend="onDragEnd"
      >
        <!-- 落点指示线：一条 2px 的横线，画在这一行的**上边**。
             画成绝对定位而不是插一个元素进来，是为了让它的位置只跟行有关，
             不受行内 flex 排版影响 -->
        <span
          v-if="dropIndex === index && dragId !== null"
          class="pointer-events-none absolute inset-x-1.5 -top-px h-0.5 rounded-full bg-accent"
        />
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
