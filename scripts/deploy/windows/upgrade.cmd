@echo off
REM Woodpecker 升级（Windows 服务，cmd 版；功能与 upgrade.ps1 等价）。
REM 用法（管理员 cmd）：upgrade.cmd [Repo目录] [/skip-pull]
REM   缺省 Repo=当前目录；传 /skip-pull 跳过 git pull（用本地已就绪的代码构建）。

setlocal enabledelayedexpansion
chcp 65001 >nul

set "REPO=%~1"
if "%REPO%"=="" set "REPO=%CD%"
if "%REPO%"=="/skip-pull" set "REPO=%CD%"
set "SKIP_PULL=0"
if /i "%~2"=="/skip-pull" set SKIP_PULL=1
if /i "%~1"=="/skip-pull" set SKIP_PULL=1

set "INSTALL_DIR=C:\Program Files\Woodpecker"
for /f "tokens=2*" %%a in ('reg query "HKLM\SOFTWARE\Woodpecker" /v InstallDir 2^>nul') do set "INSTALL_DIR=%%b"

net session >nul 2>&1 || (echo [fail] 请以管理员身份运行 cmd & exit /b 1)
if not exist "%INSTALL_DIR%\woodpecker.exe" (echo [fail] 未检测到已安装服务（先运行 install.cmd） & exit /b 1)
if not exist "%REPO%\go.mod" (echo [fail] 参数一指向的不是仓库根目录：%REPO% & exit /b 1)

if %SKIP_PULL%==0 (
  echo [upgrade] 拉取最新代码（git pull）...
  pushd "%REPO%"
  git pull --ff-only || (popd & echo [fail] git pull 失败 & exit /b 1)
  popd
)

echo [upgrade] 重新构建（npm + go）...
pushd "%REPO%\web"
call npm ci --silent || (popd & echo [fail] npm ci 失败 & exit /b 1)
call npm run build --silent || (popd & echo [fail] 前端构建失败 & exit /b 1)
popd
pushd "%REPO%"
go run scripts/embed_web.go || (popd & echo [fail] 拷贝 embed 产物失败 & exit /b 1)
set CGO_ENABLED=0
go build -trimpath -ldflags "-s -w" -o "%REPO%\bin\woodpecker.exe" .\cmd\server || (popd & echo [fail] 后端构建失败 & exit /b 1)
popd

echo [upgrade] 停服 → 换二进制 → 起服（迁移随启动自动执行）...
sc.exe stop Woodpecker >nul
REM 等待服务真正停止（最多 20 秒）
set STOPPED=0
for /l %%i in (1,1,20) do (
  if !STOPPED!==0 (
    sc.exe query Woodpecker | findstr /c:"STOPPED" >nul && set STOPPED=1
    if !STOPPED!==0 timeout /t 1 /nobreak >nul
  )
)
copy /y "%REPO%\bin\woodpecker.exe" "%INSTALL_DIR%\woodpecker.exe" >nul
sc.exe start Woodpecker >nul

timeout /t 3 /nobreak >nul
set OK=0
for /l %%i in (1,1,10) do (
  if !OK!==0 (
    curl -fsS -o nul http://127.0.0.1:8080/api/v1/healthz 2>nul && set OK=1
    if !OK!==0 timeout /t 2 /nobreak >nul
  )
)
if %OK%==1 (
  echo [upgrade] 升级完成。
  exit /b 0
)
echo [warn] 健康检查未通过（应用日志/事件查看器排查）。
exit /b 1
