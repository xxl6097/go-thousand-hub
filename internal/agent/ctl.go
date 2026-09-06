package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"remoteconsole/internal/protocol"
)

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

// powerCmd 重启/关机。优先 systemd,依次回退常见路径。
func (a *Agent) powerCmd(shutdown bool) error {
	target := "reboot"
	if shutdown {
		target = "poweroff"
	}
	if runtime.GOOS == "linux" {
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
	}
	// 非 Linux / 无 systemctl:尽力直接调用
	return startDetached(target)
}

// restartSelf 由 systemd 托管时优雅重启(先结束自身,Restart=always 自动拉起)。
func restartSelf() error {
	svc := os.Getenv("RC_SERVICE_NAME")
	if svc == "" {
		svc = "rc-agent"
	}
	// 稍作延迟等回执送达再自杀
	time.Sleep(500 * time.Millisecond)
	if runtime.GOOS == "linux" {
		if err := startDetached("/bin/systemctl", "restart", svc); err == nil {
			return nil
		}
		if err := startDetached("/usr/bin/systemctl", "restart", svc); err == nil {
			return nil
		}
	}
	// 无托管环境(systemd 命令失败):直接退出,由调用方/运维负责拉起
	os.Exit(0)
	return nil
}

// doUninstall 彻底卸载:清 systemd 单元/二进制/配置/ID/日志残留,再结束自身。
func (a *Agent) doUninstall() {
	time.Sleep(400 * time.Millisecond) // 等回执落地
	// 1. 以独立会话启动清理进程(脱离本进程生命周期)
	_ = startDetached("/bin/sh", "-c", uninstallScript())
	// 2. 清理进程会 stop+删文件;自身稍候退出,避免 systemd Restart=always 复活
	time.Sleep(1500 * time.Millisecond)
	os.Exit(0)
}

// uninstallScript 清理脚本(与安装动作完全互逆,覆盖 install-agent.sh 产生的全部痕迹)
func uninstallScript() string {
	svc := os.Getenv("RC_SERVICE_NAME")
	if svc == "" {
		svc = "rc-agent"
	}
	return `#!/bin/sh
sleep 1
# 1) 停服并取消开机自启(systemd 托管时)
systemctl disable --now ` + svc + ` 2>/dev/null
systemctl stop ` + svc + ` 2>/dev/null
systemctl reset-failed ` + svc + ` 2>/dev/null
# 2) 删除服务单元
rm -f /etc/systemd/system/` + svc + `.service /etc/systemd/system/` + svc + `.service.d/override.conf 2>/dev/null
rmdir /etc/systemd/system/` + svc + `.service.d 2>/dev/null
# 3) 删除二进制
rm -f /usr/local/bin/rc-agent 2>/dev/null
rm -f /usr/bin/rc-agent 2>/dev/null
rm -f /usr/sbin/rc-agent 2>/dev/null
# 4) 删除配置与持久化目录
rm -rf /etc/rc-agent /var/lib/rc-agent /etc/rc-agent.conf 2>/dev/null
# 5) 刷新 systemd 并清残余进程
systemctl daemon-reload 2>/dev/null
pkill -x rc-agent 2>/dev/null
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
		return err
	}
	_ = cmd.Process.Release() // 避免僵尸进程,交由 init 收养
	return nil
}

func isNoSuchFile(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such file")
}
