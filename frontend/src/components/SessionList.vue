<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { Delete, Edit, Plus } from '@element-plus/icons-vue'
import type { SessionView } from '../api'

defineProps<{
  sessions: SessionView[]
  selectedId: string | null
}>()

const emit = defineEmits<{
  select: [id: string]
  create: []
  edit: [session: SessionView]
  remove: [session: SessionView]
}>()

const { t } = useI18n()
</script>

<template>
  <div class="session-list">
    <div class="head">
      <span class="title">{{ t('list.title') }}</span>
      <span class="count">{{ sessions.length }}</span>
    </div>

    <el-scrollbar class="body">
      <div v-if="sessions.length === 0" class="empty">{{ t('list.empty') }}</div>

      <div
        v-for="session in sessions"
        :key="session.config.id"
        class="item"
        :class="{ active: session.config.id === selectedId }"
        @click="emit('select', session.config.id)"
      >
        <span class="dot" :class="session.running ? 'on' : 'off'"></span>

        <div class="text">
          <div class="name">{{ session.config.name || session.config.id }}</div>
          <div class="sub">
            <span>{{ session.config.kind }}</span>
            <span v-if="!session.running && session.status">
              · {{ t('list.exitCode', { code: session.status.exitCode }) }}
            </span>
          </div>
        </div>

        <div class="ops" @click.stop>
          <el-button link :icon="Edit" size="small" @click="emit('edit', session)" />
          <!-- 运行中不给删（D23）：后端那句 session is still running 只是兜底 -->
          <el-tooltip
            :content="t('msg.removeRunning')"
            :disabled="!session.running"
            placement="top"
          >
            <span>
              <el-button
                link
                type="danger"
                :icon="Delete"
                size="small"
                :disabled="session.running"
                @click="emit('remove', session)"
              />
            </span>
          </el-tooltip>
        </div>
      </div>
    </el-scrollbar>

    <div class="foot">
      <el-button :icon="Plus" class="full" @click="emit('create')">
        {{ t('list.create') }}
      </el-button>
    </div>
  </div>
</template>

<style scoped>
.session-list {
  display: flex;
  flex-direction: column;
  height: 100%;
  border-right: 1px solid var(--el-border-color);
}

.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px;
  border-bottom: 1px solid var(--el-border-color);
}

.title {
  font-weight: 600;
  font-size: 13px;
}

.count {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.body {
  flex: 1;
  min-height: 0;
}

.empty {
  padding: 16px 12px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  cursor: pointer;
  border-left: 3px solid transparent;
}

.item:hover {
  background: var(--el-fill-color-light);
}

.item.active {
  background: var(--el-fill-color);
  border-left-color: var(--el-color-primary);
}

.dot {
  flex: none;
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

.text {
  flex: 1;
  min-width: 0;
}

.name {
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sub {
  font-size: 11px;
  color: var(--el-text-color-secondary);
}

/* 操作按钮平时不占地方，鼠标移到这一行才出现 */
.ops {
  flex: none;
  display: flex;
  gap: 2px;
  opacity: 0;
}

.item:hover .ops,
.item.active .ops {
  opacity: 1;
}

.foot {
  padding: 8px;
  border-top: 1px solid var(--el-border-color);
}

.full {
  width: 100%;
}
</style>
