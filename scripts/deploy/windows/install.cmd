@echo off
REM Woodpecker 裸机部署（Windows 服务，cmd 版；功能与 install.ps1 等价）。
REM 用法（管理员 cmd）：install.cmd [Repo目录] [安装目录] [数据目录] [端口]
REM   缺省：当前目录 / C:\Program Files\Woodpecker / C:\ProgramData\Woodpecker / 8080

setlocal enabledelayedexpansion
chcp 65001 >nul

set "REPO=%~1"
if "%REPO%"=="" set "REPO=%CD%"
set "INSTALL_DIR=%~2"
if "%INSTALL_DIR%"=="" set "INSTALL_DIR=C:\Program Files\Woodpecker"
set "DATA_DIR=%~3"
if "%DATA_DIR%"=="" set "DATA_DIR=C:\ProgramData\Woodpecker"
set "PORT=%~4"
if "%PORT%"=="" set "PORT=8080"

REM ---- 管理员检查 ----
net session >nul 2>&1
if errorlevel 1 (
  echo [fail] 请以管理员身份运行 cmd
  exit /b 1
)

if not exist "%REPO%\go.mod" (
  echo [fail] 参数一指向的不是仓库根目录：%REPO%
  exit /b 1
)

REM ---- 依赖检查 ----
where go >nul 2>&1 || (echo [fail] 缺少 Go 1.27+ & exit /b 1)
where node >nul 2>&1 || (echo [fail] 缺少 Node.js 22+ & exit /b 1)
echo [install] 依赖检查通过

REM ---- 构建单二进制（内嵌前端）----
echo [install] 构建中（npm ci + vite build + go build）...
pushd "%REPO%\web"
call npm ci --silent || (popd & echo [fail] npm ci 失败 & exit /b 1)
call npm run build --silent || (popd & echo [fail] 前端构建失败 & exit /b 1)
popd
pushd "%REPO%"
go run scripts/embed_web.go || (popd & echo [fail] 拷贝 embed 产物失败 & exit /b 1)
set CGO_ENABLED=0
go build -trimpath -ldflags "-s -w" -o "%REPO%\bin\woodpecker.exe" .\cmd\server || (popd & echo [fail] 后端构建失败 & exit /b 1)
popd
if not exist "%REPO%\bin\woodpecker.exe" (
  echo [fail] 构建失败：未生成 %REPO%\bin\woodpecker.exe
  exit /b 1
)
echo [install] 构建完成：%REPO%\bin\woodpecker.exe

REM ---- 目录与 .env ----
mkdir "%INSTALL_DIR%" 2>nul
mkdir "%DATA_DIR%\uploads" 2>nul
copy /y "%REPO%\bin\woodpecker.exe" "%INSTALL_DIR%\woodpecker.exe" >nul
if not exist "%INSTALL_DIR%\.env" (
  if exist "%REPO%\.env" (
    copy /y "%REPO%\.env" "%INSTALL_DIR%\.env" >nul
    echo [install] 已复用仓库现有 .env
  ) else if exist "%REPO%\deploy\.env.production.example" (
    copy /y "%REPO%\deploy\.env.production.example" "%INSTALL_DIR%\.env" >nul
    echo [warn] 已放置模板 %INSTALL_DIR%\.env —— 必须编辑替换全部 CHANGE_ME（JWT_SECRET/ENCRYPTION_KEY/数据库），
    echo [warn] 否则 APP_ENV=production 下启动会被配置校验拒绝。编辑后重新运行本脚本或 sc.exe stop/start Woodpecker。
  )
)

REM ---- sc.exe 注册服务 ----
sc.exe create Woodpecker binPath= "\"%INSTALL_DIR%\woodpecker.exe\" --svc" start= auto DisplayName= "Woodpecker AI knowledge base" >nul
if errorlevel 1 (
  echo [fail] sc.exe create 失败；若已存在同名服务请先运行 uninstall.cmd
  exit /b 1
)
sc.exe description Woodpecker "Woodpecker 企业知识库问答（api+worker 单进程，内嵌前端）" >nul
sc.exe failure Woodpecker reset= 86400 actions= restart/60000 >nul
echo [install] 服务已注册（Woodpecker，开机自启，失败自动重启）

REM 记录安装/数据目录（upgrade.cmd / uninstall.cmd 用于定位非默认路径）
reg add "HKLM\SOFTWARE\Woodpecker" /v InstallDir /t REG_SZ /d "%INSTALL_DIR%" /f >nul
reg add "HKLM\SOFTWARE\Woodpecker" /v DataDir /t REG_SZ /d "%DATA_DIR%" /f >nul

REM ---- 防火墙 ----
netsh advfirewall firewall delete rule name="Woodpecker" >nul 2>&1
netsh advfirewall firewall add rule name="Woodpecker" dir=in action=allow protocol=TCP localport=%PORT% >nul
echo [install] 防火墙规则已添加（TCP %PORT%）

REM ---- 启动与健康检查 ----
sc.exe start Woodpecker >nul
timeout /t 3 /nobreak >nul
set OK=0
for /l %%i in (1,1,10) do (
  if !OK!==0 (
    curl -fsS -o nul http://127.0.0.1:%PORT%/api/v1/healthz 2>nul && set OK=1
    if !OK!==0 timeout /t 2 /nobreak >nul
  )
)
if %OK%==1 (
  echo [install] 服务已就绪：http://本机:%PORT%（前端已内嵌，浏览器直接访问）
  echo [install] 常用命令：sc.exe query/stop/start Woodpecker · 升级 upgrade.cmd · 卸载 uninstall.cmd
  exit /b 0
)
echo [warn] 健康检查未通过。服务日志：事件查看器或控制台直跑 "%INSTALL_DIR%\woodpecker.exe" 看输出。
exit /b 1
