// 自动登录用的凭证从哪来，按优先级：
//
//   1. 地址栏里的 —— `#key=xxx&secret=yyy` 或 `?key=xxx&secret=yyy`
//   2. localStorage 里上次登录成功的那一对
//
// 优先认 fragment。fragment 不发给服务器，所以不会进 Caddy 的访问日志，也不会
// 出现在 Referer 里；查询串会。查询串也认，是因为有些「把网页包成 App」的工具
// 会把 fragment 吃掉。
//
// 读到就立刻从地址栏抹掉：截图、录屏、误分享都不会漏。
//
// 为什么还要存 localStorage：manifest 的 start_url 必须是公开的（manifest 谁都能
// 取），不能带凭证，所以「添加到主屏幕」启动时 fragment 一定不在。不存的话每次
// 启动都得重输一遍——那正是这个页面要解决的问题。
// 只在登录成功之后才存，所以存下来的永远是验证过的凭证。

const HASH_KEY = 'key'
const HASH_SECRET = 'secret'
const STORE_KEY = 'hub.remote.key'
const STORE_SECRET = 'hub.remote.secret'

// Credentials 是登录要的两样：使用 key（地址）+ 登陆密钥（凭证）。
export interface Credentials {
  key: string
  secret: string
}

let creds: Credentials | null = null
let inited = false
let taken = false

// ⚠️ 这里不能用 URLSearchParams 解 fragment。
//
// 查询串的解码规则是「application/x-www-form-urlencoded」：里面的 + 代表空格。
// 而 key 和密钥都是 base64，本身就带 +、/ 和 =（实测，2026-10-11）。走 URLSearchParams
// 的话 + 会被悄悄换成空格，凭证就废了——而且不报错，只报「登录失败」。
//
// 手动拆，再用 decodeURIComponent：它只认 %XX，不碰 +，正好是 fragment 那套规则。
function parsePairs(raw: string): Map<string, string> {
  const out = new Map<string, string>()
  for (const part of raw.split('&')) {
    const sep = part.indexOf('=')
    if (sep < 0) continue
    const k = part.slice(0, sep)
    const v = part.slice(sep + 1)
    if (!k || !v) continue
    try {
      out.set(k, decodeURIComponent(v))
    } catch {
      // 百分号写坏了。原样用，总比把凭证丢掉强。
      out.set(k, v)
    }
  }
  return out
}

function fromHash(): Credentials | null {
  const raw = location.hash.startsWith('#') ? location.hash.slice(1) : location.hash
  if (!raw) return null
  const m = parsePairs(raw)
  const key = m.get(HASH_KEY)
  const secret = m.get(HASH_SECRET)
  if (!key || !secret) return null
  return { key, secret }
}

function fromQuery(): Credentials | null {
  const raw = location.search.startsWith('?') ? location.search.slice(1) : location.search
  if (!raw) return null
  const m = parsePairs(raw)
  const key = m.get(HASH_KEY)
  const secret = m.get(HASH_SECRET)
  if (!key || !secret) return null
  return { key, secret }
}

function readStored(): Credentials | null {
  try {
    const key = localStorage.getItem(STORE_KEY)
    const secret = localStorage.getItem(STORE_SECRET)
    if (!key || !secret) return null
    return { key, secret }
  } catch {
    // 隐私模式下会抛。没有就没有，不影响别的。
    return null
  }
}

// 登录成功之后调一次。只存验证过的凭证，坏凭证不会留在里面。
export function rememberCredentials(key: string, secret: string): void {
  try {
    localStorage.setItem(STORE_KEY, key)
    localStorage.setItem(STORE_SECRET, secret)
  } catch {
    // 同上，存不下就下次再输一遍
  }
}

// 启动时调一次：定下这次会话用哪套凭证，并把地址栏清干净。重复调用无副作用。
export function initCredentials(): void {
  if (inited) return
  inited = true

  const fromUrl = fromHash() ?? fromQuery()
  if (!fromUrl) {
    creds = readStored()
    return
  }

  creds = fromUrl
  // 只删凭证这一个键，其余查询参数原样留着
  const params = new URLSearchParams(location.search)
  params.delete(HASH_KEY)
  params.delete(HASH_SECRET)
  const rest = params.toString()
  history.replaceState(null, '', location.pathname + (rest ? `?${rest}` : ''))
}

// 取一次凭证，取过就没了。自动登录只试一次——拿一套坏凭证反复撞，
// 会撞上服务端每分钟 5 次的限速。
export function takeAutoCredentials(): Credentials | null {
  initCredentials()
  if (taken) return null
  taken = true
  return creds
}

// 允许自动登录再试一次。
//
// 用在「cookie 没了」这条路上：relay 的会话存在内存里，它重启一次所有人的
// cookie 就都失效了，但手里的凭证还是好的。回登录页时得拿它重新换一次 cookie，
// 所以这里把「只试一次」的闸重新打开。
export function resetAutoCredentials(): void {
  taken = false
}
