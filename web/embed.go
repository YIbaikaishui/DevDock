// Package web 把前端静态资源编译进 exe。
// 最终产物因此无需伴随任何外部文件，也不需要 Node 或任何前端构建步骤。
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed index.html style.css app.js favicon.svg
var files embed.FS

// FS 返回前端资源文件系统。
func FS() fs.FS {
	sub, err := fs.Sub(files, ".")
	if err != nil {
		panic("无法加载内嵌前端资源: " + err.Error())
	}
	return sub
}

// Handler 返回前端静态资源处理器。
func Handler() http.Handler {
	return http.FileServer(http.FS(FS()))
}
