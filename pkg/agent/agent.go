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
//
// ① 版本号(Version):第三方可把自己的产品版本体系带进来,见下方示例。
//
// ② 卸载生命周期(Hook):见下方示例。
//
// 版本号定制示例(优先级高于 RC_AGENT_VERSION/-version 与编译期注入值):
//
//	opts := &m.Options{ServerURL: ..., Token: ...}
//	opts.Version = "v2.1.0"   // 或 myProductVersion.String()
//	agent.New(opts, ctx)
//
// 该版本号会:
//   - 随 hello 上报服务端,控制台主机卡片显示「agent v2.1.0」;
//   - 出现在卸载扩展点的 m.UninstallInfo.Version 里,便于按版本统计/审计。
//
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
