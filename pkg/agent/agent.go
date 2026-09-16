package agent

import (
	"context"
	"errors"
	"log"

	"github.com/xxl6097/go-thousand-hub/internal/agent"
	"github.com/xxl6097/go-thousand-hub/pkg/agent/m"
)

func New(opts *m.Options, ctx context.Context) error {
	if opts == nil {
		return errors.New("opts is nil")
	}
	if err := agent.New(*opts).Run(ctx); err != nil {
		log.Printf("agent 退出: %v", err)
		return err
	}
	return nil
}

// 业务自定义扩展(可选):
// agent 执行「彻底卸载」时,会按 BeforeUninstall -> ExtraCleanup -> AfterUninstall
// 的顺序回调 m.Options.Hook,业务可借此上报卸载事件、清理自己的文件、通知注册中心等:
//
//	opts := &m.Options{ServerURL: ..., Token: ...}
//	opts.Hook = m.UninstallHookFuncs{
//		OnBeforeUninstall: func(ctx context.Context, info m.UninstallInfo) error { ... },
//		OnExtraCleanup:    func(ctx context.Context, info m.UninstallInfo) string { return "rm -rf /opt/myapp/sidecar" },
//		OnAfterUninstall:  func(ctx context.Context, info m.UninstallInfo) error { ... },
//	}
//	agent.New(opts, ctx)
//
// 回调返回的错误/panic 都不会阻断卸载;详见 pkg/agent/m/hooks.go。
