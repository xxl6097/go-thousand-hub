# rc-server 升级接口(Updater)接入指南

> 面向第三方开发者:为控制台的「检测升级 / 立即升级」接入**你自己的升级通道** —— 查询内部发布系统、从对象存储拉取、走自建升级服务,均可。
> rc-server 只定义契约,**不内置任何升级逻辑**。

- 接口定义:[`pkg/server/updater/updater.go`](../pkg/server/updater/updater.go)
- HTTP 参考实现:[`pkg/server/updater/http.go`](../pkg/server/updater/http.go)(`NewHTTPRemote`)
- 可运行示例:[`examples/server-custom-updater`](../examples/server-custom-updater/main.go)
- 前端入口:控制台右上角 **账号 ▾ → 检测升级 / 升级到 vX.Y.Z / 退出登录**

---

## 1. 30 秒概览

```
控制台菜单「检测升级」
        │  POST /api/update/check
        ▼
   rc-server  ──► updater.Updater.Check(ctx)   ← 你的实现
        │                 │
        │                 └─ 返回 *Release(新版本) 或 nil(已最新) / error
        ▼
   前端提示「发现新版本 vX」→ 菜单出现「⬆ 升级到 vX」
        │
        │  POST /api/update/apply {"version":"vX"}
        ▼
   rc-server  ──► updater.Updater.Apply(ctx, rel)   ← 你的实现(下载/校验/替换/重启)
```

两种接入方式,**任选其一**:

| 方式 | 适用 | 怎么做 |
|---|---|---|
| **A. Go 实现并注入** | 升级逻辑用 Go 写,或需要复用内部 SDK/凭据 | 实现 `updater.Updater` → 赋给 `cfg.Updater` |
| **B. HTTP 回调(零代码)** | 已有(或用任意语言写的)升级服务 | 配 `RC_UPDATE_URL`,内置 `HTTPRemote` 转发 |

---

## 2. 快速开始

### 方式 A:Go 实现并注入(推荐)

```go
package main

import (
	"context"
	"log"

	"github.com/xxl6097/go-thousand-hub/pkg/server"
	"github.com/xxl6097/go-thousand-hub/pkg/server/m"
	"github.com/xxl6097/go-thousand-hub/pkg/server/updater"
)

type myUpdater struct{ /* 你的依赖:发布系统 client、下载器、日志… */ }

// Check 只做探测,不得有副作用;nil = 已是最新
func (u *myUpdater) Check(ctx context.Context) (*updater.Release, error) {
	// 例:查询内部发布系统 → 与当前版本比较
	return &updater.Release{Version: "v1.2.0", Notes: "修复若干问题", URL: "https://.../rc-server-v1.2.0"}, nil
}

// Apply 执行真实升级(允许长耗时;可能重启自身)
func (u *myUpdater) Apply(ctx context.Context, rel *updater.Release) error {
	// 下载 → 校验哈希/签名 → 原子替换 → 重启(systemd: systemctl restart rc-server)
	return nil
}

func main() {
	cfg := &m.Config{
		Listen:     ":8080",
		AgentToken: "<共享令牌>",
		AdminPass:  "<强密码>",
		Secret:     "<固定密钥>",   // 必填:固定后重启不掉登录态
		Version:    "v1.2.0",      // 当前版本(展示/对比用)
	}
	cfg.Updater = &myUpdater{}     // ★ 注入
	if err := server.RunServer(cfg); err != nil {
		log.Fatal(err)
	}
}
```

### 方式 B:HTTP 回调(零代码,任意语言)

给 rc-server 配一个地址即可,内置实现会把检测/升级转发过去:

```bash
RC_UPDATE_URL=https://upd.example.com/api/upgrade ./rc-server
```

