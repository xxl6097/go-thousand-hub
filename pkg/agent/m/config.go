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
}
