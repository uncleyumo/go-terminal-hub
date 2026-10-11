<script setup lang="ts">
// 可折叠的按键工具条。
//
// 手机软键盘上没有 Esc / Tab / 方向键 / 退格 / Ctrl 组合，而终端里这几样是高频操作。
//
// 分两层，因为一屏塞不下也不该塞：
//   常驻层 —— Esc/Tab/退格/方向/回车，闭着眼就能按
//   展开层 —— Ctrl 组合和符号，**每个都带一句用途**。摆一排 C-xxx 在那里，
//             不知道 C-r 是干什么的人根本不会去点，等于没做。
//
// 两个粘滞修饰键：按下高亮，再按任意键就带上它，然后自动弹起——
// 和手机输入法上的 Shift 一样的心智。
import { ref, watch } from 'vue'
import {
  ROW_CTRL,
  ROW_EDIT,
  ROW_MOVE,
  ROW_NEWLINE,
  ROW_SYMBOL,
  type KeyDef,
} from '../keys'

const props = defineProps<{
  ctrl: boolean
  shift: boolean
}>()

const emit = defineEmits<{
  (e: 'press', key: KeyDef): void
  (e: 'toggle', mod: 'ctrl' | 'shift'): void
}>()

const COLLAPSE_KEY = 'hub.remote.keybar.collapsed'
const MORE_KEY = 'hub.remote.keybar.more'

// 读也要包 try：隐私模式、WebView 里关掉 DOM storage 都会抛。
// 抛在 setup() 里会让整个键盘栏渲染不出来——而它只是记个展开状态，
// 丢了大不了回到默认，不该把终端页带塌。
function recall(key: string): boolean {
  try {
    return localStorage.getItem(key) === '1'
  } catch {
    return false
  }
}

const collapsed = ref(recall(COLLAPSE_KEY))
const more = ref(recall(MORE_KEY))

function remember(key: string, value: boolean) {
  try {
    localStorage.setItem(key, value ? '1' : '0')
  } catch {
    // 隐私模式下 localStorage 会抛。状态丢了不影响用。
  }
}

watch(collapsed, (v) => remember(COLLAPSE_KEY, v))
watch(more, (v) => remember(MORE_KEY, v))
</script>

<template>
  <div class="keybar">
    <!-- 折叠整条工具条：用外接键盘或者只想看输出的时候，把它收掉 -->
    <button class="handle" @click="collapsed = !collapsed">
      {{ collapsed ? '⌨ 按键' : '⌄' }}
    </button>

    <div v-if="!collapsed" class="body">
      <!-- 粘滞修饰键 + 「更多按键」开关 -->
      <div class="mods">
        <button class="key mod" :class="{ on: props.ctrl }" @click="emit('toggle', 'ctrl')">Ctrl</button>
        <button class="key mod" :class="{ on: props.shift }" @click="emit('toggle', 'shift')">Shift</button>
        <span class="spacer"></span>
        <button class="key mod more" :class="{ on: more }" @click="more = !more">
          {{ more ? '收起 ⌃' : '更多按键 ⌄' }}
        </button>
      </div>

      <!-- 常驻层 -->
      <div class="row">
        <button v-for="k in ROW_EDIT" :key="k.label" class="key" @click="emit('press', k)">
          <span class="label">{{ k.label }}</span>
          <span v-if="k.hint" class="hint">{{ k.hint }}</span>
        </button>
      </div>
      <div class="row">
        <button v-for="k in ROW_MOVE" :key="k.label" class="key" @click="emit('press', k)">
          <span class="label">{{ k.label }}</span>
          <span v-if="k.hint" class="hint">{{ k.hint }}</span>
        </button>
      </div>

      <!-- 展开层 -->
      <template v-if="more">
        <div class="section">
          换行 —— 回车是「发送」，这些是「换行不发送」。<br />
          Claude Code 里 Ctrl+Enter 也是发送，不是换行。
        </div>
        <div class="row">
          <button v-for="k in ROW_NEWLINE" :key="k.label" class="key wide" @click="emit('press', k)">
            <span class="label">{{ k.label }}</span>
            <span class="hint">{{ k.hint }}</span>
          </button>
        </div>

        <div class="section">Ctrl 组合</div>
        <div class="grid">
          <button v-for="k in ROW_CTRL" :key="k.label" class="key wide" @click="emit('press', k)">
            <span class="label">{{ k.label }}</span>
            <span class="hint">{{ k.hint }}</span>
          </button>
        </div>

        <div class="section">符号</div>
        <div class="grid">
          <button v-for="k in ROW_SYMBOL" :key="k.label" class="key sym" @click="emit('press', k)">
            <span class="label">{{ k.label }}</span>
          </button>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.keybar {
  flex: 0 0 auto;
  background: var(--bg-elev);
  border-top: 1px solid var(--border);
  padding-bottom: var(--safe-bottom);
  /* 展开之后可能很高，别把终端挤没了 */
  max-height: 52vh;
  overflow-y: auto;
}

.handle {
  display: block;
  width: 100%;
  min-height: 30px;
  color: var(--text-dim);
  font-size: 13px;
}

.body {
  padding: 0 8px 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.mods,
.row {
  display: flex;
  gap: 6px;
  align-items: stretch;
}

.row {
  overflow-x: auto;
  scrollbar-width: none;
}

.row::-webkit-scrollbar {
  display: none;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(78px, 1fr));
  gap: 6px;
}

.section {
  margin-top: 4px;
  font-size: 11px;
  line-height: 1.4;
  color: var(--text-dim);
}

.spacer {
  flex: 1;
}

.key {
  flex: 1 0 auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 1px;
  min-width: 46px;
  min-height: 42px;
  padding: 4px 8px;
  border-radius: 7px;
  background: #232838;
  border: 1px solid var(--border);
  color: var(--text);
}

.key:active {
  background: #2f3648;
}

.key .label {
  font-size: 14px;
  line-height: 1.1;
}

/* 用途说明。没有它，展开层就是一排看不懂的缩写。 */
.key .hint {
  font-size: 10px;
  line-height: 1.1;
  color: var(--text-dim);
  white-space: nowrap;
}

.key.wide {
  flex: 0 0 auto;
}

.key.sym {
  min-height: 38px;
}

.key.sym .label {
  font-size: 16px;
}

.key.mod {
  flex: 0 0 auto;
  min-width: 62px;
  flex-direction: row;
}

.key.mod.on {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}

.key.mod.more {
  min-width: 92px;
}
</style>
