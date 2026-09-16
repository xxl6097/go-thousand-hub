package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xxl6097/go-thousand-hub/pkg/agent/m"
)

// TestUninstallScriptRemovesArtifacts 真实执行卸载清理脚本(带 RC_UNINSTALL_PREFIX 前缀),
// 验证二进制/配置/持久化 ID/systemd 单元全部被删除,且脚本自身不被误杀、正常退出。
func TestUninstallScriptRemovesArtifacts(t *testing.T) {
	root := t.TempDir()
	mk := func(p string) {
		fp := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 模拟 install-agent.sh 产生的全部痕迹
	mk("usr/local/bin/rc-agent")
	mk("usr/bin/rc-agent")
	mk("etc/rc-agent/rc-agent.conf")
	mk("etc/rc-agent.conf")
	mk("var/lib/rc-agent/id")
	mk("etc/systemd/system/rc-agent.service")
	mk("etc/systemd/system/rc-agent.service.d/override.conf")

	// 必须在生成脚本之前设置,脚本会把前缀内联进去(否则会指向真实根路径)
	t.Setenv("RC_UNINSTALL_PREFIX", root)
	t.Setenv("RC_SERVICE_NAME", "rc-agent")

	script := uninstallScript("")
	if script == "" {
		t.Fatal("empty script")
	}
	if !strings.Contains(script, "P='"+root+"'") {
		t.Fatalf("脚本未内联测试前缀,可能指向真实根路径,拒绝执行:\n%s", script)
	}

	out, err := exec.Command("/bin/sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("清理脚本执行失败: %v\n%s", err, out)
	}

	removed := []string{
		"usr/local/bin/rc-agent",
		"usr/bin/rc-agent",
		"etc/rc-agent/rc-agent.conf",
		"etc/rc-agent.conf",
		"var/lib/rc-agent/id",
		"etc/systemd/system/rc-agent.service",
		"etc/systemd/system/rc-agent.service.d/override.conf",
	}
	for _, p := range removed {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Errorf("应被删除但仍在: %s", p)
		}
	}
	// 配置目录应整目录消失
	for _, d := range []string{"etc/rc-agent", "var/lib/rc-agent", "etc/systemd/system/rc-agent.service.d"} {
		if _, err := os.Stat(filepath.Join(root, d)); !os.IsNotExist(err) {
			t.Errorf("目录应被删除但仍在: %s", d)
		}
	}
	t.Logf("清理脚本删除校验通过(共 %d 项)", len(removed))
}

// TestUninstallScriptExtraCleanup 验证业务附加清理段:
// ExtraCleanup 返回的脚本被真实执行,且排在「停服/杀进程」之前。
func TestUninstallScriptExtraCleanup(t *testing.T) {
	root := t.TempDir()
	biz := filepath.Join(root, "opt/myapp/sidecar.bin")
	if err := os.MkdirAll(filepath.Dir(biz), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(biz, []byte("biz"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RC_UNINSTALL_PREFIX", root)
	t.Setenv("RC_SERVICE_NAME", "rc-agent")

	// 业务附加清理:删除自己的侧车文件(脚本内可用 $P 前缀变量以便演练)
	extra := `rm -rf "$P/opt/myapp" 2>/dev/null || true`
	script := uninstallScript(extra)

	// 顺序断言:业务段必须在停服之前
	iBiz := strings.Index(script, "业务附加清理")
	iStop := strings.Index(script, `systemctl stop "$SVC"`)
	if iBiz < 0 {
		t.Fatalf("脚本缺少业务附加清理段:\n%s", script)
	}
	if iStop < 0 || iBiz > iStop {
		t.Fatalf("业务附加清理段必须排在停服之前(biz=%d stop=%d)", iBiz, iStop)
	}
	// 空 extra 不应产生该段
	if strings.Contains(uninstallScript("   "), "业务附加清理") {
		t.Fatal("空 extra 不应插入业务附加清理段")
	}

	out, err := exec.Command("/bin/sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("清理脚本执行失败: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "opt/myapp")); !os.IsNotExist(err) {
		t.Error("业务附加清理未生效:/opt/myapp 仍存在")
	}
}

// TestUninstallHookPhases 验证三个扩展点按顺序被调用、上下文完整,
// 且业务返回 error / panic 时不阻断(仅记录日志)。
func TestUninstallHookPhases(t *testing.T) {
	var got []string
	var gotInfo m.UninstallInfo

	a := &Agent{
		opts: m.Options{Hook: m.UninstallHookFuncs{
			OnBeforeUninstall: func(ctx context.Context, info m.UninstallInfo) error {
				got = append(got, "before")
				gotInfo = info
				return errors.New("业务上报失败(不应阻断)")
			},
			OnExtraCleanup: func(ctx context.Context, info m.UninstallInfo) string {
				got = append(got, "extra")
				return "rm -rf /tmp/whatever"
			},
			OnAfterUninstall: func(ctx context.Context, info m.UninstallInfo) error {
				got = append(got, "after")
				panic("业务收尾 panic(不应阻断)")
			},
		}},
		id: "id-123", name: "web-01", host: "web-01.local",
	}

	ctx := context.Background()
	info := a.uninstallInfo()
	a.hookBeforeUninstall(ctx, info)
	if extra := a.hookExtraCleanup(ctx, info); extra != "rm -rf /tmp/whatever" {
		t.Errorf("ExtraCleanup 返回值错误: %q", extra)
	}
	a.hookAfterUninstall(ctx, info)

	want := []string{"before", "extra", "after"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("扩展点调用顺序错误: got=%v want=%v", got, want)
	}
	if gotInfo.AgentID != "id-123" || gotInfo.Name != "web-01" || gotInfo.Host != "web-01.local" || gotInfo.Version == "" {
		t.Errorf("扩展点上下文不完整: %+v", gotInfo)
	}
}

// TestUninstallHookNilSafe 未注入扩展点时应为纯空操作,不 panic。
func TestUninstallHookNilSafe(t *testing.T) {
	a := &Agent{opts: m.Options{}, id: "x", name: "x", host: "x"}
	ctx := context.Background()
	info := a.uninstallInfo()
	a.hookBeforeUninstall(ctx, info)
	if extra := a.hookExtraCleanup(ctx, info); extra != "" {
		t.Errorf("无 Hook 时 ExtraCleanup 应为空串,got=%q", extra)
	}
	a.hookAfterUninstall(ctx, info)
}

// TestUninstallHookFuncsNilFields 函数式适配器只填部分字段时,其余阶段应为空操作。
func TestUninstallHookFuncsNilFields(t *testing.T) {
	h := m.UninstallHookFuncs{OnExtraCleanup: func(context.Context, m.UninstallInfo) string { return "echo hi" }}
	ctx := context.Background()
	if err := h.BeforeUninstall(ctx, m.UninstallInfo{}); err != nil {
		t.Errorf("未实现的 BeforeUninstall 应返回 nil,got=%v", err)
	}
	if err := h.AfterUninstall(ctx, m.UninstallInfo{}); err != nil {
		t.Errorf("未实现的 AfterUninstall 应返回 nil,got=%v", err)
	}
	if s := h.ExtraCleanup(ctx, m.UninstallInfo{}); s != "echo hi" {
		t.Errorf("ExtraCleanup 应透传函数返回值,got=%q", s)
	}
}
