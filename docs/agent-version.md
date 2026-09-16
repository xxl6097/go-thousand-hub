# Agent 版本号扩展点(Version)

> 目标:把写死在 `internal/agent` 里的版本号做成**可扩展**,让第三方开发者/运维在不改动 agent 源码的前提下,
> 用自己产品的版本体系标识被管端。

## 一、背景

早期实现是硬编码常量:

```go
const Version = "1.0.0"   // internal/agent/agent.go
```

问题:第三方集成 rc-agent 后,控制台里所有主机都显示 `agent v1.0.0`,
无法区分自家版本;卸载事件里的 `version` 也失去意义。

现在改为**四级回退**,每一级都可被不同角色(开发者 / 运维 / CI)覆盖。

## 二、三种定制方式(优先级从高到低)

### 方式 1:代码注入 `m.Options.Version`(推荐给二次开发者)

```go
import (
    pub "github.com/xxl6097/go-thousand-hub/pkg/agent"
    "github.com/xxl6097/go-thousand-hub/pkg/agent/m"
)

opts := &m.Options{ServerURL: server, Token: token}
opts.Version = "v2.1.0"           // 或 myProductVersion.String()
pub.New(opts, ctx)
```

- 字段定义:`pkg/agent/m/config.go` 的 `Options.Version`
- 留空即不生效,自动走后面的回退链
- 首尾空白会被自动裁剪(`" v2.1.0 "` → `"v2.1.0"`)

### 方式 2:部署配置 `RC_AGENT_VERSION` / `-version`(运维侧)

```bash
# 环境变量
RC_AGENT_VERSION=v2.1.0 ./rc-agent -server ws://host:8080/ws/agent -token xxx

# 或命令行
./rc-agent -version v2.1.0 -server ws://host:8080/ws/agent -token xxx

# systemd 场景写进 /etc/rc-agent/rc-agent.conf
RC_AGENT_VERSION=v2.1.0
```

适用于**原样使用官方二进制**的场景。

### 方式 3:编译期注入 `-ldflags -X`(自建 CI)

```bash
go build -ldflags "-X github.com/xxl6097/go-thousand-hub/internal/agent.Version=v2.1.0" -o rc-agent ./cmd/agent
```

Makefile 里可接 CI 变量:

```makefile
VERSION ?= dev
LDFLAGS := -X github.com/xxl6097/go-thousand-hub/internal/agent.Version=$(VERSION)
agent:
	go build -ldflags "$(LDFLAGS)" -o dist/rc-agent ./cmd/agent
```

> 注意:`Version` 已从 `const` 改为 `var`,否则 `-X` 无法注入。

### 兜底

三者都不指定时取 `internal/agent.DefaultVersion`(`1.0.0`)。

## 三、优先级与实现

| 优先级 | 来源 | 判定位置 |
|---|---|---|
| 1 | `m.Options.Version` | `internal/agent.(*Agent).version()` |
| 2 | `RC_AGENT_VERSION` / `-version` | `cmd/agent/main.go` 组装 `m.Options.Version` |
| 3 | 编译期注入的 `internal/agent.Version` | 同名包变量(可被 `-ldflags -X` 覆盖) |
| 4 | `internal/agent.DefaultVersion` | 内置常量 |

```go
func (a *Agent) version() string {
    if a.opts.Version != "" { return a.opts.Version }   // 代码 / 环境变量
    if Version != ""        { return Version }          // 编译期注入
    return DefaultVersion                               // 内置兜底
}
```

## 四、生效范围

- **控制台展示**:随 `hello` 上报,主机卡片显示 `agent v2.1.0`,服务端 `hub.go` 里按 `hello.Version` 落库;
- **卸载事件**:`m.UninstallInfo.Version` 同步带上,业务 Hook 里可按版本统计/审计:

```go
opts.Hook = m.UninstallHookFuncs{
    OnBeforeUninstall: func(ctx context.Context, info m.UninstallInfo) error {
        log.Printf("agent %s(v%s) 正在卸载", info.Name, info.Version)
        return nil
    },
}
```

- **不做的事**:版本号不参与鉴权、不做兼容性判断、不影响协议握手,仅作标识与展示。

## 五、验证

单测(已覆盖 5 个用例):

```bash
go test ./internal/agent/ -run Version -v
# TestAgentVersionDefault / TestAgentVersionCustom / TestAgentVersionTrimSpace
# TestAgentVersionFallbackToBuildVar / TestUninstallInfoVersion
```

手验:

```bash
# 起本地服务端
RC_LISTEN=0.0.0.0:18080 RC_AGENT_TOKEN=t RC_ADMIN_PASS=p RC_SECRET=s ./dist/rc-server
# 带版本号起 agent
RC_SERVER=ws://127.0.0.1:18080/ws/agent RC_TOKEN=t RC_AGENT_VERSION=v9.9.9 ./dist/rc-agent -name demo
```

浏览器打开控制台,该主机卡片副行应显示 `agent v9.9.9`。

示例程序:`examples/agent-custom-version/main.go`(演示方式 1 的代码注入)。

## 六、FAQ

**Q:改了版本号会影响升级/鉴权吗?**
A:不会。版本号纯标识,服务端不做任何基于版本的分支判断。

**Q:能不能运行时动态改?**
A:版本号在 `hello` 里上报一次,运行期修改需重启 agent(或等下次重连握手)。

**Q:空字符串会怎样?**
A:视为未设置,自动回退到下一级;`"   "` 这类纯空白也会被裁成空并回退。

**Q:为什么 `Version` 是变量不是常量?**
A:为了让 `-ldflags -X` 能注入。语义上的"内置默认值"仍在常量 `DefaultVersion` 里。
