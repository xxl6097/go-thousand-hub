package agent

import (
	"testing"

	"github.com/xxl6097/go-thousand-hub/pkg/agent/m"
)

// 版本号扩展:第三方可通过 m.Options.Version 覆盖,未指定时回退到编译期注入值/内置版本
func TestAgentVersionDefault(t *testing.T) {
	a := New(m.Options{})
	if got := a.version(); got != DefaultVersion {
		t.Fatalf("默认版本应为 %q,实际 %q", DefaultVersion, got)
	}
}

func TestAgentVersionCustom(t *testing.T) {
	a := New(m.Options{Version: "v2.3.4"})
	if got := a.version(); got != "v2.3.4" {
		t.Fatalf("业务定制版本未生效,实际 %q", got)
	}
}

func TestAgentVersionTrimSpace(t *testing.T) {
	a := New(m.Options{Version: "  v9.9.9-beta  "})
	if got := a.version(); got != "v9.9.9-beta" {
		t.Fatalf("版本号应去除首尾空白,实际 %q", got)
	}
}

// 未定制时应取编译期 -ldflags 注入的 Version 变量
func TestAgentVersionFallbackToBuildVar(t *testing.T) {
	old := Version
	Version = "build-1.2.3"
	defer func() { Version = old }()

	a := New(m.Options{})
	if got := a.version(); got != "build-1.2.3" {
		t.Fatalf("应回退到编译期注入版本,实际 %q", got)
	}
	// 业务定制仍优先于编译期注入
	b := New(m.Options{Version: "biz-3.0.0"})
	if got := b.version(); got != "biz-3.0.0" {
		t.Fatalf("业务定制应优先,实际 %q", got)
	}
}

// 卸载扩展点上下文里的 Version 也应为生效版本
func TestUninstallInfoVersion(t *testing.T) {
	a := New(m.Options{Version: "v2.3.4"})
	info := a.uninstallInfo()
	if info.Version != "v2.3.4" {
		t.Fatalf("UninstallInfo.Version 应为生效版本,实际 %q", info.Version)
	}
	if info.AgentID == "" || info.Name == "" {
		t.Fatalf("UninstallInfo 基础字段缺失: %+v", info)
	}
}
