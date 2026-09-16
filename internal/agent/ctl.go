package agent

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/xxl6097/go-thousand-hub/internal/protocol"
	"github.com/xxl6097/go-thousand-hub/pkg/agent/m"
)

// hookTimeout 单个业务扩展点的最长执行时间(超时即放弃,不拖住卸载)
const hookTimeout = 10 * time.Second

// handleHostCtl 处理服务端下发的主机控制指令。
// 流程:校验动作 -> 立即回执 -> 异步执行破坏性动作(回执先于动作,避免执行瞬间断链丢回执)。
func (a *Agent) handleHostCtl(c *websocket.Conn, env protocol.Envelope) error {
	var ctl protocol.HostCtl
	if err := json.Unmarshal(env.Data, &ctl); err != nil {
		return a.ctlReply(c, "", false, "指令格式错误")
	}

	switch ctl.Action {
	case protocol.CtlReboot:
		if err := a.ctlReply(c, ctl.Action, true, "已受理,主机将重启"); err != nil {
			return err
		}
		go a.delayed(func() { _ = a.powerCmd(false) })
		return nil

	case protocol.CtlShutdown:
		if err := a.ctlReply(c, ctl.Action, true, "已受理,主机将关机"); err != nil {
			return err
		}
		go a.delayed(func() { _ = a.powerCmd(true) })
		return nil

	case protocol.CtlRestartAgent:
		if err := a.ctlReply(c, ctl.Action, true, "已受理,agent 将重启(systemd 托管时自动拉起)"); err != nil {
			return err
		}
		go a.delayed(func() { _ = restartSelf() })
		return nil

	case protocol.CtlUninstall:
		if err := a.ctlReply(c, ctl.Action, true, "已受理,agent 正在彻底卸载自身"); err != nil {
			return err
		}
		go a.doUninstall()
		return nil

	default:
		return a.ctlReply(c, ctl.Action, false, "不支持的动作: "+ctl.Action)
	}
}

// ctlReply 向服务端回执
func (a *Agent) ctlReply(c *websocket.Conn, action string, ok bool, msg string) error {
	return a.write(c, protocol.Envelope{
		Type:    protocol.MsgHostCtlRes,
		AgentID: a.id,
		Data:    protocol.Enc(protocol.HostCtlResult{Action: action, Ok: ok, Msg: msg}),
	})
}

// delayed 小延迟后再执行,确保回执已发出
func (a *Agent) delayed(fn func()) {
	time.Sleep(600 * time.Millisecond)
	fn()
}

// powerCmd 重启/关机。仅 Linux 生效(macOS/开发机安全空转,避免误重启本机)。
func (a *Agent) powerCmd(shutdown bool) error {
	if runtime.GOOS != "linux" {
		return nil // 非 Linux 仅作开发冒烟,不执行任何系统动作
	}
	target := "reboot"
	if shutdown {
		target = "poweroff"
	}
	for _, cand := range [][]string{
		{"/bin/systemctl", target},
		{"/usr/bin/systemctl", target},
		{"/sbin/" + target},
		{"/usr/sbin/" + target},
	} {
		if err := startDetached(cand[0], cand[1:]...); err == nil {
			return nil
		} else if isNoSuchFile(err) {
			continue
		}
	}
	return startDetached(target)
}

// restartSelf 由 systemd 托管时优雅重启(先结束自身,Restart=always 自动拉起)。
// 仅 Linux 执行;macOS/开发机为空操作。
func restartSelf() error {
	if runtime.GOOS != "linux" {
		return nil
	}
	svc := os.Getenv("RC_SERVICE_NAME")
	if svc == "" {
		svc = "rc-agent"
	}
	// 稍作延迟等回执送达再自杀
	time.Sleep(500 * time.Millisecond)
	if err := startDetached("/bin/systemctl", "restart", svc); err == nil {
		return nil
	}
	if err := startDetached("/usr/bin/systemctl", "restart", svc); err == nil {
		return nil
	}
	// 无托管环境:直接退出,由调用方/运维负责拉起
	os.Exit(0)
	return nil
}

// doUninstall 彻底卸载 agent 自身安装的产物(不区分平台;删的是 agent 安装的文件,与操作系统无关)。
// 流程:先回执 -> [业务扩展 Before] -> [业务附加清理脚本] -> 以独立会话启动清理子进程
// -> [业务扩展 After] -> 自身稍候退出作为兜底。
//
// 业务扩展点 = m.Options.Hook(m.UninstallHook);未注入时全部为空操作。
func (a *Agent) doUninstall() {
	ctx := context.Background()
	info := a.uninstallInfo()

	time.Sleep(400 * time.Millisecond) // 等回执落地

	// 扩展点①:清理前的业务自定义处理(上报/注销/备份等)
	a.hookBeforeUninstall(ctx, info)

	// 扩展点②:业务附加清理脚本(如删业务 sidecar 文件),拼进清理脚本执行
	extra := a.hookExtraCleanup(ctx, info)

	// 清理子进程用 Setsid 脱离会话;关键:systemd 停服会杀整个 cgroup,
	// 因此脚本把”停服/杀进程”放到最后一步(见 uninstallScript),保证文件先删完。
	if err := startDetached("/bin/sh", "-c", uninstallScript(extra)); err != nil {
		// 启动失败也退出,避免半死状态
		os.Exit(0)
		return
	}

	// 扩展点③:清理已下发,agent 退出前最后一次业务收尾(发送"卸载完成"通知等)
	a.hookAfterUninstall(ctx, info)

	// 兜底:父进程稍候退出(无论 systemd 是否托管)
	time.Sleep(2 * time.Second)
	os.Exit(0)
}

