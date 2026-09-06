#!/usr/bin/env bash
# 千机台 rc-agent 一键装机脚本(root 执行)
#
# 用法:
#   ./install-agent.sh <SERVER_WS_URL> <AGENT_TOKEN>
#   ./install-agent.sh wss://rc.example.com/ws/agent 'MyToken'
#
# 可选环境变量:
#   RC_BIN     rc-agent 二进制路径(默认自动查找 dist/rc-agent-linux-<arch>)
#   RC_NAME    主机展示名(默认主机名)
set -euo pipefail

if [ $# -lt 2 ]; then
  echo "用法: $0 <SERVER_WS_URL> <AGENT_TOKEN>" >&2
  echo "示例: $0 wss://rc.example.com/ws/agent 'MyToken'" >&2
  exit 1
fi
SERVER_URL="$1"
AGENT_TOKEN="$2"
RC_NAME="${RC_NAME:-}"

if [ "$(id -u)" -ne 0 ]; then
  echo "请以 root 运行(需写入 /usr/local/bin 与 systemd)" >&2
  exit 1
fi

# ---- 定位二进制 ----
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "不支持的架构: $ARCH" >&2; exit 1 ;;
esac
if [ -z "${RC_BIN:-}" ]; then
  SELF_DIR="$(cd "$(dirname "$0")" && pwd)"
  for cand in "$SELF_DIR/rc-agent-linux-$ARCH" "$SELF_DIR/../dist/rc-agent-linux-$ARCH" /usr/local/bin/rc-agent; do
    if [ -f "$cand" ]; then RC_BIN="$cand"; break; fi
  done
fi
if [ -z "${RC_BIN:-}" ] || [ ! -f "$RC_BIN" ]; then
  echo "未找到 rc-agent 二进制(可用 RC_BIN 指定路径)" >&2
  exit 1
fi

echo "[1/4] 安装二进制: $RC_BIN -> /usr/local/bin/rc-agent"
install -m 0755 "$RC_BIN" /usr/local/bin/rc-agent

echo "[2/4] 写入配置 /etc/rc-agent/rc-agent.conf"
mkdir -p /etc/rc-agent /var/lib/rc-agent
umask 077
cat > /etc/rc-agent/rc-agent.conf <<EOF
RC_SERVER=$SERVER_URL
RC_TOKEN=$AGENT_TOKEN
RC_ID_FILE=/var/lib/rc-agent/id
EOF
[ -n "$RC_NAME" ] && echo "RC_NAME=$RC_NAME" >> /etc/rc-agent/rc-agent.conf
umask 022

echo "[3/4] 注册 systemd 服务"
install -D -m 0644 /dev/stdin /etc/systemd/system/rc-agent.service <<'UNIT'
[Unit]
Description=RC Agent (Remote Console 千机台被管端)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/rc-agent
EnvironmentFile=-/etc/rc-agent/rc-agent.conf
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload

echo "[4/4] 启动 rc-agent"
systemctl enable --now rc-agent
systemctl --no-pager --lines=8 status rc-agent || true

echo
echo "✅ 安装完成。agent 已向 $SERVER_URL 注册,ID 持久化于 /var/lib/rc-agent/id"
echo "   管理页面请登录控制台查看主机状态。"
