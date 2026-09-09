package m

import (
	"github.com/xxl6097/go-thousand-hub/pkg/server/updater"
)

// Config 服务端配置
type Config struct {
	Listen     string // 监听地址,如 :8080
	AgentToken string // agent 接入令牌
	AdminUser  string
	AdminPass  string
	Secret     string // 会话签名随机源(默认每次启动随机,重启需重新登录)
	WebDir     string // 前端静态资源目录覆盖(可选)
	TLS        bool
	CertFile   string
	KeyFile    string
	Dev        bool            // 开发模式:允许任意 Origin
	Version    string          // 服务端当前版本号(展示用,默认 "dev")
	Updater    updater.Updater // 自升级通道(可选,由第三方实现注入;nil 时控制台提示未配置)
}
