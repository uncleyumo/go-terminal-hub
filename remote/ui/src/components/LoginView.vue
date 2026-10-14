<script setup lang="ts">
// 登录页：输一次「使用 key」和「登陆密钥」，服务器下发 HttpOnly cookie，之后免登录。
//
// 凭证走 POST 请求体而不是 URL 查询串——URL 会进访问日志、会进 Referer，
// 对一个能把按键送进真实终端的服务来说，那是不必要的暴露面。
//
// 例外：URL 里带了凭证，或者上次登录成功过。那是给「网页包成 App」和「加到主屏」
// 的场景用的——启动地址没法手输。凭证读完立刻从地址栏抹掉，见 token.ts。
//
// 为什么要两样：key 是「进哪台机器」（地址），密钥是「你有没有资格」（凭证）。
// 只有 key 没有密钥，等于知道门牌号就能开门；两样分开，门牌号泄露了也不致命。
import { onMounted, ref } from 'vue'
import { rememberCredentials, takeAutoCredentials } from '../token'

const emit = defineEmits<{ (e: 'success'): void }>()

const key = ref('')
const secret = ref('')
const busy = ref(false)
const error = ref('')

async function submit() {
  if (busy.value || !key.value || !secret.value) return
  // 先留一份：下面成功时会清空输入框，存下来的得是这次真正用的那个
  const usedKey = key.value
  const usedSecret = secret.value
  busy.value = true
  error.value = ''
  try {
    const res = await fetch('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ key: usedKey, secret: usedSecret }),
    })
    if (res.status === 429) {
      error.value = '尝试次数过多，请等一分钟再试'
      return
    }
    if (!res.ok) {
      // 中心站把失败原因分得很细（key 不存在/过期/吊销、密钥不对），
      // 原样透出来：笼统的「登录失败」让人不知道该找谁、该改什么。
      const msg = await res.text().catch(() => '')
      error.value = msg || '登录失败'
      return
    }
    // 登录成功才记：存进去的永远是验证过的凭证，换了一套手输一次就自愈
    rememberCredentials(usedKey, usedSecret)
    key.value = ''
    secret.value = ''
    emit('success')
  } catch {
    error.value = '连不上服务器'
  } finally {
    busy.value = false
  }
}

// 有现成的凭证就直接登录，不用手输。只试一次：失败了就把输入框留给用户，
// 免得拿一套坏凭证反复撞服务端每分钟 5 次的限速。
onMounted(async () => {
  const c = takeAutoCredentials()
  if (!c) return
  key.value = c.key
  secret.value = c.secret
  await submit()
  // 自动试的那次没成，说明存的那套已经废了。把框清空，用户直接敲新的就行，
  // 不用先全选删掉一长串乱码——那是密码框，看不清还删不干净。
  if (error.value) {
    key.value = ''
    secret.value = ''
  }
})
</script>

<template>
  <div class="centered">
    <h1 style="font-size: 20px; margin: 0">terminal hub</h1>
    <p class="dim" style="margin: 0; text-align: center; font-size: 13px">
      输入桌面端「远程访问」里显示的 key 和登陆密钥
    </p>

    <input
      v-model="key"
      type="text"
      inputmode="text"
      autocomplete="off"
      placeholder="使用 key"
      @keyup.enter="submit"
    />

    <input
      v-model="secret"
      type="password"
      inputmode="text"
      autocomplete="current-password"
      placeholder="登陆密钥"
      @keyup.enter="submit"
    />

    <button class="btn primary" :disabled="busy || !key || !secret" @click="submit">
      {{ busy ? '登录中…' : '登录' }}
    </button>

    <p v-if="error" style="color: var(--danger); font-size: 13px; margin: 0">{{ error }}</p>
  </div>
</template>
