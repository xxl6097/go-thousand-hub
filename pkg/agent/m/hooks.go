package m

import "context"

// UninstallInfo 卸载上下文,随各扩展点一起传给业务方。
type UninstallInfo struct {
	AgentID string // agent 持久化 ID
	Name    string // 控制台展示名
	Host    string // 真实主机名
	Version string // agent 版本号
}

// UninstallHook 卸载生命周期扩展点 —— 业务方按需实现,在 agent 执行「彻底卸载自身」时
// 插入自定义处理:上报卸载事件、通知注册中心/CMDB、清理业务文件、备份日志等。
//
// 约定(重要):
//   - 三个方法语义上均为「可选」:不需要的阶段返回零值即可(nil error / 空字符串);
//   - 返回的 error 只记录日志,**不阻断**卸载主流程 —— 卸载必须尽力完成;
//   - 各方法内部 panic 会被 recover 兜住,同样不影响卸载;
//   - 不想实现整个接口时,用 UninstallHookFuncs 只填关心的函数字段;
//   - 注入方式:agent 启动前设置 m.Options.Hook(见 pkg/agent 入口)。
type UninstallHook interface {
	// BeforeUninstall 清理开始前调用:此时仍在 agent 进程内,可访问网络与自身资源,
	// 适合上报/注销/写审计。返回 error 仅记日志。
	BeforeUninstall(ctx context.Context, info UninstallInfo) error

	// ExtraCleanup 返回附加清理脚本(shell,以 root 权限由独立清理进程执行),
	// 会被追加进卸载清理脚本、排在「停服/杀进程」之前执行,用于删除业务自己的产物。
	// 返回空字符串表示不需要附加清理。
	ExtraCleanup(ctx context.Context, info UninstallInfo) string

	// AfterUninstall 在清理脚本已下发、agent 自身退出前调用(进程内最后一次机会),
	// 适合发送"卸载完成"通知。返回 error 仅记日志。
	AfterUninstall(ctx context.Context, info UninstallInfo) error
}

// UninstallHookFuncs UninstallHook 的函数式适配器:字段为 nil 即跳过该阶段。
//
// 用法(业务侧):
//
//	opts := &m.Options{ /* ... */ }
//	opts.Hook = m.UninstallHookFuncs{
//		OnBeforeUninstall: func(ctx context.Context, info m.UninstallInfo) error {
//			return notify("agent 即将卸载: " + info.AgentID)
//		},
//		OnExtraCleanup: func(ctx context.Context, info m.UninstallInfo) string {
//			return "rm -rf /opt/myapp/agent-sidecar" // 追加进清理脚本
//		},
//		OnAfterUninstall: func(ctx context.Context, info m.UninstallInfo) error {
//			return notify("agent 卸载完成")
//		},
//	}
type UninstallHookFuncs struct {
	OnBeforeUninstall func(ctx context.Context, info UninstallInfo) error
	OnExtraCleanup    func(ctx context.Context, info UninstallInfo) string
	OnAfterUninstall  func(ctx context.Context, info UninstallInfo) error
}

func (h UninstallHookFuncs) BeforeUninstall(ctx context.Context, info UninstallInfo) error {
	if h.OnBeforeUninstall == nil {
		return nil
	}
	return h.OnBeforeUninstall(ctx, info)
}

func (h UninstallHookFuncs) ExtraCleanup(ctx context.Context, info UninstallInfo) string {
	if h.OnExtraCleanup == nil {
		return ""
	}
	return h.OnExtraCleanup(ctx, info)
}

func (h UninstallHookFuncs) AfterUninstall(ctx context.Context, info UninstallInfo) error {
	if h.OnAfterUninstall == nil {
		return nil
	}
	return h.OnAfterUninstall(ctx, info)
}
