<script setup lang="ts">
// 会话列表。全屏，手机上点得中就行。
//
// 权限范围：能看、能启停，不能新建/编辑/删除——
// 手机上填工作目录、参数这些表单体验很差，而且误删一个会话代价不小。
import { ref } from 'vue'
import type { SessionInfo } from '../api'

const props = defineProps<{
  sessions: SessionInfo[]
  agentOnline: boolean
}>()

const emit = defineEmits<{
  (e: 'open', s: SessionInfo): void
  (e: 'start', id: string): void
  (e: 'stop', id: string): void
}>()

function subtitle(s: SessionInfo): string {
  if (s.running) return '运行中'
  if (s.exitCode === -1) return '未启动'
  if (s.errMsg) return `已退出（${s.errMsg}）`
  return `已退出（退出码 ${s.exitCode}）`
}

// 停止要二次确认。手机上一行就一个按钮，手指一滑就点上去了，
// 而停掉一个正在跑的会话是**不可撤销**的——里面可能有没保存的东西。
const pendingStop = ref<SessionInfo | null>(null)

function askStop(s: SessionInfo) {
  pendingStop.value = s
}

function confirmStop() {
  const s = pendingStop.value
  pendingStop.value = null
  if (s) emit('stop', s.id)
}
</script>

<template>
  <div class="list">
    <div v-if="!props.agentOnline" class="banner warn">
      本地电脑上的 hub 没在线上，等它启动后会自动恢复
    </div>

    <div v-if="props.sessions.length === 0" class="centered">
      <p class="dim">还没有会话</p>
    </div>

    <div v-for="s in props.sessions" :key="s.id" class="row" @click="emit('open', s)">
      <span class="dot" :class="s.running ? 'on' : 'off'"></span>
      <div class="name">
        <div>{{ s.name || s.id }}</div>
        <div class="kind">{{ subtitle(s) }}</div>
      </div>
      <button
        class="btn"
        :disabled="!props.agentOnline"
        @click.stop="s.running ? askStop(s) : emit('start', s.id)"
      >
        {{ s.running ? '停止' : '启动' }}
      </button>
    </div>

    <!-- 确认框。用整页遮罩而不是原生 confirm()：原生弹窗在手机浏览器里
         会被当成页面级对话框，样式不受控，而且长按选中之类的手势会打架。 -->
    <div v-if="pendingStop" class="modal-mask" @click.self="pendingStop = null">
      <div class="modal">
        <div class="modal-title">停止「{{ pendingStop.name || pendingStop.id }}」？</div>
        <p class="dim modal-body">
          里面正在跑的程序会被直接结束，没保存的东西就没了。
        </p>
        <div class="modal-actions">
          <button class="btn" @click="pendingStop = null">取消</button>
          <button class="btn danger" @click="confirmStop">停止</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-mask {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  z-index: 50;
}

.modal {
  width: 100%;
  max-width: 340px;
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 18px 16px 14px;
}

.modal-title {
  font-size: 15px;
  font-weight: 600;
  margin-bottom: 8px;
}

.modal-body {
  font-size: 13px;
  line-height: 1.5;
  margin: 0 0 16px;
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.modal-actions .btn {
  min-height: 40px;
  padding: 0 18px;
}

.modal-actions .btn.danger {
  background: var(--danger);
  border-color: var(--danger);
  color: #fff;
}
</style>
