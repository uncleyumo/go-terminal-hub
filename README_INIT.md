# go-terminal-hub · 开工前必读

用 Go + Wails 写一个 Windows 桌面应用，把散落各处的脚本和命令行程序集中管起来：
统一启停、实时看输出、能跟运行中的程序交互。最终产物是单个 `go-terminal-hub.exe`，双击即用。

这份文档只回答一件事：**动手之前，你需要知道这个骨架是怎么转起来的。**

---

## 一、三层结构：一条命令最后落到哪

```
wails3 <子命令>          第 1 层  CLI。自己不做活，只负责调 Taskfile
  └─ Taskfile.yml        第 2 层  任务定义。真正描述"构建分哪几步"
       ├─ go build                      第 3 层  编译 Go
       ├─ npm install / npm run build   第 3 层  编译前端
       └─ wails3 generate bindings      又调回 CLI
```

**wails3 CLI 内嵌了 go-task 运行时**（`internal/commands/task.go` 原文 "fall through to the embedded
task runtime"），所以**不用另外装 `task`**。

根 `Taskfile.yml` 只做分发：`build` → `{{.GOOS}}:build` → `build/windows/Taskfile.yml`。
公共步骤在 `build/Taskfile.yml`（include 名 `common`），平台相关的在 `build/<平台>/Taskfile.yml`。

> 根 Taskfile 的 `includes:` **无条件列出了全部六个平台**（common / windows / darwin / linux / ios / android）。
> 所以 `build/darwin/`、`build/android/`、`build/ios/` 这些目录**不能删、也不能整个 gitignore 掉**——
> 缺任何一个文件，`task` 加载就失败，Windows 构建会跟着一起跑不起来。

---

## 二、目录里谁是谁

| 路径 | 是什么 | 动不动 |
|---|---|---|
| `main.go` | 入口。建 app、建窗口、注册 service、`app.Run()` | 要改 |
| `internal/service/greetservice.go` | 模板自带的示例 service（一个 `Greet` 方法） | 会被换成 Executor / Session |
| `go.mod` / `go.sum` | Go 依赖。**已锁 `wails/v3 v3.0.0-beta.23`** | 加依赖时自动改 |
| `Taskfile.yml` | 构建入口，只做平台分发 | 基本不动 |
| `build/config.yml` | 应用元信息（公司 / 产品名 / 版本）+ dev 模式配置 | M5 要改 |
| `build/windows/` | Windows 专用：`icon.ico`、`info.json`、`wails.exe.manifest`、nsis / msix 打包脚本 | M5 看 |
| `build/appicon.png` | 图标源图，`generate:icons` 由它生成 `.ico` / `.icns` | M5 换 |
| `frontend/` | Vue 3 + TypeScript + Vite | 要改 |
| `frontend/src/App.vue` | 前端根组件 | 要改 |
| `frontend/bindings/` | **生成物**，Go service 的 TS 镜像 | 别手改 |
| `frontend/dist/` | **生成物**，被 Go 编译时 embed 进 exe | 别手改 |
| `bin/` | 编译产物 | 别管 |
| `test/demo/` | M1 的 ConPTY spike 代码，独立可跑 | 参考用 |

---

## 三、原理：Go 怎么写出桌面应用

一句话：**Go 进程负责逻辑，WebView2 负责画界面，两边靠 bindings 和 events 通信。**

```
┌─ go-terminal-hub.exe ─────────────────────────────────┐
│                                                        │
│   Go 侧（你的代码）              WebView2 侧（前端）    │
│   ┌───────────────┐             ┌───────────────┐     │
│   │ GreetService  │◄──bindings─►│  Vue + TS     │     │
│   │ Executor ...  │             │  xterm.js     │     │
│   └───────┬───────┘             └───────┬───────┘     │
│           │        ◄──events──►         │             │
│           ▼                             ▼             │
│   ┌─────────────────────────────────────────────┐     │
│   │ 内嵌 HTTP 资源服务器（assetserver）          │     │
│   │   生产：从 embed 的 frontend/dist 取         │     │
│   │   开发：反代到 Vite dev server               │     │
│   └─────────────────────────────────────────────┘     │
└────────────────────────────────────────────────────────┘
```

