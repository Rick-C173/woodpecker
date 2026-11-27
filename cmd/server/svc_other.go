//go:build !windows

package main

import "errors"

// 非 Windows 平台对 svcCancel 的编译期引用：svcCancel 仅被 svc_windows.go
// （Windows 构建）读取，Linux/CI 的静态分析视角下会误报 unused——此处取址
// 引用保持全平台一致的可用性契约。
var _ = &svcCancel

// runAsWindowsService 非 Windows 平台的占位实现：服务化部署走 systemd
// （模板 scripts/deploy/linux/woodpecker.service），此处给出明确指引而非静默失败。
func runAsWindowsService(string) error {
	return errors.New("--svc 仅支持 Windows（Linux 服务化部署用 systemd：scripts/deploy/linux/install.sh）")
}
