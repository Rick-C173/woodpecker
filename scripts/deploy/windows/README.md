# Windows 部署脚本说明

两版脚本行为完全一致，按使用习惯任选其一（均需**管理员权限**）：

| 操作 | pwsh | cmd |
|---|---|---|
| 安装 | `.\install.ps1 [-Repo ...] [-InstallDir ...] [-DataDir ...] [-Port 8080]` | `install.cmd [Repo] [安装目录] [数据目录] [端口]` |
| 升级 | `.\upgrade.ps1 [-Repo ...] [-SkipPull]` | `upgrade.cmd [Repo] [/skip-pull]` |
| 卸载 | `.\uninstall.ps1 [-Purge]` | `uninstall.cmd [/purge]` |

pwsh 策略受限时可用：`powershell -ExecutionPolicy Bypass -File .\install.ps1`。

## 要点

- **服务**：`Woodpecker`（sc.exe 注册，开机自启，失败 1 分钟后自动重启）。进程以 `--svc` 模式
  响应 SCM 控制协议（cmd/server/svc_windows.go），Stop 走与应用内 SIGTERM 相同的优雅关闭链路。
- **配置**：`.env` 放在二进制同目录（服务启动自动切换工作目录到 exe 所在目录后由 godotenv 读取）。
  模板 `deploy/.env.production.example`，`CHANGE_ME` 必须替换，否则生产模式拒绝启动。
- **数据**：`<DataDir>\uploads`（上传/staging 目录，api 与 worker 共用同进程无需挂载）。
  卸载默认保留数据目录与数据库，`/purge`（`-Purge`）才删数据；PostgreSQL 始终手动清理。
- **安装目录记录**：写注册表 `HKLM\SOFTWARE\Woodpecker`（InstallDir/DataDir），upgrade/uninstall
  据此定位非默认路径。
- **端口**：默认 8080，install 时可用参数改；脚本同步加防火墙入站规则。
- **日志**：默认输出到 Windows 事件流（控制台模式）；排查问题时控制台直跑
  `<安装目录>\woodpecker.exe`（不带 --svc）看实时输出，或 `.env` 设 `LOG_TO_FILE=true` 落盘。
- **前端**：内嵌在二进制里（构建链路自动执行 npm build → embed → go build），浏览器直接访问
  `http://<本机>:8080`。如企业已有 nginx/网关托管前端，见 `../nginx/woodpecker.conf.template`。
