// Package pipewright is the module root. It embeds the built frontend (web/dist)
// so the whole platform ships as a single static binary (go:embed).
package pipewright

import (
	"embed"
	"io/fs"
)

//go:embed all:web/dist
var distFS embed.FS

// WebFS 返回内嵌前端 SPA 的文件系统(根为 web/dist)。
func WebFS() fs.FS {
	sub, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		// web/dist 在编译期由 //go:embed 固化;出错说明构建产物缺失,属编程/构建错误。
		panic(err)
	}
	return sub
}
