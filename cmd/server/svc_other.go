//go:build !windows

package main

import "errors"

// runAsWindowsService 非 Windows 平台的占位实现：服务化部署走 systemd
// （模板 scripts/deploy/linux/woodpecker.service），此处给出明确指引而非静默失败。
func runAsWindowsService(string) error {
	return errors.New("--svc 仅支持 Windows（Linux 服务化部署用 systemd：scripts/deploy/linux/install.sh）")
}
