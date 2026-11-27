#!/usr/bin/env bash
# Woodpecker 裸机/VM 首次安装（Linux + systemd）。
#
# 用法（root 或 sudo）：
#   sudo bash install.sh [--repo /path/to/woodpecker] [--install-dir /opt/woodpecker] [--data-dir /data/woodpecker]
#
# 流程：依赖检查 → 构建单二进制（内嵌前端）→ 系统用户/目录 → .env 就绪检查 →
#       systemd 注册 → 启动 → 健康检查。
# PostgreSQL/pgvector 与 Redis 需自行就绪（本机或远程），本脚本只校验连通性不代装。
set -euo pipefail

REPO_DIR="$(pwd)"
INSTALL_DIR="/opt/woodpecker"
DATA_DIR="/data/woodpecker"
APP_USER="woodpecker"
APP_PORT="${APP_PORT:-8080}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)       REPO_DIR="$2"; shift 2 ;;
    --install-dir) INSTALL_DIR="$2"; shift 2 ;;
    --data-dir)   DATA_DIR="$2"; shift 2 ;;
    *) echo "未知参数: $1"; exit 2 ;;
  esac
done

say()  { echo -e "\033[1;32m[install]\033[0m $*"; }
warn() { echo -e "\033[1;33m[warn]\033[0m $*"; }
die()  { echo -e "\033[1;31m[fail]\033[0m $*"; exit 1; }

[[ $EUID -eq 0 ]] || die "请用 root/sudo 运行（需创建系统用户与 systemd 单元）"
[[ -f "$REPO_DIR/go.mod" ]] || die "--repo 指向的不是仓库根目录：$REPO_DIR"

# ---- 依赖检查 ----
command -v go   >/dev/null || die "缺少 Go 1.27+（https://go.dev/dl/）"
command -v node >/dev/null || die "缺少 Node.js 22+（https://nodejs.org/）"
command -v systemctl >/dev/null || die "需要 systemd（本脚本仅支持 systemd 发行版）"
go version  | grep -q 'go1\.2[7-9]\|go1\.[3-9][0-9]' || warn "Go 版本偏低（$(go version)），要求 1.27+"
say "依赖检查通过：$(go version | cut -d' ' -f3) / node $(node --version)"

# ---- 构建单二进制（内嵌前端）----
say "构建中（npm ci + vite build + go build）..."
(cd "$REPO_DIR/web" && npm ci --silent)
(cd "$REPO_DIR/web" && npm run build --silent)
(cd "$REPO_DIR" && go run scripts/embed_web.go)
BIN_SRC="$REPO_DIR/bin/woodpecker"
(cd "$REPO_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$BIN_SRC" ./cmd/server)
[[ -x "$BIN_SRC" ]] || die "构建失败：未生成 $BIN_SRC"
say "构建完成：$BIN_SRC"

# ---- 系统用户与目录 ----
id -u "$APP_USER" &>/dev/null || useradd --system --home "$INSTALL_DIR" --shell /usr/sbin/nologin "$APP_USER"
mkdir -p "$INSTALL_DIR" "$DATA_DIR/uploads"
cp "$BIN_SRC" "$INSTALL_DIR/woodpecker"
chmod 750 "$INSTALL_DIR/woodpecker"

# ---- .env 就绪检查（godotenv 从工作目录读取）----
if [[ ! -f "$INSTALL_DIR/.env" ]]; then
  if [[ -f "$REPO_DIR/.env" ]]; then
    cp "$REPO_DIR/.env" "$INSTALL_DIR/.env"; say "已复用仓库现有 .env"
  elif [[ -f "$REPO_DIR/deploy/.env.production.example" ]]; then
    cp "$REPO_DIR/deploy/.env.production.example" "$INSTALL_DIR/.env"
    warn "已放置模板 $INSTALL_DIR/.env —— 必须编辑替换全部 CHANGE_ME（JWT_SECRET/ENCRYPTION_KEY/数据库），"
    warn "否则 APP_ENV=production 下启动会被配置校验拒绝。编辑后重新运行本脚本或 systemctl restart woodpecker。"
  fi
fi
chown -R "$APP_USER:$APP_USER" "$INSTALL_DIR" "$DATA_DIR"

# ---- systemd 注册 ----
sed -e "s|__APP_USER__|$APP_USER|g" \
    -e "s|__INSTALL_DIR__|$INSTALL_DIR|g" \
    -e "s|__DATA_DIR__|$DATA_DIR|g" \
    "$REPO_DIR/scripts/deploy/linux/woodpecker.service" > /etc/systemd/system/woodpecker.service
systemctl daemon-reload
systemctl enable --now woodpecker

# ---- 健康检查 ----
sleep 2
for i in $(seq 1 10); do
  if curl -fsS "http://127.0.0.1:${APP_PORT}/api/v1/healthz" >/dev/null 2>&1; then
    say "服务已就绪：http://<本机>:${APP_PORT}（前端已内嵌，浏览器直接访问）"
    say "常用命令：systemctl status/restart/stop woodpecker · 升级用 upgrade.sh · 卸载用 uninstall.sh"
    exit 0
  fi
  sleep 2
done
warn "健康检查未通过，查看日志：journalctl -u woodpecker -n 50 --no-pager"
exit 1
