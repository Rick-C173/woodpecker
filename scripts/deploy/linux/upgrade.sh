#!/usr/bin/env bash
# Woodpecker 升级（Linux）：拉取最新代码 → 重新构建（内嵌前端）→ 停服换二进制 → 起服。
# 迁移在服务启动时自动执行（内嵌 golang-migrate），数据不动。
#
# 用法：sudo bash upgrade.sh [--repo /path/to/woodpecker] [--skip-pull]
set -euo pipefail

REPO_DIR="$(pwd)"
INSTALL_DIR="/opt/woodpecker"
SKIP_PULL=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)      REPO_DIR="$2"; shift 2 ;;
    --skip-pull) SKIP_PULL=1; shift ;;
    *) echo "未知参数: $1"; exit 2 ;;
  esac
done

say()  { echo -e "\033[1;32m[upgrade]\033[0m $*"; }
die()  { echo -e "\033[1;31m[fail]\033[0m $*"; exit 1; }

[[ $EUID -eq 0 ]] || die "请用 root/sudo 运行"
[[ -x "$INSTALL_DIR/woodpecker" ]] || die "未检测到已安装服务（先运行 install.sh）"
[[ -f "$REPO_DIR/go.mod" ]] || die "--repo 指向的不是仓库根目录：$REPO_DIR"

if [[ $SKIP_PULL -eq 0 ]]; then
  say "拉取最新代码（git pull）..."
  (cd "$REPO_DIR" && git pull --ff-only)
fi

say "重新构建（npm + go）..."
(cd "$REPO_DIR/web" && npm ci --silent)
(cd "$REPO_DIR/web" && npm run build --silent)
(cd "$REPO_DIR" && go run scripts/embed_web.go)
(cd "$REPO_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$REPO_DIR/bin/woodpecker" ./cmd/server)

say "停服 → 换二进制 → 起服（迁移随启动自动执行）..."
systemctl stop woodpecker
install -m 750 -o woodpecker -g woodpecker "$REPO_DIR/bin/woodpecker" "$INSTALL_DIR/woodpecker"
systemctl start woodpecker

sleep 2
for i in $(seq 1 10); do
  if curl -fsS "http://127.0.0.1:8080/api/v1/healthz" >/dev/null 2>&1; then
    say "升级完成：$(systemctl show -p ActiveState --value woodpecker)"
    exit 0
  fi
  sleep 2
done
echo -e "\033[1;33m[warn]\033[0m 健康检查未通过：journalctl -u woodpecker -n 50 --no-pager"
exit 1
