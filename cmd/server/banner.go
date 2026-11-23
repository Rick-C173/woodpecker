// 启动横幅：内容来自仓库根目录的 banner 文件（用户自定义 ASCII art）。
// 读取失败或文件缺失时静默跳过（banner 为可选装饰，不影响启动）；
// 受配置 BANNER_ENABLED 控制（internal/config）。
package main

import (
	"fmt"
	"os"

	"woodpecker/internal/config"
)

// bannerFile 用户自定义 banner 文件路径（相对服务启动目录）。
const bannerFile = "banner"

// printBanner 打印用户自定义 banner；BANNER_ENABLED=false 时关闭。
func printBanner(cfg *config.Config) {
	if !cfg.BannerEnabled {
		return
	}
	data, err := os.ReadFile(bannerFile)
	if err != nil {
		return // 无 banner 文件：静默跳过
	}
	fmt.Println()
	fmt.Print(string(data))
	fmt.Println()
}
