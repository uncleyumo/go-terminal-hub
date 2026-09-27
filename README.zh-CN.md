# go-terminal-hub

[English](README.md) · **简体中文**

一个 Windows 桌面应用，把散落各处的脚本和命令行程序集中管起来：统一启停、实时看输出、能跟运行中的程序交互。

> 状态：早期开发。基于 Wails v3 `v3.0.0-beta.23`，而 v3 API 仍在 beta，上游随时可能改接口。还不适合生产使用。

## 为什么做

抽屉里攒了一堆 `.bat` 的人都知道那个场景：黑框一闪而过，说不清哪个还在跑，窗口一关进程也没停。

做着做着会发现需求比批处理大一圈。只要能在一行命令里跑起来的东西——`.bat`、`.ps1`、`.exe`、python 脚本、任意 CLI——都该能托管。所以这不是批处理管理器，是通用终端会话管理器。

## 功能

| 能力 | 说明 |
|---|---|
| 真 PTY | 会话跑在 Windows ConPTY 伪控制台后面，交互式程序的表现和普通终端一致，不是管道 |
| 实时输出 | 每个会话一个 [xterm.js](https://xtermjs.org/) 面板，输出边产生边渲染 |
| 启停 | 单个会话启停，也能一次停掉全部 |
| 系统托盘 | 关窗口只是隐藏，应用和会话继续跑 |
| 持久化 | 会话定义存盘，重启还在 |
| 双语界面 | 英文 / 简体中文 |
| 单文件 | 前端用 `go:embed` 打进二进制，运行时不依赖额外文件 |

## 环境要求

| 项 | 要求 |
|---|---|
| 操作系统 | Windows 10 1809（build 17763）或更新 |
| Go | 1.25 以上 |
| Node.js | 18 以上 |
| npm | 7 以上 |
| Wails v3 CLI | `v3.0.0-beta.23` |
| WebView2 运行时 | Win11 自带；Win10 需装 Evergreen Runtime |

Windows 10 1809 这条线是 ConPTY 的要求，不是 Wails 的。目前也没有非 Windows 构建：`internal/exec/conpty` 下只有一个 `_windows.go`，因为 ConPTY 是 Windows API。

## 快速开始

```bash
git clone https://github.com/uncleyumo/go-terminal-hub.git
cd go-terminal-hub

wails3 dev
```

`wails3 dev` 首次运行会装前端依赖，起 Vite（端口 9245），编译 Go 侧，然后启动应用，前后端都带热重载。

### 构建

```bash
wails3 build      # 产物 bin/go-terminal-hub.exe
wails3 package    # 打安装包，见 build/windows
```

构建走 [go-task](https://taskfile.dev/)，由 `wails3 task` 调度，单步可以拆开跑：

```bash
wails3 task common:build:frontend     # 只构建 Vue + Vite
wails3 task common:generate:bindings  # 从 Go 服务重新生成 frontend/bindings
```

## 目录结构

```
main.go                  入口：嵌入 frontend/dist、绑定服务、建窗口和托盘
*_service.go             暴露给前端的服务
internal/exec/
  conpty/                Windows ConPTY 封装
  executor/              进程启动与生命周期
  session/               一个被托管的会话
  sink/                  把会话输出转成前端事件
  hub/                   全部会话的注册与编排
  store/                 会话持久化
frontend/                Vue 3 + TypeScript + Vite
  src/components/        SessionList、SessionForm、TerminalPane 等
  src/terminal/          xterm.js 实例管理
  src/i18n/              en、zh-CN
  bindings/              由 Go 服务生成的 TypeScript 绑定
build/                   Wails v3 平台脚手架（Taskfile、图标、打包）
```

## 数据怎么流

```
xterm.js  <-  Wails 事件  <-  sink  <-  session  <-  executor  <-  ConPTY
```

前端调 `HubService` 启动一个会话。hub 建出 `Session`，`Session` 驱动 `Executor`，`Executor` 持有 ConPTY 伪控制台。从控制台读到的内容交给 `Sink`，由它发出 `session:started`、`session:output`、`session:exited` 三个事件，前端订阅这些事件，终端面板把载荷写进 xterm.js。

## 许可证

[MIT](LICENSE)
