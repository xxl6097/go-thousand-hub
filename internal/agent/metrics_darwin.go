//go:build darwin

package agent

import (
	"os/exec"
	"strings"

	"remoteconsole/internal/protocol"
)

// kernelInfo macOS 用 uname 命令取内核版本(仅用于开发机冒烟)
func kernelInfo() string {
	b, err := exec.Command("/usr/bin/uname", "-r").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// collect macOS 开发机上仅回填主机名与 uptime,其余指标为零(不影响终端功能冒烟)
func collect(hostname string) protocol.Metrics {
	return protocol.Metrics{
		Hostname: hostname,
	}
}
