<script setup lang="ts">
import { onBeforeUnmount, onMounted, useTemplateRef } from 'vue'
import { attach, detach } from '../terminal/manager'

const props = defineProps<{ sessionId: string }>()

const hostRef = useTemplateRef<HTMLDivElement>('host')

// 挂载时容器才进 DOM，所以 attach 必须放 onMounted（manager 里要量尺寸）。
onMounted(() => {
  if (hostRef.value) attach(props.sessionId, hostRef.value)
})

// 卸载只摘容器，实例留在 manager 里。
onBeforeUnmount(() => detach(props.sessionId))
</script>

<template>
  <div ref="host" class="pane"></div>
</template>

<style scoped>
.pane {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  background: #1e1e1e;
}
</style>
