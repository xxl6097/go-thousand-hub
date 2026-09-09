// rc-server 远程运维控制台服务端。
//
// 提供:
//   - 被管主机(rc-agent)反向接入与在线状态监控
//   - 浏览器管理页面(需登录),可对每台在线主机打开真实 shell 执行任意命令
//
// 用法:
//
//	RC_AGENT_TOKEN=<agent共享令牌> RC_ADMIN_PASS=<管理密码> ./rc-server
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/xxl6097/go-thousand-hub/internal/server"
	"github.com/xxl6097/go-thousand-hub/internal/updater"
)

func main() {
	var (
		listen  = flag.String("listen", envOr("RC_LISTEN", ":8080"), "HTTP 监听地址")
		token   = flag.String("agent-token", envOr("RC_AGENT_TOKEN", ""), "agent 接入令牌(agent 端 RC_TOKEN 需一致)")
		user    = flag.String("admin-user", envOr("RC_ADMIN_USER", "admin"), "控制台管理员账号")
		pass    = flag.String("admin-pass", envOr("RC_ADMIN_PASS", ""), "控制台管理员密码")
		secret  = flag.String("secret", envOr("RC_SECRET", ""), "会话签名密钥(建议固定,重启不失效)")
		webDir  = flag.String("web-dir", envOr("RC_WEB_DIR", ""), "前端资源目录(默认内置,无需设置)")
		tlsMode = flag.Bool("tls", envBool("RC_TLS"), "启用 HTTPS/WSS")
		cert    = flag.String("cert", envOr("RC_TLS_CERT", "cert.pem"), "TLS 证书路径")
		key     = flag.String("key", envOr("RC_TLS_KEY", "key.pem"), "TLS 私钥路径")
		dev     = flag.Bool("dev", envBool("RC_DEV"), "开发模式(放行跨源 Origin)")
		version = flag.String("version", envOr("RC_VERSION", "dev"), "服务端当前版本号(仅展示/对比用)")
		updURL  = flag.String("updater-url", envOr("RC_UPDATE_URL", ""), "第三方升级服务地址(可选):控制台检测升级/执行升级会转发到 {url}/check 与 {url}/apply")
	)
	flag.Parse()

	if *token == "" {
		*token = "rc-agent-token"
		log.Printf("提示: 未设置 RC_AGENT_TOKEN,使用默认 %q(生产环境务必修改)", *token)
	}
	if *pass == "" {
		*pass = "admin123"
		log.Printf("提示: 未设置 RC_ADMIN_PASS,使用默认 admin123(生产环境务必修改)")
	}

	cfg := server.Config{
		Listen:     *listen,
		AgentToken: *token,
		AdminUser:  *user,
		AdminPass:  *pass,
		Secret:     *secret,
		WebDir:     *webDir,
		TLS:        *tlsMode,
		CertFile:   *cert,
		KeyFile:    *key,
		Dev:        *dev,
		Version:    *version,
	}
	// 升级扩展点:第三方实现注入。
	// 方式一(零代码):配置 RC_UPDATE_URL 指向自己的升级服务(任意语言),
	//   接口约定见 internal/updater/http.go(内置 HTTPRemote 参考实现)。
	// 方式二(Go):实现 updater.Updater 接口后赋给 cfg.Updater 再编译。
	if *updURL != "" {
		cfg.Updater = updater.NewHTTPRemote(*updURL)
		log.Printf("已启用升级通道: %s/check 与 %s/apply (HTTPRemote)", *updURL, *updURL)
	} else {
		log.Printf("未配置升级通道 RC_UPDATE_URL,控制台'检测升级'将提示未配置(由第三方实现注入)")
	}
	s := server.New(cfg)

	log.Printf("rc-server 启动: listen=%s admin_user=%s", cfg.Listen, cfg.AdminUser)
	log.Printf("agent 接入地址: ws://<本机>%s/ws/agent (请为 agent 配置 RC_SERVER)", cfg.Listen)
	if cfg.AdminPass == "admin123" || cfg.AgentToken == "rc-agent-token" {
		fmt.Fprintln(os.Stderr, "⚠ 正在使用默认凭据,仅供本地体验;部署前请务必修改 RC_ADMIN_PASS 与 RC_AGENT_TOKEN")
	}
	if err := s.Run(); err != nil {
		log.Fatalf("server 退出: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	v := os.Getenv(key)
	if v == "" {
		return false
	}
	b, _ := strconv.ParseBool(v)
	return b
}
