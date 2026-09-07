// rc-agent 被管主机常驻客户端。
//
// 以 root 运行后即可被服务端远程打开真实 shell,执行该 Linux 主机上的任意命令。
// 用法:
//
//	RC_SERVER=ws://10.0.0.1:8080/ws/agent RC_TOKEN=<共享令牌> ./rc-agent
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/xxl6097/go-thousand-hub/internal/agent"
	"github.com/xxl6097/go-thousand-hub/pkg/qjt"
)

func main() {
	var (
		server   = flag.String("server", envOr("RC_SERVER", ""), "服务端地址 ws(s)://host:port/ws/agent")
		token    = flag.String("token", envOr("RC_TOKEN", ""), "与服务端共享的 agent 令牌")
		name     = flag.String("name", envOr("RC_NAME", ""), "主机展示名(默认主机名)")
		idFile   = flag.String("id-file", envOr("RC_ID_FILE", "/var/lib/rc-agent/id"), "agent ID 持久化路径")
		interval = flag.Duration("interval", envDur("RC_INTERVAL", 5*time.Second), "指标上报周期")
		caFile   = flag.String("ca-file", envOr("RC_CA_FILE", ""), "自定义 CA 证书路径(wss 自签证书场景)")
		insecure = flag.Bool("insecure", envBool("RC_INSECURE"), "跳过 TLS 证书校验(仅测试/内网)")
	)
	flag.Parse()

	if *server == "" {
		fmt.Fprintln(os.Stderr, "缺少必要参数: 请设置 -server 或环境变量 RC_SERVER(如 ws://host:8080/ws/agent)")
		flag.Usage()
		os.Exit(2)
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "缺少必要参数: 请设置 -token 或环境变量 RC_TOKEN(与服务端 RC_AGENT_TOKEN 一致)")
		os.Exit(2)
	}

	var cadata []byte
	if caFile != nil && *caFile != "" {
		cadata, _ = os.ReadFile(*caFile)
	}
	opts := qjt.Options{
		ServerURL: *server,
		Token:     *token,
		IDFile:    *idFile,
		Name:      *name,
		Interval:  *interval,
		CAData:    cadata,
		Insecure:  *insecure,
	}

	log.Printf("rc-agent %s 启动(server=%s name=%s idFile=%s)", agent.Version, *server, nameLabel(*name), *idFile)
	if err := agent.New(opts).Run(context.Background()); err != nil {
		log.Fatalf("agent 退出: %v", err)
	}
}

func nameLabel(n string) string {
	if n == "" {
		return "(默认主机名)"
	}
	return n
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envBool(key string) bool {
	v := os.Getenv(key)
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	return err == nil && b
}
