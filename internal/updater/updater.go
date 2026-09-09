// Package updater 定义 rc-server「服务端自升级」的可插拔扩展点。
//
// rc-server 自身只定义契约,不内置任何具体升级逻辑 —— 具体"在哪里检测新版本、
// 如何拉取并替换自身"由第三方开发者实现 Updater 并注入,接入方式见 README
// 「七、升级扩展(Updater)」。
package updater

import (
	"context"
	"errors"
)

// ErrNotConfigured 表示服务器未注入任何 Updater 实现(默认占位)。
// 控制台"检测升级/立即升级"会据此提示"升级通道未配置"。
var ErrNotConfigured = errors.New("升级通道未配置: 未注入 updater.Updater 实现")

// Release 一次可用的新版本描述(Check 的返回值,即"检测到的升级")。
type Release struct {
	Version string `json:"version"`          // 新版本号,如 "v1.3.0"
	Notes   string `json:"notes,omitempty"`  // 更新说明(可选,展示给管理员)
	URL     string `json:"url,omitempty"`    // 发布页/下载地址(可选)
}

// Updater 服务端自升级能力接口 —— 由第三方开发者实现并注入 rc-server。
//
// 实现要求:
//   - Check 只做"探测",不得产生副作用,可被管理员频繁触发,建议自带缓存/限流;
//     返回 nil 表示当前已是最新(控制台提示"已是最新版本");
//   - Apply 在管理员确认后执行真实升级(下载、校验、替换二进制、重启进程等),
//     允许长时间阻塞;成功后 rc-server 进程可能自行重启/退出,
//     控制台会按"升级指令已发出,服务可能重启"处理断连;
//   - 两个方法都应支持 ctx 取消/超时,返回的 error 会原样展示给管理员。
type Updater interface {
	// Check 检测是否存在可升级的新版本;nil 表示已是最新。
	Check(ctx context.Context) (*Release, error)

	// Apply 执行升级到 rel(通常来自 Check)。
	Apply(ctx context.Context, rel *Release) error
}

// Noop 是默认占位实现:未注入任何实现时,检测/升级返回 ErrNotConfigured,
// 让控制台给出友好提示而非 500。server.New 在 Config.Updater 为 nil 时自动使用。
type Noop struct{}

func (Noop) Check(ctx context.Context) (*Release, error) { return nil, ErrNotConfigured }
func (Noop) Apply(ctx context.Context, rel *Release) error {
	return ErrNotConfigured
}
