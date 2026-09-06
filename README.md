# 千机台 · Remote Console(rc-server / rc-agent)

基于 **Go + WebSocket + PTY** 的轻量主机远控与运维控制台。

- 在被管 Linux 主机上装一个 **agent(常驻进程,建议以 root 运行)**,它主动**反向连接**服务端 —— 无需被管机开放任何入站端口,NAT/防火墙后同样可用;
- 服务端提供**浏览器管理页面**:左侧实时显示所有主机的在线状态与 CPU/内存/磁盘/负载指标;
- 点击任意在线主机,即可在浏览器里打开一个**真实交互终端(xterm.js + 远端 PTY shell)**,和 SSH 登录体验一致,可运行该主机上的**任意命令、任意切换用户(su / sudo)、运行 vim / top / 交互程序**;
- 全程**不需要任何 SSH 账号密码** —— agent 以 root 身份运行,令牌即身份。

> ⚠ 安全模型:接入控制台即获得所有被管主机的 root 权限,能力等同后门。仅限自有/受信服务器群使用;请务必修改默认凭据并按需开启 TLS(见下)。

---

## 一、架构

```
┌────────────┐  WebSocket(反向接入)   ┌────────────────────────────┐
│ Linux 主机  │ ───────────────────────► │  rc-server(单二进制)         │
│  rc-agent   │   Bearer Token 鉴权      │  ├─ agent 注册表/在线状态     │
│  ├ 指标上报  │                          │  ├─ 终端会话路由/桥           │
│  ├ PTY shell│ ◄──── 打开/输入/改尺寸 ── │  └─ HTTP API + Web 页面      │
└────────────┘                          └──────────────┬─────────────┘
                                                       │ 同源 WebSocket
                                                  ┌────▼─────┐
                                                  │  浏览器   │
                                                  │ 管理页面  │
                                                  └──────────┘
```

链路:浏览器 xterm.js →(WS)→ rc-server →(WS)→ rc-agent → 主机真实 PTY(shell)。
终端输出字节流经 base64 包装,跨包 UTF-8 流式解码,中文/二进制均不丢字符。

| 模块 | 说明 |
|---|---|
| `cmd/server` | rc-server,HTTP + 两个 WS 端点(`/ws/agent` 接入、`/ws/console` 控制台)+ REST(`/api/login /api/agents /api/me`) |
| `cmd/agent` | rc-agent,反向连接、断线指数退避重连、持久化 agent ID、指标采集(Linux `/proc`) |
| `internal/protocol` | 三端共享的 JSON 信封协议与消息类型 |
| `internal/server` | Hub:在线主机表、历史注册表、终端路由桥、会话鉴权 |
| `internal/agent` | PTY 会话管理、平台层(Linux 全量指标;macOS 用于开发冒烟) |
| `internal/server/webroot` | 前端单页(内嵌进二进制,也可用 `RC_WEB_DIR` 覆盖为磁盘目录) |

单二进制交付:server 已把前端与静态资源 **embed** 进自身,Linux 上只拷一个 `rc-server` 即可。

---

## 二、编译

```bash
make all            # 一键产出 dist/rc-server、rc-agent-{darwin,linux-amd64,linux-arm64}
# 或手动:
go build ./cmd/server  ./cmd/agent
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o rc-agent-linux-amd64 ./cmd/agent

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/server
```

产物全部为静态链接单文件,Linux 目标不依赖 glibc。

---

## 三、快速体验(本机两分钟)

```bash
# 终端 1:启动服务端(默认监听 :8080)
RC_ADMIN_PASS=admin123 RC_AGENT_TOKEN=demo-token ./rc-server

# 终端 2:把当前这台机器“接入”(体验用;Linux 会显示真实指标)
RC_SERVER=ws://127.0.0.1:8080/ws/agent RC_TOKEN=demo-token ./rc-agent-darwin
```

浏览器打开 `http://127.0.0.1:8080`,用 `admin / 你设置的密码` 登录,点击左侧主机即可开终端。

---

## 四、生产部署

### 4.1 服务端(Linux,systemd)

```bash
sudo cp rc-server /usr/local/bin/
sudo mkdir -p /etc/rc-server && sudo tee /etc/rc-server/rc-server.conf <<'EOF'
RC_LISTEN=:8080
RC_AGENT_TOKEN=请改成超长随机串
RC_ADMIN_USER=admin
RC_ADMIN_PASS=请改成强密码
RC_SECRET=会话密钥_固定后重启不踢登录
# 如启用 TLS:
# RC_TLS=true
# RC_TLS_CERT=/etc/letsencrypt/live/rc.example.com/fullchain.pem
# RC_TLS_KEY=/etc/letsencrypt/live/rc.example.com/privkey.pem
EOF

sudo cp deploy/rc-server.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now rc-server
```

