// 示例:第三方把自己的版本号注入 rc-agent(无需修改 agent 源码)。
//
// 三种定制方式(优先级从高到低):
//  1. 代码注入 m.Options.Version  —— 本示例的做法,推荐给二次开发者
//  2. 部署时 RC_AGENT_VERSION=v2.1.0 ./rc-agent(或 -version v2.1.0)
//  3. 编译期注入 go build -ldflags "-X github.com/xxl6097/go-thousand-hub/internal/agent.Version=v2.1.0"
//
// 都不用时回退到内置 DefaultVersion("1.0.0")。
//
// 生效后:控制台主机卡片显示「agent v2.1.0」,卸载扩展点 m.UninstallInfo.Version
// 也会带上该值,便于按自己的版本体系统计被管端。
//
// 运行:
//   go run ./examples/agent-custom-version -server ws://127.0.0.1:8080/ws/agent -token <token>
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	pub "github.com/xxl6097/go-thousand-hub/pkg/agent"
	"github.com/xxl6097/go-thousand-hub/pkg/agent/m"
)

// 业务侧自己的版本来源:可以是常量、构建产物、配置文件,或运行时从别处读到
const myProductVersion = "v2.1.0-edge.3"

func main() {
	server := flag.String("server", envOr("RC_SERVER", ""), "服务端地址 ws(s)://host:port/ws/agent")
	token := flag.String("token", envOr("RC_TOKEN", ""), "与服务端共享的 agent 令牌")
	flag.Parse()

	if *server == "" || *token == "" {
		log.Fatal("请设置 -server 与 -token(或 RC_SERVER / RC_TOKEN)")
	}

	opts := &m.Options{
		ServerURL: *server,
		Token:     *token,
		Version:   myProductVersion, // ← 关键:一行注入业务版本号
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("以业务版本 %s 启动 agent", myProductVersion)
	if err := pub.New(opts, ctx); err != nil {
		log.Fatalf("agent 退出: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
