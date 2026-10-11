// 自动登录用的令牌从哪来，按优先级：
//
//   1. 地址栏里的 —— `#token=xxx` 或 `?token=xxx`
//   2. localStorage 里上次登录成功的那一个
//
// 优先认 fragment。fragment 不发给服务器，所以不会进 Caddy 的访问日志，也不会
// 出现在 Referer 里；查询串会。查询串也认，是因为有些「把网页包成 App」的工具
// 会把 fragment 吃掉。
//
// 读到就立刻从地址栏抹掉：截图、录屏、误分享都不会漏。
//
// 为什么还要存 localStorage：manifest 的 start_url 必须是公开的（manifest 谁都能
// 取），不能带令牌，所以「添加到主屏幕」启动时 fragment 一定不在。不存的话每次
// 启动都得重输一遍——那正是这个页面要解决的问题。
// 只在登录成功之后才存，所以存下来的永远是验证过的令牌；换了令牌手输一次就自愈。

const HASH_PREFIX = '#token='
const QUERY_KEY = 'token'
const STORE_KEY = 'hub.remote.token'

let token: string | null = null
let inited = false
let taken = false

function fromHash(): string | null {
  if (!location.hash.startsWith(HASH_PREFIX)) return null
  const raw = location.hash.slice(HASH_PREFIX.length)
  if (!raw) return null
  try {
    // fragment 浏览器不会自动解码，得自己来
    return decodeURIComponent(raw)
  } catch {
    // 百分号写坏了。原样用，总比把令牌丢掉强。
    return raw
  }
}

// ⚠️ 这里不能用 URLSearchParams。
//
// 查询串的解码规则是「application/x-www-form-urlencoded」：里面的 + 代表空格。
// 而令牌是标准 base64，本身就带 +、/ 和 =（实测，2026-10-11）。走 URLSearchParams
// 的话 + 会被悄悄换成空格，令牌就废了——而且不报错，只报「令牌不对」。
//
// 手动拆，再用 decodeURIComponent：它只认 %XX，不碰 +，正好是 fragment 那套规则。
function fromQuery(): string | null {
  const q = location.search.startsWith('?') ? location.search.slice(1) : location.search
  for (const part of q.split('&')) {
    const sep = part.indexOf('=')
    if (sep < 0 || part.slice(0, sep) !== QUERY_KEY) continue
    const raw = part.slice(sep + 1)
    if (!raw) return null
    try {
      return decodeURIComponent(raw)
    } catch {
      // 百分号写坏了。原样用，总比把令牌丢掉强。
      return raw
    }
  }
  return null
}

function readStored(): string | null {
  try {
    return localStorage.getItem(STORE_KEY)
  } catch {
    // 隐私模式下会抛。没有就没有，不影响别的。
    return null
  }
}

// 登录成功之后调一次。只存验证过的令牌，坏令牌不会留在里面。
export function rememberToken(value: string): void {
  try {
    localStorage.setItem(STORE_KEY, value)
  } catch {
    // 同上，存不下就下次再输一遍
  }
}

// 启动时调一次：定下这次会话用哪个令牌，并把地址栏清干净。重复调用无副作用。
export function initToken(): void {
  if (inited) return
  inited = true

  const fromUrl = fromHash() ?? fromQuery()
  if (!fromUrl) {
    token = readStored()
    return
  }

  token = fromUrl
  // 只删令牌这一个键，其余查询参数原样留着
  const params = new URLSearchParams(location.search)
  params.delete(QUERY_KEY)
  const rest = params.toString()
  history.replaceState(null, '', location.pathname + (rest ? `?${rest}` : ''))
}

// 取一次令牌，取过就没了。自动登录只试一次——拿一个坏令牌反复撞，
// 会撞上服务端每分钟 5 次的限速。
export function takeAutoToken(): string | null {
  initToken()
  if (taken) return null
  taken = true
  return token
}

// 允许自动登录再试一次。
//
// 用在「cookie 没了」这条路上：relay 的会话存在内存里，它重启一次所有人的
// cookie 就都失效了，但手里的令牌还是好的。回登录页时得拿它重新换一次 cookie，
// 所以这里把「只试一次」的闸重新打开。
export function resetAutoToken(): void {
  taken = false
}
