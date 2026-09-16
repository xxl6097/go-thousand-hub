// 示例:业务如何扩展 rc-agent 的「彻底卸载」流程(UninstallHook)。
//
// 场景:某业务在主机上随 rc-agent 一起部署了自己的 sidecar 目录,
// 希望在 agent 被远程卸载时:
//
//	① 卸载前上报/审计(BeforeUninstall)
//	② 顺带删掉自己的 sidecar 目录(ExtraCleanup:返回一段 shell,由清理进程执行)
//	③ 卸载完成后发通知(AfterUninstall)
//
// 运行(与 rc-agent 参数一致,额外用业务自有配置):
//
//	RC_SERVER=ws://127.0.0.1:8080/ws/agent RC_TOKEN=<令牌> \
//	BIZ_SIDECAR_DIR=/opt/myapp/agent-sidecar \
//	BIZ_HOOK_LOG=/var/log/myapp/agent-uninstall.log \
//	go run ./examples/agent-uninstall-hook
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	ia "github.com/xxl6097/go-thousand-hub/internal/agent"
	pub "github.com/xxl6097/go-thousand-hub/pkg/agent"
	"github.com/xxl6097/go-thousand-hub/pkg/agent/m"
)

func main() {
	bizDir := envOr("BIZ_SIDECAR_DIR", "/opt/myapp/agent-sidecar") // 业务自己的产物目录
	hookLog := envOr("BIZ_HOOK_LOG", "/tmp/agent-hook-demo.log")   // 业务自己的审计日志

	// 业务侧统一的事件记录(示例:写文件;真实业务可换成上报 HTTP/写入审计系统)
	trace := func(phase string, info m.UninstallInfo) {
		f, err := os.OpenFile(hookLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			log.Printf("[biz] 记录 %s 失败: %v", phase, err)
			return
		}
		defer f.Close()
		fmt.Fprintf(f, "%s phase=%s agent=%s name=%s host=%s version=%s\n",
			time.Now().Format(time.RFC3339), phase, info.AgentID, info.Name, info.Host, info.Version)
	}

	opts := m.Options{
		ServerURL: envOr("RC_SERVER", ""),
		Token:     envOr("RC_TOKEN", ""),
		IDFile:    envOr("RC_ID_FILE", "/var/lib/rc-agent/id"),
		Name:      envOr("RC_NAME", ""),
		Interval:  5 * time.Second,
	}

	// ★ 扩展点注入:业务自定义卸载行为
	opts.Hook = m.UninstallHookFuncs{
		// ① 清理开始前:此时 agent 进程还在,可联网上报/注销
		OnBeforeUninstall: func(ctx context.Context, info m.UninstallInfo) error {
			trace("before-uninstall", info)
			// 例:return notifyCMDB(info.AgentID)
			return nil
		},
		// ② 附加清理:返回的 shell 会被追加进卸载清理脚本(排在停服之前执行)
		OnExtraCleanup: func(ctx context.Context, info m.UninstallInfo) string {
			trace("extra-cleanup", info)
			return "rm -rf " + shellQuote(bizDir) + " 2>/dev/null || true"
		},
		// ③ 清理已下发、agent 退出前:最后一次收尾(通知"卸载完成")
		OnAfterUninstall: func(ctx context.Context, info m.UninstallInfo) error {
			trace("after-uninstall", info)
			return nil
		},
	}

	if opts.ServerURL == "" || opts.Token == "" {
		log.Fatal("请设置 RC_SERVER 与 RC_TOKEN(与 rc-agent 相同)")
	}
	log.Printf("[biz] 启动: sidecar=%s hookLog=%s", bizDir, hookLog)
	if err := pub.New(&opts, context.Background()); err != nil {
		log.Fatalf("agent 退出: %v", err)
	}
}

// shellQuote 单引号包裹,避免路径含空格/特殊字符时注入
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var _ = ia.Version // 保持与 internal/agent 的版本一致(供业务上报)
