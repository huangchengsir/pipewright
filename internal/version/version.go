// Package version 暴露构建期注入的版本元数据。
//
// 这些变量由链接器在构建时经 -ldflags "-X" 覆盖(见 Makefile / .goreleaser.yaml）;
// 源码态(go run / 未注入构建)保持下方默认值,便于本地开发区分「正式版」与「开发态」。
package version

import (
	"fmt"
	"runtime"
)

// 构建期可注入变量。注入路径:
//
//	github.com/huangchengsir/pipewright/internal/version.Version=v1.2.3
//	github.com/huangchengsir/pipewright/internal/version.Commit=<git sha>
//	github.com/huangchengsir/pipewright/internal/version.Date=<RFC3339>
var (
	// Version 是语义化版本(发版 tag,如 v1.2.3);开发态为 "dev"。
	Version = "dev"
	// Commit 是构建所基于的 git 短/全 SHA;未注入为 "none"。
	Commit = "none"
	// Date 是构建时间(RFC3339);未注入为 "unknown"。
	Date = "unknown"
)

// Info 是 /version 端点与 --version 输出共享的结构化版本信息。
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get 返回当前构建的版本信息(含运行时 Go 版本与平台)。
func Get() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// String 是 `pipewright --version` 的人类可读单行输出。
func String() string {
	return fmt.Sprintf("pipewright %s (commit %s, built %s, %s, %s)",
		Version, Commit, Date, runtime.Version(), runtime.GOOS+"/"+runtime.GOARCH)
}
