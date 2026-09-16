# rc-agent 卸载扩展点(UninstallHook)接入指南

> 面向第三方开发者:在 **不修改 rc-agent 源码** 的前提下,把业务自己的处理插入 agent 的「彻底卸载」流程 —— 上报卸载事件、注销注册中心/CMDB、清理业务随包文件、发送卸载完成通知等。

- 接口定义:[`pkg/agent/m/hooks.go`](../pkg/agent/m/hooks.go)
- 可运行示例:[`examples/agent-uninstall-hook`](../examples/agent-uninstall-hook/main.go)
- 相关实现:[`internal/agent/ctl.go`](../internal/agent/ctl.go)(`doUninstall` / `uninstallScript`)

---

## 1. 30 秒概览

agent 被远程卸载时,会依次回调三个扩展点:

```
控制台「卸载 Agent」
        │
        ▼
① BeforeUninstall(ctx, info)       ← 还在 agent 进程内,可联网:上报 / 注销 / 审计
        │
        ▼
② ExtraCleanup(ctx, info) string   ← 返回的 shell 片段被追加进清理脚本(以 root 执行)
        │                             排在「停服」之前,用来删业务自己的产物
        ▼
③ AfterUninstall(ctx, info)        ← 清理已下发、agent 退出前:发"卸载完成"通知
        │
        ▼
   agent 进程退出
```

三个回调**都是可选的**;不实现、返回 `nil` / 空串即跳过。

---

## 2. 最小示例(可直接复制)

```go
package main

import (
	"context"
	"log"
	"time"

	pub "github.com/xxl6097/go-thousand-hub/pkg/agent"
	"github.com/xxl6097/go-thousand-hub/pkg/agent/m"
)

func main() {
	opts := m.Options{
		ServerURL: "ws://10.0.0.1:8080/ws/agent", // 与 rc-agent 参数一致
		Token:     "<共享令牌>",
		IDFile:    "/var/lib/rc-agent/id",
		Name:      "web-01",
		Interval:  5 * time.Second,
	}

	// ★ 唯一新增:注入卸载扩展点
	opts.Hook = m.UninstallHookFuncs{
		OnBeforeUninstall: func(ctx context.Context, info m.UninstallInfo) error {
			log.Printf("agent %s 即将卸载", info.AgentID)
			return nil
		},
		OnExtraCleanup: func(ctx context.Context, info m.UninstallInfo) string {
			return "rm -rf /opt/myapp/agent-sidecar 2>/dev/null || true"
		},
		OnAfterUninstall: func(ctx context.Context, info m.UninstallInfo) error {
			log.Printf("agent %s 卸载流程已完成", info.AgentID)
			return nil
		},
	}

	if err := pub.New(&opts, context.Background()); err != nil {
		log.Fatal(err)
	}
}
```

> 用这个入口替换官方 `rc-agent` 即可(参数、行为与官方完全一致,只多了扩展点)。

---

## 3. 接口契约

### 3.1 `UninstallHook`

```go
type UninstallHook interface {
	BeforeUninstall(ctx context.Context, info UninstallInfo) error
	ExtraCleanup(ctx context.Context, info UninstallInfo) string
	AfterUninstall(ctx context.Context, info UninstallInfo) error
}
```

### 3.2 `UninstallInfo`(回调上下文)

| 字段 | 说明 |
|---|---|
| `AgentID` | agent 持久化 ID(`RC_ID_FILE` 中保存的稳定标识) |
| `Name` | 控制台展示名(默认主机名) |
| `Host` | 真实主机名 |
| `Version` | agent 版本号 |

### 3.3 `UninstallHookFuncs`(函数式适配器)

只关心某一步时用它,未填的字段 == 空操作:

```go
type UninstallHookFuncs struct {
	OnBeforeUninstall func(ctx context.Context, info UninstallInfo) error
	OnExtraCleanup    func(ctx context.Context, info UninstallInfo) string
	OnAfterUninstall  func(ctx context.Context, info UninstallInfo) error
}
```

---

## 4. 三个阶段详解

| 阶段 | 运行位置 | 能做什么 | 注意 |
|---|---|---|---|
| `BeforeUninstall` | **agent 进程内**(清理尚未开始) | 调 HTTP 上报/注销、写审计日志、备份状态、通知业务模块自行收尾 | 超时 10s;不要在这里做耗时下载 |
| `ExtraCleanup` | 返回值作为 **shell 片段**被拼进清理脚本,由**独立清理进程**以 root 执行 | 删除业务自己的文件/目录/服务单元;调用业务自有的清理脚本 | 排在「systemctl stop」**之前**执行;卸载会紧随其后停服,建议短平快 |
| `AfterUninstall` | **agent 进程内**(清理脚本已启动,进程即将退出) | 发送「卸载完成」通知、写最后一条审计 | 超时 10s;此后 agent 立即 `exit` |

### ExtraCleanup 的脚本环境

清理脚本由 `internal/agent` 生成,其中有可复用的变量:

