# Woodpecker 升级（Windows 服务，pwsh 版；cmd 等价版见 upgrade.cmd）。
# 停服 → git pull → 重新构建（内嵌前端）→ 换二进制 → 起服（迁移随启动自动执行）。
#Requires -RunAsAdministrator
param(
  [string]$Repo = (Get-Location).Path,
  [string]$InstallDir = "$env:ProgramFiles\Woodpecker",
  [switch]$SkipPull
)

$ErrorActionPreference = 'Stop'
function Say($m) { Write-Host "[upgrade] $m" -ForegroundColor Green }
function Die($m) { Write-Host "[fail] $m" -ForegroundColor Red; exit 1 }

if (-not (Test-Path "$InstallDir\woodpecker.exe")) { Die "未检测到已安装服务（先运行 install.ps1）" }
if (-not (Test-Path "$Repo\go.mod")) { Die "-Repo 指向的不是仓库根目录：$Repo" }

if (-not $SkipPull) {
  Say "拉取最新代码（git pull）..."
  Push-Location $Repo
  try { git pull --ff-only; if ($LASTEXITCODE) { Die "git pull 失败" } } finally { Pop-Location }
}

Say "重新构建（npm + go）..."
Push-Location "$Repo\web"
try {
  npm ci --silent; if ($LASTEXITCODE) { Die "npm ci 失败" }
  npm run build --silent; if ($LASTEXITCODE) { Die "前端构建失败" }
} finally { Pop-Location }
Push-Location $Repo
try {
  go run scripts/embed_web.go; if ($LASTEXITCODE) { Die "拷贝 embed 产物失败" }
  $env:CGO_ENABLED = '0'
  go build -trimpath -ldflags "-s -w" -o "$Repo\bin\woodpecker.exe" .\cmd\server
  if ($LASTEXITCODE) { Die "后端构建失败" }
} finally { Pop-Location }

Say "停服 → 换二进制 → 起服（迁移随启动自动执行）..."
sc.exe stop Woodpecker | Out-Null
# 等待服务真正停止（STOP_PENDING → STOPPED）
for ($i = 0; $i -lt 20; $i++) {
  $s = (sc.exe query Woodpecker | Select-String 'STATE').ToString()
  if ($s -match 'STOPPED') { break }
  Start-Sleep -Seconds 1
}
Copy-Item "$Repo\bin\woodpecker.exe" "$InstallDir\woodpecker.exe" -Force
sc.exe start Woodpecker | Out-Null

Start-Sleep -Seconds 3
for ($i = 1; $i -le 10; $i++) {
  try {
    Invoke-WebRequest -Uri "http://127.0.0.1:8080/api/v1/healthz" -UseBasicParsing -TimeoutSec 3 | Out-Null
    Say "升级完成。"
    exit 0
  } catch { Start-Sleep -Seconds 2 }
}
Write-Host "[warn] 健康检查未通过（应用日志/事件查看器排查）。" -ForegroundColor Yellow
exit 1
