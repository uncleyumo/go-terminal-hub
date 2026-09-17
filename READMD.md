**wails v3 command**
`wails3 init -t vue` | generate the project skeleton
`wails3 generate bindings` | generate the bindings between go's excutor and frontend
`wails3 generate syso` | generate icon for exe
`wails3 build` | build and package wails v3 project to exe

**what is `go-pty`**
> ConPTY = 微软给 Windows 补上的伪终端 API，Windows 10 1809（build 17763）起提供
- go-pty 是一个跨平台的「伪终端」Go 库
- Windows 上它底层就是 ConPTY，Unix 上是 openpty
- Go 标准库做不了 ConPTY

**project init**

`wails3 init -t vue -n go-terminal-hub -mod github.com/uncleyumo/go-terminal-hub`

- `-t vue` = Vue + TypeScript + Vite（v2 的 `vue-ts` 在 v3 不存在）
- `-n` 必填；`-d` 默认就是当前目录
- 会覆盖现有的 go.mod 和 .gitignore：跑完确认 go-pty 还在，`.idea` / `**/*.exe` 补回去