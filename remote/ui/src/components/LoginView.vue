<script setup lang="ts">
// 登录页：输一次令牌，服务器下发 HttpOnly cookie，之后免登录。
//
// 令牌走 POST 请求体而不是 URL 查询串——URL 会进访问日志、会进 Referer，
// 对一个能把按键送进真实终端的服务来说，那是不必要的暴露面。
//
// 例外：URL 里带了令牌，或者上次登录成功过。那是给「网页包成 App」和「加到主屏」
// 的场景用的——启动地址没法手输。令牌读完立刻从地址栏抹掉，见 token.ts。
import { onMounted, ref } from 'vue'
import { rememberToken, takeAutoToken } from '../token'

const emit = defineEmits<{ (e: 'success'): void }>()

const token = ref('')
const busy = ref(false)
const error = ref('')

async function submit() {
  if (busy.value || !token.value) return
  // 先留一份：下面成功时会清空 token.value，存下来的得是这次真正用的那个
  const used = token.value
  busy.value = true
  error.value = ''
  try {
    const res = await fetch('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token: used }),
    })
    if (res.status === 429) {
      error.value = '尝试次数过多，请等一分钟再试'
      return
    }
    if (!res.ok) {
      error.value = '令牌不对'
      return
    }
    // 登录成功才记：存进去的永远是验证过的令牌，换了令牌手输一次就自愈
    rememberToken(used)
    token.value = ''
    emit('success')
  } catch {
    error.value = '连不上服务器'
  } finally {
    busy.value = false
  }
}

// 有现成的令牌就直接登录，不用手输。只试一次：失败了就把输入框留给用户，
// 免得拿一个坏令牌反复撞服务端每分钟 5 次的限速。
onMounted(() => {
  const t = takeAutoToken()
  if (!t) return
  token.value = t
  void submit()
})
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
