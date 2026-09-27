# go-terminal-hub

**English** · [简体中文](README.zh-CN.md)

A Windows desktop app that puts scattered scripts and CLI programs under one roof:
start them, stop them, watch their output live, and type into them while they run.

> **Status: early development.** Built on Wails v3 `v3.0.0-beta.23`. The Wails v3 API
> is still in beta, so upstream breaking changes are likely. Not production-ready.

## Why

If you keep a drawer full of `.bat` files, you know the drill: a console window flashes
open, you can't tell which one is still running, and closing the window doesn't stop the
process.

The need turns out to be a size larger than batch files. Anything that runs from a single
command line — `.bat`, `.ps1`, `.exe`, a Python script, any CLI — should be manageable
from one place. So this is not a batch-file manager; it is a general terminal session
manager.

## Features

- **A real PTY, not a pipe.** Sessions run behind a Windows ConPTY pseudo-console, so
  interactive programs behave the way they do in a normal terminal.
- **Live output** streamed into an [xterm.js](https://xtermjs.org/) pane, one per session.
- **Start / stop** an individual session, or stop every session at once.
- **System tray.** Closing the window hides it; the app and its sessions keep running.
- **Persistence** of session definitions across restarts.
- **Bilingual UI** — English and 简体中文.
- **Single executable.** The frontend is embedded with `go:embed`, so runtime needs no
  extra files.

## Requirements

| | |
|---|---|
| OS | Windows 10 1809 (build 17763) or newer |
| Go | 1.25 or newer |
| Node.js | 18 or newer |
| npm | 7 or newer |
| Wails v3 CLI | `v3.0.0-beta.23` |
| WebView2 Runtime | preinstalled on Windows 11; on Windows 10 install the Evergreen Runtime |

The Windows 10 1809 floor comes from **ConPTY**, not from Wails. There is no non-Windows
build: `internal/exec/conpty` only contains a `_windows.go` file, because ConPTY is a
Windows API.

## Getting started

```bash
git clone https://github.com/uncleyumo/go-terminal-hub.git
cd go-terminal-hub

wails3 dev
```

`wails3 dev` installs frontend dependencies on first run, starts Vite on port 9245,
builds the Go side, and launches the app with hot reload for both halves.

### Build

```bash
wails3 build      # -> bin/go-terminal-hub.exe
wails3 package    # installer, see build/windows
```

Builds are dispatched through [go-task](https://taskfile.dev/) via `wails3 task`, so
individual steps can be run on their own:

```bash
wails3 task common:build:frontend     # Vue + Vite build only
wails3 task common:generate:bindings  # regenerate frontend/bindings from the Go services
```

## Project layout

```
main.go                  entry point: embeds frontend/dist, binds services, window, tray
*_service.go             services exposed to the frontend
internal/exec/
  conpty/                Windows ConPTY wrapper
  executor/              process launch and lifecycle
  session/               one managed session
  sink/                  turns session output into frontend events
  hub/                   registry and orchestration of all sessions
  store/                 session persistence
frontend/                Vue 3 + TypeScript + Vite
  src/components/        SessionList, SessionForm, TerminalPane, ...
  src/terminal/          xterm.js instance management
  src/i18n/              en, zh-CN
  bindings/              generated TypeScript bindings for the Go services
build/                   Wails v3 platform scaffold (Taskfiles, icons, packaging)
```

## How it fits together

```
xterm.js  <-  Wails events  <-  sink  <-  session  <-  executor  <-  ConPTY
```

The frontend calls into `HubService` to start a session. The hub creates a `Session`,
which drives an `Executor`, which owns the ConPTY pseudo-console. Everything read from
the console goes to a `Sink`, which emits the `session:started`, `session:output` and
`session:exited` events the frontend subscribes to. The terminal pane writes those
payloads into xterm.js.

## License

[MIT](LICENSE)
