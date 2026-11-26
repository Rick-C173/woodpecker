#!/usr/bin/env bash
# Woodpecker 卸载（Linux）：停服并移除 systemd 单元、安装目录与系统用户。
# 数据默认保留；--purge 才删除数据目录，数据库（PostgreSQL）始终需手动清理。
#
# 用法：sudo bash uninstall.sh [--install-dir /opt/woodpecker] [--data-dir /data/woodpecker] [--purge]
set -euo pipefail

INSTALL_DIR="/opt/woodpecker"
DATA_DIR="/data/woodpecker"
APP_USER="woodpecker"
PURGE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --install-dir) INSTALL_DIR="$2"; shift 2 ;;
    --data-dir)    DATA_DIR="$2"; shift 2 ;;
    --purge)       PURGE=1; shift ;;
    *) echo "未知参数: $1"; exit 2 ;;
  esac
done

say()  { echo -e "\033[1;32m[uninstall]\033[0m $*"; }
warn() { echo -e "\033[1;33m[warn]\033[0m $*"; }

[[ $EUID -eq 0 ]] || { echo "请用 root/sudo 运行"; exit 1; }

systemctl stop woodpecker 2>/dev/null && say "服务已停止" || warn "服务未在运行"
systemctl disable woodpecker 2>/dev/null || true
rm -f /etc/systemd/system/woodpecker.service
systemctl daemon-reload
say "systemd 单元已移除"

rm -rf "$INSTALL_DIR" && say "安装目录已删除：$INSTALL_DIR"

if [[ $PURGE -eq 1 ]]; then
  rm -rf "$DATA_DIR" && say "数据目录已删除：$DATA_DIR（--purge）"
else
  warn "数据目录保留：$DATA_DIR（上传文件/日志；加 --purge 才删除）"
fi

if id "$APP_USER" &>/dev/null; then
  userdel "$APP_USER" && say "系统用户已删除：$APP_USER"
fi

warn "数据库未触碰：如需彻底清理请手动 DROP DATABASE woodpecker（含 pgvector 扩展）。"
