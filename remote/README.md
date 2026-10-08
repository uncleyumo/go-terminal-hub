# remote —— 手机远程接管

用手机浏览器接管这台电脑上的终端会话：看输出、发输入、启停会话。

这是 `go-terminal-hub` 的**扩展项目**，跟桌面端共用一套 Go module，但产出是**两个独立的二进制**：

| 产物 | 跑在哪 | 干什么 |
|---|---|---|
| `go-terminal-hub.exe` | 你的 Windows 电脑 | 桌面应用本体，会话的唯一真相源 |
| `bin/hub-relay` | 一台 Linux 云服务器 | 托管网页 + 在两边搬帧 |

桌面端不 import `remote/` 下的任何包，所以 Web 端的产物不会被塞进 `go-terminal-hub.exe`。

---

## 连接方向是反的

```
┌────────────────────┐   wss://你的域名/ws/agent   ┌──────────────────────┐
│  本地 hub（Windows）│ ─────────────────────────► │  relay（Linux 云主机）│
│  Wails GUI + ConPTY│ ◄───────────────────────── │  单文件二进制          │
│  ← 会话的唯一真相源  │    Bearer 令牌              │  内嵌网页             │
└────────────────────┘                            └──────────┬───────────┘
                                                             │ wss://你的域名/ws/browser
                                                             │ HttpOnly cookie
                                                  ┌──────────▼───────────┐
                                                  │  手机浏览器（xterm）  │
                                                  └──────────────────────┘
```

**是电脑主动拨出去**，不是手机连进来。所以：

- 家里那台机器不需要公网 IP
- 不用做端口映射
- 不用装 frp / Tailscale / Cloudflare Tunnel

relay 是**哑管道**：它不理解会话，不缓存状态，只在两侧之间搬帧。会话的真相源始终在本地 hub 那台机器上——进程、ConPTY、配置都在那儿。

---

## 控制权租约：手机接管时电脑上会看到什么

每个会话在任意时刻只有**一个控制方**，要么桌面要么手机。只有控制方能发输入、改尺寸，**PTY 的尺寸跟着控制方走**。

手机点「接管」的瞬间：

| | 手机 | 电脑 |
|---|---|---|
| 能输入吗 | ✅ | ❌（`disableStdin`，敲键盘没反应） |
| 按什么尺寸渲染 | 自己屏幕量出来的（比如 48×20） | **PTY 的真实尺寸 48×20** |
| 看到什么 | 正常 | 一个居中的 48×20 小终端 + 一条「手机正在控制」的提示条 |

电脑上终端突然缩成一小块，**不是显示错误**。尺寸属于 PTY，不属于窗口：PTY 已经按 48 列在重绘了，桌面要是还按自己窗口的 200 列渲染，两边对不上，画面全花。

这和 tmux 的行为一样。点「收回控制」就切回去，PTY 跟着变回桌面尺寸。

> 纯旁观时画面可能是旧的：回放的是**字节日志不是屏幕快照**，重建不出全屏 TUI 的当前帧。但接管时会改 PTY 尺寸，程序收到窗口变化就重绘了——所以「接管 ⇒ 画面正确」。

---

## 安全

**这是一个远程代码执行面，不是普通网页。** 能打开那个网址的人，就能往这台机器的终端里敲任意按键。

| 措施 | 做法 |
|---|---|
| 默认不对外 | relay 的 `-addr` 默认 `127.0.0.1:8080`，必须显式改成 `0.0.0.0` 才暴露 |
| 传输加密 | TLS 由 Caddy 终结，relay 只监听明文端口 |
| 令牌强度 | 桌面端点「生成」按钮产出 32 字节随机值 |
| 常量时间比较 | 避免按字节比较泄露令牌前缀 |
| 登录限速 | 每 IP 每分钟 5 次 |
| 令牌不进 URL | 浏览器走 POST 换 HttpOnly cookie，不用 `?token=`（那会进访问日志和 Referer） |
| 单浏览器 | 新连接顶掉旧的；被顶掉的一方显示原因且**不自动重连** |

---

## 构建

```bash
wails3 task relay   # 产出 bin/hub-relay（Linux amd64，单文件，无动态依赖）
```

`wails3 task relay` 会先构建 Web 端再编译 Go，顺序不能反：relay 用 `//go:embed` 把 `remote/ui/dist` 嵌进去，目录不存在的话 `go build` 直接失败。

手动等价命令：

```bash
cd remote/ui && npm install && npm run build && cd ../..
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o bin/hub-relay ./remote/relay
```

---

## 部署

### 1. 服务器上装 relay

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin hub-relay
sudo install -m 0755 hub-relay /usr/local/bin/hub-relay

# 令牌：桌面端点「生成」按钮拿一个，或者自己 openssl rand -base64 32
echo 'RELAY_TOKEN=把令牌粘在这里' | sudo tee /etc/hub-relay.env
sudo chown root:hub-relay /etc/hub-relay.env && sudo chmod 0640 /etc/hub-relay.env

