<script setup lang="ts">
// 登录页：输一次令牌，服务器下发 HttpOnly cookie，之后免登录。
//
// 令牌走 POST 请求体而不是 URL 查询串——URL 会进访问日志、会进 Referer，
// 对一个能把按键送进真实终端的服务来说，那是不必要的暴露面。
import { ref } from 'vue'

const emit = defineEmits<{ (e: 'success'): void }>()

const token = ref('')
const busy = ref(false)
const error = ref('')

async function submit() {
  if (busy.value || !token.value) return
  busy.value = true
  error.value = ''
  try {
    const res = await fetch('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token: token.value }),
    })
    if (res.status === 429) {
      error.value = '尝试次数过多，请等一分钟再试'
      return
    }
    if (!res.ok) {
      error.value = '令牌不对'
      return
    }
    token.value = ''
    emit('success')
  } catch {
    error.value = '连不上服务器'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="centered">
    <h1 style="font-size: 20px; margin: 0">terminal hub</h1>
    <p class="dim" style="margin: 0; text-align: center; font-size: 13px">
      输入桌面端「远程访问」里显示的令牌
    </p>

    <input
      v-model="token"
      type="password"
      inputmode="text"
      autocomplete="current-password"
      placeholder="令牌"
      @keyup.enter="submit"
    />

    <button class="btn primary" :disabled="busy || !token" @click="submit">
      {{ busy ? '登录中…' : '登录' }}
    </button>

    <p v-if="error" style="color: var(--danger); font-size: 13px; margin: 0">{{ error }}</p>
  </div>
</template>
