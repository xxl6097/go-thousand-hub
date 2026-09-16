package m

import (
	"time"
)

// Options 运行参数(由 main 从环境变量/flag 组装)
type Options struct {
	ServerURL string        // 形如 ws://host:port/ws/agent 或 wss://...
	Token     string        // 与服务器共享的 agent 认证令牌
	IDFile    string        // 持久化 agent ID 的文件路径
	Name      string        // 展示名(默认取主机名)
	Interval  time.Duration // 指标上报周期
	CAData    []byte        // 自定义 CA 证书内容(wss 校验服务端证书用,自签场景必填)
	Insecure  bool          // 跳过 TLS 证书校验(仅限内网/测试,慎用)
	//CAFile    string        // 自定义 CA 证书路径(wss 校验服务端证书用,自签场景必填)

	// Version 业务自定义 agent 版本号(可选,优先级最高)。
	// 留空则依次回退到:环境变量 RC_AGENT_VERSION / 命令行 -version →
	// 编译期注入的 internal/agent.Version → 内置 DefaultVersion。
	// 该版本号会上报给服务端(控制台主机卡片显示「agent vX」),并出现在卸载扩展点的
	// m.UninstallInfo.Version 中,便于第三方按自己产品的版本体系标识被管端。
	Version string

	// Hook 卸载生命周期扩展(可选):业务自定义「卸载前/附加清理/卸载后」处理。
	// 传 nil 表示不需要;只想关心某一步可用 m.UninstallHookFuncs。
	Hook UninstallHook
}