// uninstallInfo 组装扩展点上下文
func (a *Agent) uninstallInfo() m.UninstallInfo {
	return m.UninstallInfo{AgentID: a.id, Name: a.name, Host: a.host, Version: Version}
}

// callHook 统一调用扩展点:recover panic + 超时,错误只记录日志,绝不阻断卸载。
func callHook(step string, fn func(ctx context.Context) error) {
	if fn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[uninstall] 业务扩展 %s panic(已忽略): %v", step, r)
		}
	}()
	if err := fn(ctx); err != nil {
		log.Printf("[uninstall] 业务扩展 %s 返回错误(已忽略): %v", step, err)
	}
}

// hookBeforeUninstall 扩展点①:清理前
func (a *Agent) hookBeforeUninstall(ctx context.Context, info m.UninstallInfo) {
	h := a.opts.Hook
	if h == nil {
		return
	}
	callHook("BeforeUninstall", func(c context.Context) error { return h.BeforeUninstall(c, info) })
}

// hookExtraCleanup 扩展点②:业务附加清理脚本(带 panic 保护)
func (a *Agent) hookExtraCleanup(ctx context.Context, info m.UninstallInfo) string {
	h := a.opts.Hook
	if h == nil {
		return ""
	}
	var out string
	callHook("ExtraCleanup", func(c context.Context) error {
		out = h.ExtraCleanup(c, info)
		return nil
	})
	return out
}

// hookAfterUninstall 扩展点③:清理已下发,退出前
func (a *Agent) hookAfterUninstall(ctx context.Context, info m.UninstallInfo) {
	h := a.opts.Hook
	if h == nil {
		return
	}
	callHook("AfterUninstall", func(c context.Context) error { return h.AfterUninstall(c, info) })
}

// uninstallScript 生成清理脚本。顺序要点:
//
//	先取消自启、先删文件/单元 -> 业务附加清理(ExtraCleanup)-> 最后才 stop / pkill,
//	避免 systemd 停服把"自己所在的清理进程"一起杀掉导致残留(早期版本的 bug)。
//
// 参数 extra 为业务方通过 m.UninstallHook.ExtraCleanup 提供的附加清理脚本片段,
// 为空则不插入该段。
//
// 环境变量(供单元测试/调试):
//
//	RC_SERVICE_NAME   服务名(默认 rc-agent)
//	RC_UNINSTALL_PREFIX  所有目标路径加前缀(默认空=真实根);非空时跳过 pkill,便于安全演练
func uninstallScript(extra string) string {
	svc := os.Getenv("RC_SERVICE_NAME")
	if svc == "" {
		svc = "rc-agent"
	}
	prefix := os.Getenv("RC_UNINSTALL_PREFIX")

	// 业务附加清理段:排在「删除 agent 产物之后、停服之前」执行
	bizStep := ""
	if strings.TrimSpace(extra) != "" {
		bizStep = `
# 5) 业务附加清理(m.Options.Hook.ExtraCleanup 提供)
` + strings.TrimRight(extra, "\n") + `
`
	}

	return `#!/bin/sh
# 清理脚本(由 rc-agent 卸载时以独立进程执行)
P='` + prefix + `'
SVC='` + svc + `'

# 1) 取消开机自启(只 disable 不 stop,避免此刻停服误杀本脚本)
systemctl disable "$SVC" 2>/dev/null || true

# 2) 删除可执行文件(Linux 允许删除运行中的二进制,父进程靠最后一步结束)
rm -f "$P/usr/local/bin/rc-agent" "$P/usr/bin/rc-agent" "$P/usr/sbin/rc-agent" 2>/dev/null || true

# 3) 删除配置与持久化(ID 等)
rm -rf "$P/etc/rc-agent" "$P/var/lib/rc-agent" "$P/etc/rc-agent.conf" 2>/dev/null || true

# 4) 删除 systemd 单元(停服前删除;reload 后 stop 若失败也无妨,文件已删净)
rm -f "$P/etc/systemd/system/$SVC.service" "$P/etc/systemd/system/$SVC.service.d/override.conf" 2>/dev/null || true
rmdir "$P/etc/systemd/system/$SVC.service.d" 2>/dev/null || true
systemctl daemon-reload 2>/dev/null || true
` + bizStep + `
# 6) 最后才触发停止/清进程:至此已无任何待删文件,即使本脚本随 cgroup 被杀也无残留
systemctl stop "$SVC" 2>/dev/null || true
systemctl reset-failed "$SVC" 2>/dev/null || true
if [ -z "$P" ]; then
  pkill -x rc-agent 2>/dev/null || true
fi
exit 0
`
}

// startDetached 以独立进程组启动并释放句柄(子进程不随父退出)
func startDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		// log.Printf("[uninstall] 清理进程启动失败: %v", err)
		return err
	}
	_ = cmd.Process.Release() // 避免僵尸进程,交由 init 收养
	return nil
}

func isNoSuchFile(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such file")
}