| 变量 | 含义 |
|---|---|
| `$P` | 清理前缀。**默认空串 = 真实根路径**;演练时由 `RC_UNINSTALL_PREFIX` 指定 |
| `$SVC` | 服务名(默认 `rc-agent`,可由 `RC_SERVICE_NAME` 覆盖) |

因此业务脚本可以写成前缀可演练的形式:

```sh
rm -rf "$P/opt/myapp" 2>/dev/null || true
```

---

## 5. 注入方式

### 5.1 方式 A:函数式适配器(推荐,零样板)

见 [第 2 节](#2-最小示例可直接复制)。适合回调很少、无状态的场景。

### 5.2 方式 B:实现 `UninstallHook` 接口的类型

回调逻辑复杂、需要在多次调用间共享状态(如 HTTP client、业务配置、缓存)时:

```go
type bizHook struct {
	api    *http.Client
	appKey string
	logger *slog.Logger
}

func (h *bizHook) BeforeUninstall(ctx context.Context, info m.UninstallInfo) error {
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://cmdb.example.com/deregister", nil)
	q := req.URL.Query()
	q.Set("agent_id", info.AgentID)
	q.Set("app", h.appKey)
	req.URL.RawQuery = q.Encode()
	resp, err := h.api.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	return err
}

func (h *bizHook) ExtraCleanup(ctx context.Context, info m.UninstallInfo) string {
	return "rm -rf /opt/myapp /var/log/myapp-agent 2>/dev/null || true"
}

func (h *bizHook) AfterUninstall(ctx context.Context, info m.UninstallInfo) error {
	h.logger.Info("uninstall finished", "agent", info.AgentID)
	return nil
}

// opts.Hook = &bizHook{api: http.DefaultClient, appKey: "order-svc", logger: slog.Default()}
```

> 官方 `cmd/agent` 入口不读取任何 Hook 配置(Hook 是 Go 接口,属**编译期注入**)。
> 需要扩展时,请使用自己的入口程序调用 `pkg/agent` —— 即上面的最小示例。
> 如果希望「运行时可配置」,在**你自己的入口**里读环境变量/配置文件再决定填哪些回调即可
> (示例程序就是用 `BIZ_SIDECAR_DIR`、`BIZ_HOOK_LOG` 这么做的)。

---

## 6. 完整可运行示例

仓库内示例:`examples/agent-uninstall-hook`,模拟一个真实业务场景:

- 业务在主机上部署了 sidecar 目录 `/opt/myapp/agent-sidecar`;
- 卸载前写审计日志;
- 卸载时顺带删除 sidecar 目录;
- 卸载完成后记录收尾事件。

```bash
# 构建
go build -o rc-agent-biz ./examples/agent-uninstall-hook

# 运行(参数与 rc-agent 相同;BIZ_* 是业务自己的配置)
RC_SERVER=ws://10.0.0.1:8080/ws/agent \
RC_TOKEN=<共享令牌> \
BIZ_SIDECAR_DIR=/opt/myapp/agent-sidecar \
BIZ_HOOK_LOG=/var/log/myapp/agent-uninstall.log \
./rc-agent-biz
```

---

## 7. 与 agent 主流程的时序

```
控制台下发 host_ctl(uninstall)
        │
        ├─ agent 立即回执 {ok:true, msg:"已受理,agent 正在彻底卸载自身"}
        │
        └─ goroutine: doUninstall()
              ├─ sleep 400ms                 (等回执送达)
              ├─ BeforeUninstall             (扩展点①)
              ├─ ExtraCleanup                (扩展点②:取脚本片段)
              ├─ setsid sh -c <清理脚本>      (独立进程:删文件 → 业务附加清理 → 最后停服)
              ├─ AfterUninstall              (扩展点③)
              └─ sleep 2s → os.Exit(0)       (父进程兜底退出)
```

关键点:**回执先于动作**。控制台一定先看到「已受理」,不会因为卸载把连接掐断而丢回执。

---

## 8. 契约、约束与容错(重要)

| 约定 | 说明 |
|---|---|
| 全部可选 | 未实现的方法 / 返回 `nil`、`""` 均视为「跳过」 |
| **失败不阻断** | 回调返回 `error` 只在 agent 日志记一行 `[uninstall] 业务扩展 xxx 返回错误(已忽略)`,**卸载照常完成** |
| panic 被兜住 | 回调内 panic 会被 `recover`,同样只记日志 |
| 超时 | `BeforeUninstall` / `AfterUninstall` 各限时 **10s**(`ctx` 到期即放弃) |
| 不能取消卸载 | 设计如此:远程卸载必须尽力完成,扩展点只用于「附加处理」 |
| ExtraCleanup 权限 | 脚本以 **root** 执行(与 agent 同权限),请只删自己的产物,勿碰系统文件 |
| 顺序保证 | 业务附加清理段固定排在「停服/杀进程」之前,确保业务文件能被删掉 |
| 平台 | 卸载逻辑不区分平台(删除的是 agent 安装的产物);`ExtraCleanup` 在 Linux 上是常规路径 |

> 多个业务需要扩展时,建议在**一个** Hook 内部依次调用各方回调(或自行做复合),
> 避免为每个业务各起一个 agent 进程。

---

## 9. 本地调试与验证

### 9.1 演练模式(强烈推荐,不碰真实路径)

设置 `RC_UNINSTALL_PREFIX` 后,清理脚本的所有目标路径都会加上该前缀,并且**跳过 `pkill`**:

```bash
# 准备演练根:模拟 agent 产物 + 业务产物
mkdir -p /tmp/unin_root/{usr/local/bin,etc/rc-agent,var/lib/rc-agent,opt/myapp}
echo x > /tmp/unin_root/usr/local/bin/rc-agent
echo biz > /tmp/unin_root/opt/myapp/sidecar.bin

# 启动带 Hook 的 agent(前缀模式)
RC_SERVER=ws://127.0.0.1:8080/ws/agent RC_TOKEN=<令牌> \
RC_ID_FILE=/tmp/unin_root/var/lib/rc-agent/id \
RC_UNINSTALL_PREFIX=/tmp/unin_root \
BIZ_SIDECAR_DIR=/tmp/unin_root/opt/myapp \
BIZ_HOOK_LOG=/tmp/hook_trace.log \
./rc-agent-biz
```

然后在控制台点「操作 → 卸载 Agent」,验证:

```bash
cat /tmp/hook_trace.log          # 应看到 before-uninstall / extra-cleanup / after-uninstall 三行
find /tmp/unin_root -type f      # 应为空(agent 产物 + 业务 sidecar 都被删除)
```

### 9.2 单元测试参考

`internal/agent/ctl_test.go` 覆盖了扩展点的行为,可作为自测模板:

```bash
go test ./internal/agent/ -run TestUninstall -v
```

| 测试 | 验证点 |
|---|---|
| `TestUninstallScriptRemovesArtifacts` | 清理脚本真实删除全部安装痕迹 |
| `TestUninstallScriptExtraCleanup` | 附加清理段被执行,且排在停服之前;空 `extra` 不插入该段 |
| `TestUninstallHookPhases` | 三阶段按序回调、上下文完整、error/panic 不阻断 |
| `TestUninstallHookNilSafe` | 未注入 Hook 时为空操作 |
| `TestUninstallHookFuncsNilFields` | 适配器空字段跳过 |

### 9.3 自查清单

1. Hook 是否真的生效? → 在 `BeforeUninstall` 里打日志,卸载时看 agent 日志(官方入口不会打这些日志,说明你跑的还是 `cmd/agent`);
2. 业务文件没被删? → 检查 `ExtraCleanup` 返回的路径是否存在、是否有转义问题(见第 11 节);
3. 想看卸载全过程? → 用 `RC_UNINSTALL_PREFIX` 演练模式,文件不会被真删,可反复调试。

---

## 10. FAQ

**Q:我的 Hook 完全没被调用?**
A:确认你使用的是**自己的入口程序 + `pkg/agent.New`**,且 `opts.Hook` 已赋值。官方 `rc-agent`(`cmd/agent`)不读取 Hook。

**Q:能否阻止/延迟卸载?**
A:不能。回调失败、超时、panic 都不影响卸载继续执行 —— 这是有意设计,避免业务代码把远程卸载「卡死」。

**Q:能传业务配置给 Hook 吗?**
A:能。配置属于你自己的入口程序:从环境变量、配置文件、启动参数读取后闭包捕获,或在自定义 Hook 类型里作为字段携带(见 5.2)。

**Q:能在 ExtraCleanup 里跑耗时的清理任务吗?**
A:脚本在独立进程中执行,本身不受 10s 限制;但它后面紧跟着停服/杀进程。耗时任务建议放 `BeforeUninstall`(异步触发),或让脚本自行 `nohup` 后台化。

**Q:能拿到证书/令牌做 HTTPS 上报吗?**
A:可以,`BeforeUninstall` 在 agent 进程内执行,可用你自己的 HTTP client 与凭据;自签证书场景自行配置 `TLSClientConfig`。

**Q:多个回调同时报错会怎样?**
A:每个回调独立处理,互不影响,各自记一条日志。

---

## 11. 安全注意事项

1. `ExtraCleanup` 返回的脚本**以 root 执行**,只允许删除自己的产物;
2. 拼接路径时务必转义,避免空格或特殊字符导致误删:

   ```go
   // 单引号包裹,防止注入
   func shellQuote(s string) string {
       return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
   }
   // return "rm -rf " + shellQuote(bizDir) + " 2>/dev/null || true"
   ```

3. 不要硬编码凭据到 Hook 里,从业务配置读取;
4. `BeforeUninstall` 中做过网络上报时,注意给 `ctx` 设超时(框架已给 10s 上限,内部可再收紧);
5. 演练务必使用 `RC_UNINSTALL_PREFIX`,避免误删真实路径。

---

## 12. 版本记录

| 版本 | 变更 |
|---|---|
| v1.0.x | 引入 `UninstallHook`(BeforeUninstall / ExtraCleanup / AfterUninstall)、`UninstallHookFuncs` 适配器、示例程序与单元测试 |
