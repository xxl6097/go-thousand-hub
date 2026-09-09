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

自签证书可直接用仓库内 `certs/` 示例(或自行生成,需含服务端 SAN):

```bash
openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 825 \
  -keyout server.key -out server.crt \
  -subj "/CN=remote-console/O=rc" \
  -addext "subjectAltName=DNS:rc.example.com,DNS:localhost,IP:127.0.0.1" \
  -addext "basicConstraints=critical,CA:TRUE"
  

openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 825 \
  -keyout server.key -out server.crt \
  -subj "/CN=remote-console/O=rc" \
  -addext "subjectAltName=DNS:103.42.30.173,DNS:localhost,IP:127.0.0.1" \
  -addext "basicConstraints=critical,CA:TRUE"
  
  
openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 825 \
  -keyout server.key -out server.crt \
  -subj "/CN=rc-server/O=rc" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1,IP:103.42.30.173" \
  -addext "basicConstraints=critical,CA:TRUE"
```

```bash
sudo cp rc-server /usr/local/bin/
sudo mkdir -p /etc/rc-server && sudo tee /etc/rc-server/rc-server.conf <<'EOF'
RC_LISTEN=:8080
RC_AGENT_TOKEN=请改成超长随机串
RC_ADMIN_USER=admin
RC_ADMIN_PASS=请改成强密码
RC_SECRET=会话密钥_固定后重启不踢登录
RC_TLS=true
RC_TLS_CERT=/etc/rc-server/server.crt
RC_TLS_KEY=/etc/rc-server/server.key
EOF

sudo cp deploy/rc-server.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now rc-server
```

### 4.2 被管主机批量接入

```
sudo mkdir -p /etc/rc-agent && sudo tee /etc/rc-agent/rc-agent.conf <<'EOF'
RC_SERVER=wss://103.42.30.173:8080/ws/agent
RC_TOKEN=zhujiangjiayuan2026
RC_ID_FILE=/var/lib/rc-agent/id
RC_NAME=Flight-Hub
EOF



/etc/systemd/system/

/usr/lib/systemd/system/

systemctl enable --now rc-agent

systemctl start rc-agent

```

```bash
# 单台:
sudo ./deploy/install-agent.sh wss://rc.example.com/ws/agent '<与上面一致的RC_AGENT_TOKEN>'

# 批量(把服务器列表按行放入 hosts.txt,scp 脚本+二进制到各机后执行)
while read h; do
  ssh root@$h 'bash -s' < deploy/install-agent.sh -- wss://rc.example.com/ws/agent '<token>'
done < hosts.txt
```

install-agent 会:安装 `/usr/local/bin/rc-agent` → 写 `/etc/rc-agent/rc-agent.conf` → 注册 systemd 并启动。agent 的 ID 持久化在 `/var/lib/rc-agent/id`,重连/重启身份不变。开 TLS 后 agent 端配置示例(自签证书需带 CA,否则握手失败):

```bash
sudo tee /etc/rc-agent/rc-agent.conf <<'EOF'
RC_SERVER=wss://rc.example.com:8080/ws/agent
RC_TOKEN=<与RC_AGENT_TOKEN一致>
RC_ID_FILE=/var/lib/rc-agent/id
RC_CA_FILE=/etc/rc-agent/server.crt
EOF
sudo systemctl restart rc-agent
```

> 自签证书场景:把 `server.crt` 分发到各被管机并配 `RC_CA_FILE`(agent 会以它为信任根校验服务端,同时该证书 `CA:TRUE` 声明使其可作 CA 使用);浏览器访问控制台会提示证书不受信,需手动信任一次(或改用受信 CA/letsencrypt 证书)。纯测试可给 agent 加 `RC_INSECURE=true` 跳过校验(不建议生产)。

agent 常用参数(均可用环境变量替代 flag):

| flag | 环境变量 | 默认 | 说明 |
|---|---|---|---|
| `-server` | `RC_SERVER` | 必填 | 服务端地址 `ws(s)://host:port/ws/agent`(开 TLS 用 `wss://`) |
| `-token` | `RC_TOKEN` | 必填 | 与服务端 `RC_AGENT_TOKEN` 一致 |
| `-name` | `RC_NAME` | 主机名 | 控制台展示名 |
| `-id-file` | `RC_ID_FILE` | `/var/lib/rc-agent/id` | agent ID 持久化路径 |
| `-interval` | `RC_INTERVAL` | `5s` | 指标上报周期 |
| `-ca-file` | `RC_CA_FILE` | 空 | 自定义 CA 证书路径;服务端用自签证书时必填,agent 以此校验 wss 服务端 |
| `-insecure` | `RC_INSECURE` | false | 跳过 TLS 校验(仅测试/纯内网,慎用) |

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

## 六、远程主机控制(重启 / 关机 / 卸载)