### 3.1 前端怎么进到 exe 里 —— `go:embed`

`main.go` 里这行是关键：

```go
//go:embed all:frontend/dist
var assets embed.FS
```

编译时，`frontend/dist/` 整个目录被**塞进二进制**。所以 `frontend/dist` 必须存在，
否则 `go build` 直接报 `pattern all:frontend/dist: no matching files found`。
这就是你现在跑 `go build ./...` 会失败的原因——前端还没构建过。

### 3.2 两边怎么通信

**bindings（前端调 Go）**

Go 侧把结构体注册成 service：

```go
application.NewService(&GreetService{})
```

`wails3 generate bindings` 扫这些 service 的**导出方法**，生成 TS 代码到 `frontend/bindings/`。
前端 import 后就能像调本地函数一样调 Go：

```ts
const result = await GreetService.Greet("world")
```

**events（Go 推前端）**

```go
application.RegisterEvent[string]("time")   // 注册，为了拿到强类型 TS API
app.Event.Emit("time", now)                 // 推
```

```ts
import { Events } from '@wailsio/runtime'
Events.On('time', (e) => { /* ... */ })
```

> **这条是本项目的主路。** 终端输出是持续、高频、单向的流，只能靠事件推，不能靠前端轮询调 Go 方法。
> M2 里"输出每 50ms 合批"就是在这个机制上做节流。

### 3.3 开发模式下前端从哪来 —— 一个环境变量

| | 前端资源来源 |
|---|---|
| 生产构建（带 `production` tag） | `frontend/dist`，已经 embed 进 exe |
| 开发构建（不带 production tag） | `wails3 dev` 设 `FRONTEND_DEVSERVER_URL=http://127.0.0.1:9245`，**反代到 Vite dev server** |

判定逻辑在 Wails 源码里，同一份 `main.go` 靠 **build tag** 编出两种行为：

```go
// internal/assetserver/build_dev.go        只在非 production 构建里编译
func GetDevServerURL() string { return os.Getenv("FRONTEND_DEVSERVER_URL") }

// internal/assetserver/build_production.go 生产构建里恒返回空
func GetDevServerURL() string { return "" }
```

所以 dev 模式下改前端有热更新，不用重新编译 Go。

### 3.4 窗口是 WebView2，不是内置浏览器

Wails 不打包浏览器内核，用系统自带的 **WebView2 运行时**（Win11 自带）。
这就是为什么 `wails3 doctor` 在 Windows 上只查 WebView2 一项——它是唯一的系统要求。

---

## 四、构建流程 `wails3 build`

`wails3 build` → 根 Taskfile `build` → `windows:build` → `build:native`，按依赖顺序：

```
1. go mod tidy                                    common:go:mod:tidy
2. npm install                                    install:frontend:deps
3. wails3 generate bindings -ts -i -clean=true    generate:bindings
4. npm run build  即  vue-tsc && vite build       build:frontend
      → 产出 frontend/dist/
5. wails3 generate icons                          generate:icons
      build/appicon.png → build/windows/icon.ico
6. wails3 generate syso -arch amd64 \
      -icon windows/icon.ico \
      -manifest windows/wails.exe.manifest \
      -info windows/info.json \
      -out ../wails_windows_amd64.syso            generate:syso
      → 项目根生成 .syso（图标 + 清单），链接进 exe
7. go build -tags production -trimpath -buildvcs=false \
      -ldflags="-w -s -H windowsgui" \
      -o bin/go-terminal-hub.exe
8. 删掉 .syso
```

几个值得记住的点：

