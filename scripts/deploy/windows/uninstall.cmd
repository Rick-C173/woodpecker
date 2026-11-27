@echo off
REM Woodpecker 卸载（Windows 服务，cmd 版；功能与 uninstall.ps1 等价）。
REM 用法（管理员 cmd）：uninstall.cmd [/purge]
REM   数据目录默认保留；/purge 才删除；数据库始终手动清理。

setlocal
chcp 65001 >nul
set "PURGE=0"
if /i "%~1"=="/purge" set PURGE=1

net session >nul 2>&1 || (echo [fail] 请以管理员身份运行 cmd & exit /b 1)

set "INSTALL_DIR=C:\Program Files\Woodpecker"
set "DATA_DIR=C:\ProgramData\Woodpecker"
for /f "tokens=2*" %%a in ('reg query "HKLM\SOFTWARE\Woodpecker" /v InstallDir 2^>nul') do set "INSTALL_DIR=%%b"
for /f "tokens=2*" %%a in ('reg query "HKLM\SOFTWARE\Woodpecker" /v DataDir 2^>nul') do set "DATA_DIR=%%b"

REM 无论注册与否都执行 stop/delete（未注册时静默失败，等效幂等）
sc.exe stop Woodpecker >nul 2>&1
for /l %%i in (1,1,20) do (
  sc.exe query Woodpecker 2>nul | findstr /c:"STOPPED" >nul && goto stopped
  timeout /t 1 /nobreak >nul
)
:stopped
echo [uninstall] 服务已停止并注销
sc.exe delete Woodpecker >nul 2>&1
netsh advfirewall firewall delete rule name="Woodpecker" >nul 2>&1
echo [uninstall] 防火墙规则已删除

if exist "%INSTALL_DIR%" (
  rmdir /s /q "%INSTALL_DIR%"
  echo [uninstall] 安装目录已删除：%INSTALL_DIR%
)
if %PURGE%==1 (
  rmdir /s /q "%DATA_DIR%"
  echo [uninstall] 数据目录已删除：%DATA_DIR%（/purge）
) else (
  echo [warn] 数据目录保留：%DATA_DIR%（上传文件/日志；加 /purge 才删除）
)
reg delete "HKLM\SOFTWARE\Woodpecker" /f >nul 2>&1
echo [warn] 数据库未触碰：如需彻底清理请手动 DROP DATABASE woodpecker（含 pgvector 扩展）。
exit /b 0
