<template>
  <div class="term-wrapper">
    <div class="term-host" ref="termHost"></div>
    <div class="normal-button" @click="handleStartSessionClick">Start Session</div>
    <div class="normal-button" @click="handleStopSessionClick">Stop Session</div>
  </div>
</template>

<script setup lang="ts">
import {onMounted, onUnmounted, ref, useTemplateRef} from "vue";
import {Terminal} from "@xterm/xterm";
import {Events} from "@wailsio/runtime";
import {XtermDemoService} from "../../bindings/github.com/uncleyumo/go-terminal-hub";

const termHostRef = useTemplateRef<HTMLDivElement>('termHost')
const termInstance = ref<Terminal | null>(null)

const handleStartSessionClick = () => {
  XtermDemoService.StartSession()
      .then(() => {
        console.log('Session started')
      })
      .catch((err) => {
        console.error('Failed to start session', err)
      })
}

const handleStopSessionClick = () => {
  XtermDemoService.StopSession()
      .then(() => {
        console.log('Session stopped')
      })
      .catch((err) => {
        console.error('Failed to stop session', err)
      })
}

onMounted(() => {
  const host = termHostRef.value
  if (!host) return
  const term = new Terminal()
  term.open(host)
  termInstance.value = term
  Events.On('session:output', (e) => {
    console.log('Session output:', e)
    term.write(e.data.text as string)
  })

  Events.On('session:exited', (e) => {
    console.log('Session exited:', e)
  })
})

onUnmounted(() => {
  if (!termInstance.value) return
  termInstance.value.dispose()
})

</script>

<style scoped>
.term-wrapper {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 8px;
}
.term-host {
  border: 2px solid red;
  //height: 300px;
}
.normal-button {
  padding: 10px 20px;
  font-size: 16px;
  cursor: pointer;
  border: 2px solid #ccc;
  border-radius: 5px;
  background-color: #f0f0f0;
  transition: background-color 0.1s ease;
  &:hover {
    background-color: #eee;
  }
  &:active {
    background-color: #9a9a9a;
  }
}
</style>
