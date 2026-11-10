package main

import (
	_ "embed"
	"order_server/go/server/version"
)

// CHANGELOG.md 是版本更新日志的唯一数据源：
//   - 发版脚本 scripts/publish-release.ps1 读取它，写入 GitHub Release 说明；
//   - 这里用 go:embed 编译进 exe，供顶栏悬停预览与「版本更新历史」弹窗离线展示。
//
// 两处共用同一份文件，不会出现「写了日志但界面看不到」的脱节。
//
//go:embed CHANGELOG.md
var changelogMarkdown string

func init() {
	version.SetChangelog(changelogMarkdown)
}