升级服务只需实现两个接口(见 [第 5 节](#5-http-回调协议方式-b)):

| 接口 | 方法 | 成功语义 |
|---|---|---|
| `{RC_UPDATE_URL}/check` | GET | `200` + Release JSON(有新版本)/ `204`(已是最新) |
| `{RC_UPDATE_URL}/apply` | POST | 请求体 `{"version":"v1.2.0"}`;`2xx` = 已受理 |

---

## 3. 接口契约

```go
// pkg/server/updater
type Release struct {
	Version string `json:"version"`         // 新版本号,如 "v1.2.0"
	Notes   string `json:"notes,omitempty"` // 更新说明(前端菜单里展示)
	URL     string `json:"url,omitempty"`   // 发布页/下载地址
}

type Updater interface {
	Check(ctx context.Context) (*Release, error)    // 检测新版;nil = 已最新;不得有副作用
	Apply(ctx context.Context, rel *Release) error  // 执行升级到 rel
}
```

| 约定 | 说明 |
|---|---|
| `Check` 无副作用 | 管理员可反复点击检测;建议实现内自带缓存/限流(默认超时 **20s**) |
| `Check` 返回 `nil, nil` | 表示已是最新 → 前端 toast「已是最新版本 (当前版本)」 |
| 版本比较由**你**决定 | 服务端不做 semver 比较:你返回 `Release` 就意味着"可升级" |
| `Apply` 可长耗时 | 默认超时 **60s**;期间进程可能被你自己重启,HTTP 响应可能发不出去 |
| `Apply` 收到的 `Release` | **只保证 `Version` 有值**(前端只回传版本号);需要 notes/url 请在实现内自行查询或缓存 |
| 错误展示 | 返回的 `error` 文本会原样出现在控制台提示里(请写人话,别塞堆栈) |
| 未注入实现 | `server.New` 自动用 `updater.Noop`,接口返回 `ErrNotConfigured`,前端提示「升级通道未配置」 |

> `ErrNotConfigured`:未注入实现时返回该错误。若你的实现里某分支同样"配置缺失",也可以返回它,前端会显示同一条友好提示。

---

## 4. 注入位置与启动参数

| 位置 | 说明 |
|---|---|
| `m.Config.Updater` | 字段类型即 `updater.Updater`;为 `nil` 时用 `Noop` |
| `m.Config.Version` | 当前版本号,仅用于展示与你的比较逻辑(默认 `dev`) |
| `pkg/server.RunServer(cfg)` | 对外启动入口(封装 `internal/server`) |

官方入口 `cmd/server` 通过环境变量/参数选择实现:

```bash
# 未配置:控制台提示"升级通道未配置"
./rc-server

# 方式 B:转发给你自己的升级服务
RC_UPDATE_URL=https://upd.example.com/api/upgrade ./rc-server

# 版本号展示
RC_VERSION=v1.2.0 ./rc-server
```

> 想用**方式 A** 时,请使用你自己的 `main` 调用 `pkg/server.RunServer`(如 [第 2 节](#方式-ago-实现并注入推荐) 示例);官方 `cmd/server` 只内置了方式 B。

---

## 5. HTTP 回调协议(方式 B)

`HTTPRemote` 的转发规则(实现见 [`pkg/server/updater/http.go`](../pkg/server/updater/http.go)):

### 5.1 `GET {base}/check`

```http
GET /api/upgrade/check HTTP/1.1
```

| 你的响应 | 含义 |
|---|---|
| `200` + `{"version":"v1.2.0","notes":"...","url":"..."}` | 有新版本可升级(必须有 `version`) |
| `204 No Content` | 已是最新 |
| `404` / `501` | 视为"未实现该接口" → 控制台提示相应错误 |
| 其它非 2xx | 报错,响应体前 1KB 会拼进错误信息 |
| 非法 JSON / 缺 `version` | 报错 |

### 5.2 `POST {base}/apply`

```http
POST /api/upgrade/apply HTTP/1.1
Content-Type: application/json

{"version":"v1.2.0"}
```

- `2xx` → 已受理,控制台显示「升级已执行,服务端可能正在重启」;
- 其它 → 报错并在控制台提示。

### 5.3 一个可用的最小实现(Node.js)

```js
const http = require("http");
const CUR = "v1.0.0", NEW = "v1.1.0";

http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/check") {
    if (CUR === NEW) return res.writeHead(204).end();
    res.writeHead(200, { "Content-Type": "application/json" });
    return res.end(JSON.stringify({ version: NEW, notes: "演示", url: "https://example.com/rc/v1.1.0" }));
  }
  if (req.method === "POST" && req.url === "/apply") {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      console.log("apply:", body); // {"version":"v1.1.0"}
      res.writeHead(200, { "Content-Type": "application/json" }).end('{"accepted":true}');
    });
    return;
  }
  res.writeHead(404).end();
}).listen(9001);
```

---

## 6. REST API 参考(前端调用的就是这两个)

两者都**需要登录**(会话 Cookie),未登录返回 `401`。

### `POST /api/update/check`

| 项 | 值 |
|---|---|
| 鉴权 | 需要(管理员会话) |
| 超时 | 20s(超时/错误 → 502) |

响应示例:

```json
// 有新版本
{"ok":true,"current":"v1.0.0","latest":{"version":"v1.1.0","notes":"演示","url":"..."}}

// 已是最新
{"ok":true,"current":"v1.0.0","latest":null}

// 未注入实现(HTTP 501)
{"ok":false,"code":"not_configured","error":"升级通道未配置: 未注入 updater.Updater 实现","current":"v1.0.0"}

// 升级源出错(HTTP 502)
{"ok":false,"error":"升级服务不可达: ...","current":"v1.0.0"}
```

### `POST /api/update/apply`

| 项 | 值 |
|---|---|
| 鉴权 | 需要(管理员会话) |
| 请求体 | `{"version":"v1.1.0"}`(`version` 必填,否则 400) |
| 超时 | 60s |

```json
// 成功
{"ok":true,"msg":"升级已执行,服务端可能正在重启(重启后请重新登录)"}

// 缺少版本 / 未配置 / 执行失败
{"ok":false,"error":"缺少 version 参数","current":"v1.0.0"}
{"ok":false,"code":"not_configured","error":"...","current":"v1.0.0"}
{"ok":false,"error":"<你的 error 文本>","current":"v1.0.0"}   // HTTP 502
```

---

## 7. 控制台交互(前端行为,便于你设计实现)

1. 菜单点「检测升级」→ 按钮变「检测中…」→ 调 `/api/update/check`;
2. 有新版 → toast 提示,菜单出现绿色「⬆ 升级到 vX · notes」;无新版 → toast「已是最新版本 (当前版本)」;
3. 点升级项 → 确认弹窗(红色危险样式,「立即升级」)→ 调 `/api/update/apply`;
4. 执行成功 → toast 显示服务端返回的 `msg`;
5. **请求断连(进程重启导致连接被重置/超时)→ 前端提示「升级指令已发出,服务端正在重启,页面将自动刷新」,4 秒后自动 `reload`**。
   → 因此 `Apply` 内部**重启进程不会导致"升级失败"的误判**,放心实现。

> 建议:升级时要重启进程,请**先让 Apply 正常返回**(或先落盘再重启),避免管理员看不到任何反馈。
> 若使用 systemd 托管,记得 `RC_SECRET` 固定,否则重启后管理员需重新登录。

---

## 8. 实现你自己的升级器:典型流程

```
Check(ctx)
 ├─ 读取当前版本(cfg.Version / 编译期注入 / 版本文件)
 ├─ 查询发布源(内部发布系统、对象存储 manifest、升级服务)
 ├─ 比较版本 → 无更新返回 nil,nil
 └─ 有更新 → 返回 &Release{Version, Notes, URL}

Apply(ctx, rel)
 ├─ 下载新版本二进制/包 → 临时文件
 ├─ 校验:哈希 / GPG 签名(务必,防供应链投毒)
 ├─ 落盘:原子替换(rename)或解包到发布目录
 ├─ 重启:systemctl restart rc-server(推荐)或 exec 自身
 └─ 返回 nil(若已重启,响应可能发不出,前端按"正在重启"处理)
```

**Go 参考**:实现 `manifestUpdater`(读一份 JSON 清单作为发布源)的完整代码见
[`examples/server-custom-updater/main.go`](../examples/server-custom-updater/main.go):

```bash
RC_LISTEN=:8080 RC_AGENT_TOKEN=<令牌> RC_ADMIN_PASS=<密码> RC_SECRET=<固定密钥> \
RC_VERSION=v1.0.0 UPD_MANIFEST=/tmp/rc-update.json UPD_LOG=/tmp/rc-upgrade.log \
go run ./examples/server-custom-updater

# 模拟"发布新版本"
echo '{"version":"v1.1.0","notes":"演示升级","url":"https://example.com/rc/v1.1.0"}' > /tmp/rc-update.json
# 然后到控制台:账号 ▾ → 检测升级 → 升级到 v1.1.0
```

---

## 9. 调试与验证

### 9.1 纯 curl(无需前端)

```bash
# 1) 登录拿 Cookie
curl -s -c /tmp/ck.txt -d '{"username":"admin","password":"<密码>"}' \
  http://127.0.0.1:8080/api/login

# 2) 检测升级
curl -s -b /tmp/ck.txt -X POST http://127.0.0.1:8080/api/update/check

# 3) 执行升级
curl -s -b /tmp/ck.txt -X POST -d '{"version":"v1.1.0"}' \
  http://127.0.0.1:8080/api/update/apply
```

### 9.2 服务端日志关键字

| 日志 | 含义 |
|---|---|
| `[update] check 失败: ...` | `Check` 返回错误(同时 HTTP 502) |
| `[update] apply <版本> 已执行` | `Apply` 成功返回 |
| `[update] apply <版本> 失败: ...` | `Apply` 返回错误(同时 HTTP 502) |
| `已启用升级通道: <url>/check 与 <url>/apply (HTTPRemote)` | 启动时已按 `RC_UPDATE_URL` 装配(官方入口) |
| `未配置升级通道 RC_UPDATE_URL,控制台'检测升级'将提示未配置` | 未装配(方式 A 时请忽略,它是官方入口的提示) |

### 9.3 自查清单

1. 返回码是否符合预期? → 先在浏览器 Network 面板看 `/api/update/check` 的响应体与 `code`;
2. `Check` 是否误报? → 记住"返回 `Release` 就等于可升级",版本比较必须自己做完;
3. `Apply` 后管理员没反馈? → 检查是不是在返回前就把进程 kill 了;先返回、或先落盘再重启;
4. 重启后要求重新登录? → 固定 `RC_SECRET`。

---

## 10. FAQ

**Q:必须用 Go 实现吗?**
A:不必。方式 B 的 HTTP 回调对语言无要求,Go/Python/Node/Shell 都可以;Go 实现适合需要复用内部 SDK 的场景。

**Q:能升级的不是 rc-server 而是别的组件吗?**
A:可以。接口语义是"服务端自身的升级",但 `Check/Apply` 内部做什么由你决定 —— 例如只负责"通知外部部署系统滚动升级",接口照样工作。

**Q:`Apply` 为什么只拿到 `Version`?**
A:前端只回传版本号。若升级过程需要 `URL`/`Notes`,请在 `Check` 时缓存到你的实现内部(注意并发),或按版本重新查询发布源。

**Q:并发点击会怎样?**
A:接口本身串行不了并发请求,建议在你的 `Apply` 内加互斥(如 `sync.Mutex` 或状态文件),避免重复升级。

**Q:能回滚/失败重试吗?**
A:由你的实现负责。建议保留上一版本二进制、失败时不覆盖,并在返回的 error 里说明原因(会显示给管理员)。

**Q:多副本部署怎么办?**
A:每个副本各自执行一次升级可能冲突。建议 `Apply` 内采用"标记 + 幂等"(如比较已完成版本)或只由一台执行、其余通过发布系统滚动。

---

## 11. 安全注意事项

1. **必须校验升级包**:哈希/签名校验不可省,升级通道是典型的供应链攻击面;
2. `apply` 是**破坏性操作**:前端已加二次确认;接口本身需登录会话,请勿把 `/api/update/*` 暴露给非管理员;
3. 升级服务若走公网,请用 HTTPS,并考虑在 `Check/Apply` 中加内网校验或双向认证;
4. `RC_UPDATE_URL` 指向的地址由运维配置,勿允许普通用户可写;
5. 升级前建议自动备份当前二进制与配置,失败时可人工回滚;
6. 日志中不要打印凭据/令牌。

---

## 12. 版本记录

| 版本 | 变更 |
|---|---|
| v1.0.x | 引入 `updater.Updater`(`Check`/`Apply`)、`Release`、`Noop`、`HTTPRemote`(`RC_UPDATE_URL`)、REST `/api/update/check|apply`、控制台升级菜单与示例程序 |