在线主机左侧点「**操作**」即可下发主机级控制指令(server → agent 通道,与终端会话独立):

| 动作 | 说明 | 二次确认 |
|---|---|---|
| 重启主机 | agent 以 root 执行 `systemctl reboot`(回退 `/sbin/reboot`) | 需输入主机名 |
| 关机 | `systemctl poweroff`(回退 `/sbin/poweroff`) | 弹窗确认 |
| 重启 Agent | `systemctl restart rc-agent`,数秒内自动恢复 | 弹窗确认 |
| 卸载 Agent | **彻底卸载自身**,见下 | 需输入主机名 |

**卸载清理范围**(与 install-agent.sh 的安装动作完全互逆,顺序经过专门设计):
1. 取消开机自启(`systemctl disable rc-agent`);
2. **先删除**可执行文件(`/usr/local/bin`、`/usr/bin`、`/usr/sbin` 下的 rc-agent);
3. 删除配置与持久化目录(`/etc/rc-agent`、`/var/lib/rc-agent`、`/etc/rc-agent.conf`);
4. 删除 systemd 单元与 `service.d/override.conf`,`daemon-reload`;
5. **最后才**触发停服/清进程(`systemctl stop`、`pkill -x rc-agent`),并结束自身进程。

> ⚠ 顺序即正确性:systemd 停服(KillMode=control-group)会把服务 cgroup 内的进程全部终止,包括清理进程自身。因此**必须先删完文件、最后停服**,否则会像早期版本那样停服瞬间清理被中断、文件残留。
>
> 卸载以**独立会话(Setsid)**启动清理子进程;先回执后执行。卸载对**所有平台生效**(只删 agent 自己安装的产物);重启/关机/重启 agent 等系统级动作仍仅 Linux 执行(macOS 为安全空转护栏)。清理脚本有单元测试覆盖(`internal/agent/ctl_test.go`,模拟完整安装痕迹并断言删除)。卸载后主机从控制台移除(历史记录保留最后心跳),恢复控制需重新人工安装 agent。
>
> 已知残留:systemd journal 中 rc-agent 单元的历史日志不随卸载删除(仅可用 `journalctl --rotate && journalctl --vacuum-time=1s` 全量清空,代价是清掉整机日志,默认不做)。web 终端里以 root 跑过的命令若写入 `~/.bash_history`,属正常使用痕迹,卸载不干预。

## 七、运维说明

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

## gen program

see: ~/Desktop/work/code/github/golang/go-frp-panel/internal/frps/client_gen.go 197

## 十、服务端自升级扩展(Updater)

控制台右上角用户菜单(账号 ▾)提供「**检测升级**」「**退出登录**」;
检测到新版本后菜单出现「**升级到 vX.Y.Z**」,确认后执行升级。
升级逻辑**不在 rc-server 内置**,而是通过可插拔接口交给第三方实现:

### 接口契约(第三方 Go 实现)

```go
// internal/updater/updater.go
type Release struct {
    Version string `json:"version"`         // 新版本号
    Notes   string `json:"notes,omitempty"` // 更新说明
    URL     string `json:"url,omitempty"`   // 发布/下载地址
}

type Updater interface {
    Check(ctx context.Context) (*Release, error) // 检测新版;nil=已最新;无副作用
    Apply(ctx context.Context, rel *Release) error // 执行升级(允许长阻塞/重启自身)
}
```

- 注入点:`server.Config.Updater`(见 `cmd/server/main.go` 的示例注释)。未注入时控制台提示「升级通道未配置」;
- REST 映射:控制台 → `POST /api/update/check` / `POST /api/update/apply`(均需登录)→ 委托 `Updater`。

### 零代码接入(RC_UPDATE_URL,任意语言实现)

给 rc-server 配一个**你自己的升级服务地址**,内置 `HTTPRemote` 实现会把检测/升级转发给它:

```ini
RC_UPDATE_URL=https://upd.example.com/api/upgrade
```

第三方升级服务只需实现两个 HTTP 接口(参考 `pkg/server/updater/http.go`,可用任意语言):

| 接口 | 说明 | 成功响应 |
|---|---|---|
| `GET  {RC_UPDATE_URL}/check` | 检测是否有新版 | `200` + `{"version":"v1.3.0","notes":"...","url":"..."}`;`204` = 已是最新 |
| `POST {RC_UPDATE_URL}/apply` | 执行升级,请求体 `{"version":"v1.3.0"}` | `2xx` = 已受理 |

> ⚠ `apply` 后 rc-server 进程可能被第三方逻辑重启,期间控制台会提示「服务端正在重启,页面将自动刷新」。当前版本号展示可用 `RC_VERSION` 指定(如 `RC_VERSION=v1.4.0`),仅用于界面展示与对比。
