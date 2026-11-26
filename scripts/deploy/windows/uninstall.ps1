# Woodpecker 卸载（Windows 服务，pwsh 版；cmd 等价版见 uninstall.cmd）。
# 停服并删除服务、防火墙规则与安装目录；数据默认保留，-Purge 才删数据目录，数据库始终手动清理。
#Requires -RunAsAdministrator
param(
  [string]$InstallDir = "$env:ProgramFiles\Woodpecker",
  [string]$DataDir = "$env:ProgramData\Woodpecker",
  [switch]$Purge
)

$ErrorActionPreference = 'SilentlyContinue'
function Say($m) { Write-Host "[uninstall] $m" -ForegroundColor Green }
function Warn($m) { Write-Host "[warn] $m" -ForegroundColor Yellow }

$s = sc.exe query Woodpecker
if ($s -match 'STOPPED') { Say "服务已停止" } elseif ($LASTEXITCODE) { Warn "服务未注册" } else {
  sc.exe stop Woodpecker | Out-Null
  for ($i = 0; $i -lt 20; $i++) {
    $s = (sc.exe query Woodpecker | Select-String 'STATE').ToString()
    if ($s -match 'STOPPED') { break }
    Start-Sleep -Seconds 1
  }
  Say "服务已停止"
}

sc.exe delete Woodpecker | Out-Null
Say "服务已注销"
netsh advfirewall firewall delete rule name="Woodpecker" | Out-Null
Say "防火墙规则已删除"

if (Test-Path $InstallDir) {
  Remove-Item $InstallDir -Recurse -Force
  Say "安装目录已删除：$InstallDir"
}
if ($Purge) {
  Remove-Item $DataDir -Recurse -Force
  Say "数据目录已删除：$DataDir（-Purge）"
} else {
  Warn "数据目录保留：$DataDir（上传文件/日志；加 -Purge 才删除）"
}

# 注册表记录的安装目录（upgrade.cmd 用于定位非默认安装路径）
Remove-Item 'HKLM:\SOFTWARE\Woodpecker' -Recurse -Force -ErrorAction SilentlyContinue

Warn "数据库未触碰：如需彻底清理请手动 DROP DATABASE woodpecker（含 pgvector 扩展）。"
