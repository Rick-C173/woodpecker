# Woodpecker 裸机部署（Windows 服务，pwsh 版；cmd 等价版见 install.cmd）。
#
# 用法（管理员 PowerShell）：
#   .\install.ps1 [-Repo D:\src\woodpecker] [-InstallDir "C:\Program Files\Woodpecker"] [-DataDir D:\woodpecker] [-Port 8080]
#
# 流程：依赖检查 → 构建单二进制（内嵌前端）→ 安装目录 → .env 就绪检查 →
#       sc.exe 注册服务（--svc 模式，SCM 启停）→ 防火墙 → 启动 → 健康检查。
# PostgreSQL/pgvector 与 Redis 需自行就绪，本脚本只校验连通性不代装。
#Requires -RunAsAdministrator
param(
  [string]$Repo = (Get-Location).Path,
  [string]$InstallDir = "$env:ProgramFiles\Woodpecker",
  [string]$DataDir = "$env:ProgramData\Woodpecker",
  [int]$Port = 8080
)

$ErrorActionPreference = 'Stop'
function Say($m) { Write-Host "[install] $m" -ForegroundColor Green }
function Warn($m) { Write-Host "[warn] $m" -ForegroundColor Yellow }
function Die($m) { Write-Host "[fail] $m" -ForegroundColor Red; exit 1 }

if (-not (Test-Path "$Repo\go.mod")) { Die "-Repo 指向的不是仓库根目录：$Repo" }

# ---- 依赖检查 ----
foreach ($tool in 'go', 'node', 'npm', 'git') {
  if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { Die "缺少 $tool（Go 1.27+ / Node 22+）" }
}
Say "依赖检查通过：go $((go version) -replace 'go version ','') / node $(node --version)"

# ---- 构建单二进制（内嵌前端）----
Say "构建中（npm ci + vite build + go build）..."
Push-Location "$Repo\web"
try {
  npm ci --silent
  if ($LASTEXITCODE) { Die "npm ci 失败" }
  npm run build --silent
  if ($LASTEXITCODE) { Die "前端构建失败" }
} finally { Pop-Location }

Push-Location $Repo
try {
  go run scripts/embed_web.go
  if ($LASTEXITCODE) { Die "拷贝 embed 产物失败" }
  $env:CGO_ENABLED = '0'
  go build -trimpath -ldflags "-s -w" -o "$Repo\bin\woodpecker.exe" .\cmd\server
  if ($LASTEXITCODE) { Die "后端构建失败" }
} finally { Pop-Location }

$BinSrc = "$Repo\bin\woodpecker.exe"
if (-not (Test-Path $BinSrc)) { Die "构建失败：未生成 $BinSrc" }
Say "构建完成：$BinSrc"

# ---- 目录与 .env ----
New-Item -ItemType Directory -Force -Path $InstallDir, "$DataDir\uploads" | Out-Null
Copy-Item $BinSrc "$InstallDir\woodpecker.exe" -Force

if (-not (Test-Path "$InstallDir\.env")) {
  if (Test-Path "$Repo\.env") {
    Copy-Item "$Repo\.env" "$InstallDir\.env"; Say "已复用仓库现有 .env"
  } elseif (Test-Path "$Repo\deploy\.env.production.example") {
    Copy-Item "$Repo\deploy\.env.production.example" "$InstallDir\.env"
    Warn "已放置模板 $InstallDir\.env —— 必须编辑替换全部 CHANGE_ME（JWT_SECRET/ENCRYPTION_KEY/数据库），"
    Warn "否则 APP_ENV=production 下启动会被配置校验拒绝。编辑后重新运行本脚本或 sc.exe stop/start Woodpecker。"
  }
}

# ---- sc.exe 注册服务（--svc：进程实现 SCM 控制协议）----
sc.exe create Woodpecker binPath= "`"$InstallDir\woodpecker.exe`" --svc" start= auto DisplayName= "Woodpecker AI knowledge base" | Out-Null
if ($LASTEXITCODE) { Die "sc.exe create 失败（退出码 $LASTEXITCODE）；若已存在同名服务请先运行 uninstall.ps1" }
sc.exe description Woodpecker "Woodpecker 企业知识库问答（api+worker 单进程，内嵌前端）" | Out-Null
# 失败自动重启：1 分钟后重试，86400 秒（1 天）后重置失败计数
sc.exe failure Woodpecker reset= 86400 actions= restart/60000 | Out-Null
Say "服务已注册（Woodpecker，开机自启，失败自动重启）"

# 记录安装/数据目录（upgrade.cmd / uninstall.cmd 用于定位非默认路径）
New-Item -Path 'HKLM:\SOFTWARE\Woodpecker' -Force | Out-Null
New-ItemProperty -Path 'HKLM:\SOFTWARE\Woodpecker' -Name InstallDir -Value $InstallDir -PropertyType String -Force | Out-Null
New-ItemProperty -Path 'HKLM:\SOFTWARE\Woodpecker' -Name DataDir -Value $DataDir -PropertyType String -Force | Out-Null

# ---- 防火墙 ----
netsh advfirewall firewall delete rule name="Woodpecker" | Out-Null
netsh advfirewall firewall add rule name="Woodpecker" dir=in action=allow protocol=TCP localport=$Port | Out-Null
Say "防火墙规则已添加（TCP $Port）"

# ---- 启动与健康检查 ----
sc.exe start Woodpecker | Out-Null
Start-Sleep -Seconds 3
for ($i = 1; $i -le 10; $i++) {
  try {
    Invoke-WebRequest -Uri "http://127.0.0.1:$Port/api/v1/healthz" -UseBasicParsing -TimeoutSec 3 | Out-Null
    Say "服务已就绪：http://<本机>:$Port（前端已内嵌，浏览器直接访问）"
    Say "常用命令：sc.exe query/stop/start Woodpecker · 升级 upgrade.ps1/.cmd · 卸载 uninstall.ps1/.cmd"
    exit 0
  } catch { Start-Sleep -Seconds 2 }
}
Warn "健康检查未通过。服务日志：Windows 事件查看器 + 应用日志（.env 配置 LOG_TO_FILE=true 落盘）；或控制台直跑 `"$InstallDir\woodpecker.exe`" 看输出。"
exit 1
