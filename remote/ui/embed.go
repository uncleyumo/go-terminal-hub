// Package ui 把构建好的网页嵌进 relay 二进制，让部署只需要传一个文件。
//
// embed 的路径是相对这个文件所在目录解析的，而且不能用 ".."，
// 所以这个文件必须待在 dist 的上一级 —— 这就是它不放在 relay/ 里的原因。
//
// dist/ 是 Vite 的构建产物，被 gitignore。仓库里只留一个 dist/.gitkeep，
// 保证干净克隆上 `go build ./...` / `go vet ./...` 不会因为目录不存在而失败。
// 真正要出包时用 `task relay:build`，它会先构建前端。
package ui

import "embed"

//go:embed all:dist
var Dist embed.FS
