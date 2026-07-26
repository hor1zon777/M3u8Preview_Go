// Package version 承载构建时注入的版本信息。
//
// 单一事实来源是仓库根目录的 VERSION 文件：Dockerfile 在编译阶段读它，
// 连同 git commit 短哈希与构建时间一起通过 -ldflags 注入下面三个变量。
//
// 为什么不用 go:embed 直接读 VERSION：embed 无法跨越包目录向上引用父目录文件，
// 把 VERSION 挪进本包又会让它从"仓库级版本"退化成"某个包的私有文件"，
// 与前端、镜像标签共用同一版本号的意图不符。
//
// 本地 `go run` 不注入时三个值保持默认（dev / unknown），这是刻意的：
// 显示 "dev" 比显示一个来路不明的版本号诚实。要在本地构建出带版本的二进制：
//
//	go build -ldflags "-X github.com/hor1zon777/m3u8-preview-go/internal/version.Version=$(cat VERSION) \
//	                   -X github.com/hor1zon777/m3u8-preview-go/internal/version.Commit=$(git rev-parse --short HEAD)" \
//	         ./cmd/server
package version

// 以下三个变量由 -ldflags -X 注入，不要在代码里赋值。
var (
	// Version 语义化版本号，取自仓库根目录 VERSION 文件。
	Version = "dev"
	// Commit git 提交短哈希。
	Commit = "unknown"
	// BuildTime 构建时刻（RFC3339，UTC）。
	BuildTime = "unknown"
)

// String 返回适合日志与人读的完整版本描述，形如 "0.1.0 (877c77a)"。
func String() string {
	if Commit == "unknown" || Commit == "" {
		return Version
	}
	return Version + " (" + Commit + ")"
}
