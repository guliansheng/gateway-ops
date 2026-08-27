// Package web 把 frontend 构建出的 dist 嵌入 Go 二进制。
//
// 开发期 dist 目录只有 .gitkeep 占位，本地跑 `go run` 时 EmbeddedDist() 返回的 FS
// 找不到 index.html，main.go 据此跳过静态 handler 注册（开发者用 vite dev server 即可）。
//
// Docker 构建期 Dockerfile 会先 `pnpm build` 把真正的 dist 拷到这个目录，
// 然后 `go build` 把整棵 dist 嵌入二进制 — 部署只有一个 binary，无外部依赖。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// DistFS 返回去掉顶层 "dist/" 前缀后的子树，方便直接 http.FileServer 用。
// 当 dist 目录里只有 .gitkeep 时 HasFrontend() 返回 false，调用方应跳过注册。
func DistFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil
	}
	return sub
}

// HasFrontend 判断嵌入的 dist 是否包含真实前端产物。
// 判据：dist/index.html 存在。
func HasFrontend() bool {
	sub := DistFS()
	if sub == nil {
		return false
	}
	_, err := fs.Stat(sub, "index.html")
	return err == nil
}
