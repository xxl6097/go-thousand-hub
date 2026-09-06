// Package agent 平台无关部分。
package agent

import (
	"os"
	"runtime"

	"remoteconsole/internal/protocol"
)

// 每个平台实现:
//
//	sysProcAttr() *syscall.SysProcAttr
//	collectMetrics(hostname string) protocol.Metrics
//	kernelInfo() string
//
// agent.go 通过 unameInfo() 取内核版本字符串,内部路由到 kernelInfo()。

func unameInfo() string { return kernelInfo() }

func collectMetrics(hostname string) protocol.Metrics {
	m := collect(hostname)
	if m.Hostname == "" {
		m.Hostname, _ = os.Hostname()
	}
	return m
}

func osInfo() (goos, arch string) { return runtime.GOOS, runtime.GOARCH }