sudo install -m 0644 deploy/hub-relay.service /etc/systemd/system/hub-relay.service
sudo systemctl daemon-reload && sudo systemctl enable --now hub-relay
journalctl -u hub-relay -f
```

### 2. Caddy 反代 + 自动 TLS

把 `deploy/Caddyfile` 里的域名换成你的，并进 `/etc/caddy/Caddyfile`，然后 `sudo systemctl reload caddy`。

Caddy 会自动申请续期证书；WebSocket 的 `Upgrade` 头它默认就转发，不用额外配。

### 3. 桌面端填配置

打开应用 → 顶栏那个地球图标 → 填：

- **relay 地址**：`wss://你的域名/ws/agent`
- **访问令牌**：跟 `/etc/hub-relay.env` 里那个一样
- 打开「启用远程接管」

保存后立刻生效，不用重启。状态行会显示「已连接」。

### 4. 手机

浏览器打开 `https://你的域名`，输一次令牌，之后免登录。

---

## 本地调试（不用云服务器）

```bash
# 终端 1：起 relay。本地是 http，所以 cookie 不能带 Secure
wails3 task build:relay:ui
go run ./remote/relay -addr 127.0.0.1:8080 -token devtoken1234567890 -cookie-secure=false -log-level debug

# 终端 2：桌面端跑起来（task dev），在地球图标里填
#   ws://127.0.0.1:8080/ws/agent
#   devtoken1234567890

# 浏览器开 http://127.0.0.1:8080，输 devtoken1234567890
```

手机真机连同一局域网的话，把 `-addr` 改成 `0.0.0.0:8080`，手机开 `http://<电脑IP>:8080`。

改 Web 端界面时用 `cd remote/ui && npm run dev`，Vite 会把 `/api` 和 `/ws` 代理到本地的 relay，不用每次重新打包。

---

## 目录

```
remote/
├─ protocol/protocol.go   线协议（hub 侧和 relay 侧共用这一份）
├─ relay/                 relay 程序：HTTP + 登录 + 两个 WebSocket 端点
├─ ui/                    手机端网页（Vite + Vue3 + xterm.js）
│  ├─ src/
│  └─ dist/               构建产物，被 relay 嵌进去（gitignore，留 .gitkeep）
├─ deploy/                Caddyfile + systemd 单元
└─ README.md              本文件
```

`ui/embed.go` 必须待在 `ui/` 的上一级目录里——`//go:embed` 的路径是相对声明它的那个文件解析的，而且不能用 `..`。

---

## 线协议

**文本帧 = JSON 控制消息；二进制帧 = 裸字节。**

二进制帧里不带会话 id：一个浏览器同一时刻只绑定一个会话，所以「当前绑定的那个」就是唯一可能的收件人。这样终端输出既不用 base64 也不用 JSON 转义。

| 浏览器 → hub | 说明 |
|---|---|
| `list` | 要会话列表 |
| `attach {id}` | 绑定到某个会话 |
| `detach` | 解绑 |
| `resize {cols, rows}` | 改尺寸（只有控制方真正生效，旁观方会被记下来备接管用） |
| `takeover` / `release` | 接管 / 交还 |
| `start` / `stop` / `restart {id}` | 启停 |
| `resync` | 请求整段重发 |

| hub → 浏览器 | 说明 |
|---|---|
| `sessions [...]` | 会话列表（store 的名称 + hub 的运行状态拼出来的） |
| `attached {id, cols, rows, controller}` | 绑定成功，紧接着是二进制回放 |
| `lease {controller, cols, rows}` | 控制方换人或尺寸变了 |
| `size {cols, rows}` | PTY 的真实网格变了 |
| `started` / `exit` | 生命周期 |
| `agent_online` / `agent_offline` | relay 合成：本地 hub 在不在线 |
| `kicked` | relay 合成：你被新连接顶掉了，随后以 4001 关闭 |

### 关闭码

| 码 | 含义 |
|---|---|
| 4001 | 浏览器被顶掉。**收到后不许自动重连**，否则两台设备会互相踢成死循环 |
| 4002 | agent 被顶掉。退避 60 秒再连，防止两个桌面实例互顶 |
| 4004 | 对端读得太慢，断开让它重连重同步 |

---

## 排查

| 现象 | 多半是 |
|---|---|
| 状态一直「连接中」 | 地址写错了，或者服务器上 relay 没起来（`journalctl -u hub-relay -f`） |
| 状态「连接失败」 | 令牌不对（看 relay 日志里的 `agent rejected: bad token`） |
| 网页打得开但登录后没反应 | Caddy 没转发 WebSocket 的 Upgrade 头 |
| 手机显示「本地 hub 离线」 | 电脑上的应用关了，或者桌面端里远程开关被关了 |
| 手机显示「已在其他设备打开」 | 正常——只允许一个浏览器在线。点「重新连接」即可 |
| 终端输出断断续续 | 网络太差，客户端跟不上。它会自动整段重同步，不用管 |
