// 示例:为 rc-server 注入自定义「服务端升级」实现(updater.Updater)。
//
// 演示两种接入方式:
//
//	① 零代码:设置 UPD_URL,使用内置 HTTPRemote 把检测/升级转发给第三方升级服务;
//	② Go 实现:实现 updater.Updater 接口(这里给出"本地版本清单文件"的演示实现)。
//
// 运行(未设置 UPD_URL 时走方式②,清单文件不存在则视为已最新):
//
//	RC_LISTEN=:8080 RC_AGENT_TOKEN=<令牌> RC_ADMIN_PASS=<密码> RC_SECRET=<固定密钥> \
//	RC_VERSION=v1.0.0 UPD_MANIFEST=/tmp/rc-update.json UPD_LOG=/tmp/rc-upgrade.log \
//	go run ./examples/server-custom-updater
//
// 然后写入一份"新版本清单"模拟发布:
//
//	echo '{"version":"v1.1.0","notes":"演示升级","url":"https://example.com/rc/v1.1.0"}' > /tmp/rc-update.json
//
// 回到控制台右上角菜单 → 检测升级 → 升级到 v1.1.0。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/xxl6097/go-thousand-hub/pkg/server"
	"github.com/xxl6097/go-thousand-hub/pkg/server/m"
	"github.com/xxl6097/go-thousand-hub/pkg/server/updater"
)

func main() {
	cfg := &m.Config{
		Listen:     envOr("RC_LISTEN", ":8080"),
		AgentToken: envOr("RC_AGENT_TOKEN", ""),
		AdminUser:  envOr("RC_ADMIN_USER", "admin"),
		AdminPass:  envOr("RC_ADMIN_PASS", ""),
		Secret:     envOr("RC_SECRET", ""), // 固定密钥,服务重启后登录态不失效
		Version:    envOr("RC_VERSION", "v1.0.0"),
	}

	// ★ 升级实现注入:方式①(HTTPRemote)或方式②(自定义类型)
	if url := os.Getenv("UPD_URL"); url != "" {
		cfg.Updater = updater.NewHTTPRemote(url)
		log.Printf("升级通道: HTTPRemote -> %s", url)
	} else {
		cfg.Updater = &manifestUpdater{
			manifest: envOr("UPD_MANIFEST", "/tmp/rc-update.json"),
			current:  cfg.Version,
			logFile:  envOr("UPD_LOG", "/tmp/rc-upgrade.log"),
		}
		log.Printf("升级通道: 本地版本清单 %s", envOr("UPD_MANIFEST", "/tmp/rc-update.json"))
	}

	if err := server.RunServer(cfg); err != nil {
		log.Fatalf("server 退出: %v", err)
	}
}

// manifestUpdater 演示实现:以一份本地 JSON 清单作为"发布源":
//
//	{"version":"v1.1.0","notes":"更新说明","url":"https://example.com/rc/v1.1.0"}
//
// 真实业务里,把 Check 换成"查询发布系统/对象存储/内部升级服务",把 Apply 换成
// "下载 → 校验 → 原子替换二进制 → 重启进程"即可,接口形态完全一致。
type manifestUpdater struct {
	manifest string // 版本清单路径
	current  string // 当前运行版本(与清单版本相同则视为已最新)
	logFile  string // 演示用:记录 apply 动作
}

// Check 只做探测:清单不存在或版本与当前一致 → 返回 nil(已是最新)。
func (u *manifestUpdater) Check(ctx context.Context) (*updater.Release, error) {
	b, err := os.ReadFile(u.manifest)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取版本清单失败: %w", err)
	}
	var rel updater.Release
	if err := json.Unmarshal(b, &rel); err != nil {
		return nil, fmt.Errorf("版本清单格式错误: %w", err)
	}
	if rel.Version == "" || rel.Version == u.current {
		return nil, nil
	}
	return &rel, nil
}

// Apply 执行升级。演示实现只写日志,避免误动本机文件。
//
// 生产实现建议:
//  1. 下载新版本到临时文件,校验哈希/签名(务必,避免供应链攻击);
//  2. 原子替换(rename)可执行文件,或在 systemd 托管下换成"落盘 + systemctl restart";
//  3. 需要重启时,先返回/落盘再重启进程 —— 控制台会按"服务可能重启"处理断连。
func (u *manifestUpdater) Apply(ctx context.Context, rel *updater.Release) error {
	line := fmt.Sprintf("%s apply version=%s url=%s\n",
		time.Now().Format(time.RFC3339), rel.Version, rel.URL)
	log.Printf("[demo-updater] 模拟执行升级: %s", strings.TrimSpace(line))

	f, err := os.OpenFile(u.logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
