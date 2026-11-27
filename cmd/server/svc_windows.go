//go:build windows

// Windows 服务模式（scripts/deploy/windows 的 sc.exe 部署链路依赖此实现）：
// sc.exe 注册的服务由 SCM 启动，进程必须实现 svc.Handler 响应控制协议，
// 否则启动超时报错 1053。控制台模式（不带 --svc）不受影响。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"

	"woodpecker/internal/config"
)

// svcName Windows 服务显示名/内部名（部署脚本 sc create 用同名）。
const svcName = "Woodpecker"

// runAsWindowsService SCM 入口：校验确由服务控制管理器拉起后进入服务循环。
func runAsWindowsService(mode string) error {
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("检测服务环境失败: %w", err)
	}
	if !isSvc {
		return fmt.Errorf("--svc 必须由 Windows 服务控制管理器启动（sc.exe 注册），控制台请直接运行不带 --svc")
	}
	return svc.Run(svcName, &woodpeckerService{mode: mode})
}

// woodpeckerService svc.Handler：StartPending → run()（复用控制台主流程）→ Running；
// 收到 Stop/Shutdown 时调用 svcCancel 触发 run 内部的优雅关闭链路（与 SIGTERM 一致）。
type woodpeckerService struct {
	mode string
}

func (s *woodpeckerService) Execute(_ []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (exitNow bool, exitCode uint32) {
	// SCM 启动的进程默认工作目录是 system32；切到 exe 所在目录，
	// 让 godotenv.Load() 能读到同目录的 .env（与 Linux systemd WorkingDirectory 等效）。
	if exe, err := os.Executable(); err == nil {
		_ = os.Chdir(filepath.Dir(exe))
	}
	status <- svc.Status{State: svc.StartPending}
	exit := make(chan int, 1)
	go func() { exit <- run(s.mode, config.Load()) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				if svcCancel != nil {
					svcCancel()
				}
			default:
				// Pause/Continue 等请求不接受（未声明 Accept），忽略。
			}
		case code := <-exit:
			return false, uint32(code)
		}
	}
}