### 4.2 被管主机批量接入

```bash
# 单台:
sudo ./deploy/install-agent.sh wss://rc.example.com/ws/agent '<与上面一致的RC_AGENT_TOKEN>'

# 批量(把服务器列表按行放入 hosts.txt,scp 脚本+二进制到各机后执行)
while read h; do
  ssh root@$h 'bash -s' < deploy/install-agent.sh -- wss://rc.example.com/ws/agent '<token>'
done < hosts.txt
```

install-agent 会:安装 `/usr/local/bin/rc-agent` → 写 `/etc/rc-agent/rc-agent.conf` → 注册 systemd 并启动。agent 的 ID 持久化在 `/var/lib/rc-agent/id`,重连/重启身份不变。

agent 常用参数(均可用环境变量替代 flag):

| flag | 环境变量 | 默认 | 说明 |
|---|---|---|---|
| `-server` | `RC_SERVER` | 必填 | 服务端地址 `ws(s)://host:port/ws/agent` |
| `-token` | `RC_TOKEN` | 必填 | 与服务端 `RC_AGENT_TOKEN` 一致 |
| `-name` | `RC_NAME` | 主机名 | 控制台展示名 |
| `-id-file` | `RC_ID_FILE` | `/var/lib/rc-agent/id` | agent ID 持久化路径 |
| `-interval` | `RC_INTERVAL` | `5s` | 指标上报周期 |

---

## 五、配置参考(rc-server)

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `RC_LISTEN` | `:8080` | HTTP(S) 监听地址 |
| `RC_AGENT_TOKEN` | `rc-agent-token` | agent 接入令牌(生产必须修改) |
| `RC_ADMIN_USER` / `RC_ADMIN_PASS` | `admin` / `admin123` | 控制台管理员(生产必须修改) |
| `RC_SECRET` | 随机(每次启动变化) | 会话签名密钥;固定后重启无需重新登录 |
| `RC_WEB_DIR` | 空(内嵌) | 覆盖前端静态资源目录(开发热更用) |
| `RC_TLS` / `RC_TLS_CERT` / `RC_TLS_KEY` | off | 开启 HTTPS + WSS(公网部署强烈建议) |
| `RC_DEV` | false | 开发模式,放行跨源 Origin(默认已做同源校验) |

---

## 六、运维说明

- **在线判定**:agent 每 5s 心跳;掉线即显示离线并自动结束其名下终端会话,重连后历史主机仍保留(含最后心跳时间)。
- **终端能力**:默认启动 `$SHELL -l`(登录式,等价 SSH 会话);在终端里 `su - user2`、`sudo -i`、跑 `top/vim` 均正常(agent 为 root 时 sudo 免密)。
- **多会话**:同一主机可开多个终端标签;多个管理员可同时在线,每个终端归属打开它的控制台,互不串扰。
- **反向连接**:agent → server 出站,被管机无需开放端口;断线指数退避(1s→30s 封顶)自动重连。
- **前端离线可用**:xterm.js 等静态资源已内嵌/随包自带,控制台所在内网无外网也能用。

## 七、安全建议(重要)

1. 立即修改 `RC_ADMIN_PASS` 与 `RC_AGENT_TOKEN`,并固定 `RC_SECRET`;
2. 公网部署务必启用 `RC_TLS`(或由 nginx/caddy 终结 TLS 再反代 8080),避免令牌与终端内容明文过网;
3. 令牌按需轮换:换 token 后所有 agent 需同步更新 `/etc/rc-agent/rc-agent.conf` 并 `systemctl restart rc-agent`;
4. agent 长期以 root 运行属设计使然(运行所有命令的前提);请仅在受信网络主机上安装;
5. 控制台会话 12 小时有效,`退出` 立即销毁会话;服务重启且未固定 `RC_SECRET` 时所有会话失效(安全兜底)。

## 八、开发

```bash
make dev      # 本机构建 + 以 RC_WEB_DIR 指向 webroot 运行(前端改动即时生效,免重新编译)
make test     # go vet + 编译全部目标
```

协议细节见 `internal/protocol/protocol.go`;端到端冒烟可参考仓库外工具:登录 → `/api/agents` 拿在线 agent → `/ws/console` 发 `open/input`,断言回显与 `exit`。
