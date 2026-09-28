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
  <!-- relative：manager 把 xterm 的容器绝对定位铺满这里（见 manager.ts 的说明） -->
  <div ref="host" class="relative min-h-0 flex-1 overflow-hidden bg-term"></div>
</template>