- `-H windowsgui` —— 让 exe 以 GUI 子系统启动，**不弹黑框**。正是你最初想解决的问题
- `-tags production` —— 决定 3.3 里 `GetDevServerURL` 编哪一份
- `-s -w` —— 去掉符号表和调试信息，产物更小
- `-buildvcs=false` —— 不去读 git 信息（v3 加的，v2 没有）
- 前端构建排在 Go 编译**之前**，因为 dist 要被 embed

---

## 五、开发流程 `wails3 dev`

`wails3 dev` 读 `build/config.yml` 的 `dev_mode.executes`，跑三件事：

| 顺序 | 命令 | 类型 | 干什么 |
|---|---|---|---|
| 1 | `wails3 build DEV=true` | blocking | 先编一次：前端不压缩、Go 不优化，方便调试 |
| 2 | `wails3 task common:dev:frontend` | background | `npm run dev` 起 Vite dev server（端口 9245） |
| 3 | `wails3 task run` | primary | 跑 `bin/go-terminal-hub.exe` |

**文件监视**：`*.go` / `*.js` / `*.ts`，debounce 1000ms，`frontend/` 整个目录被排除
（前端交给 Vite 自己热更新）。

改 Go → 重新编译 + 重启应用；改前端 → Vite 热更新，不重启。

---

## 六、常用命令

| 命令 | 干什么 |
|---|---|
| `wails3 dev` | 开发模式，热更新 |
| `wails3 build` | 生产构建 → `bin/go-terminal-hub.exe` |
| `wails3 build DEV=true` | 开发构建（不压缩、不优化） |
| `wails3 generate bindings` | 只重新生成前端 TS 绑定 |
| `wails3 generate syso` | 只重新生成 Windows 图标 / 清单资源 |
| `wails3 package` | 打安装包（NSIS 或 MSIX） |
| `wails3 doctor` | 环境体检 |
| `wails3 task <名字>` | 直接跑 Taskfile 里的某个任务 |
| `wails3 task --list` | 列出所有可用任务 |

`wails3 build` 在 v3 里**只剩 4 个 flag**：`-tags` / `-obfuscated` / `-garbleargs` / `-nocolour`。
v2 的 `-platform` / `-webview2` / `-o` / `-clean` 全部作废。
构建参数走位置参数：`wails3 build DEV=true`（写成 `-dev` 会失败）。

---

## 七、这个项目的特殊约束

不是 Wails 的通用知识，是 M1 实测出来的，写代码前必须知道：

1. **ConPTY 输出掺着 VT 转义序列** —— 必须交给 xterm.js 渲染，不能直接打进普通控制台
2. **`frontend/dist` 必须存在** —— `//go:embed all:frontend/dist` 是编译期要求
3. **起会话后要立刻 `Resize`** —— go-pty 的 `New()` 写死 80×25
4. **停止时"先排干再关"** —— `Wait()` 返回不等于输出读完；解法是只关会话、等 `io.EOF`，
   **绝不调 `p.Close()`**（会 `0xc0000374` 堆损坏）
5. **`CGO_ENABLED=1` 会不会破坏"单文件 exe"** —— 待 M5 用 `dumpbin /dependents` 验

技术依据在教程仓库的 `ARCHITECTURE.md`。

---

## 附：这个骨架是怎么建出来的

```
cd E:\Dev_work\Go_Dev\go_projects
Rename-Item go-terminal-hub go-terminal-hub-old
wails3 init -t vue -n go-terminal-hub -mod github.com/uncleyumo/go-terminal-hub
Move-Item go-terminal-hub-old\.git go-terminal-hub\.git
Remove-Item -Recurse -Force go-terminal-hub-old
```

- `-t vue` = Vue + TypeScript + Vite（v2 的 `vue-ts` 在 v3 不存在）
- `-n` 必填，且它的值**永远拼在 `-d` 后面**：目标是 `<d>\<n>`，`-d` 默认 `.`
- 目标目录**非空直接报错**，没有 force 开关 —— 所以要先让开
- `.git` 跟着目录改名一起走，不用删也不用重设
