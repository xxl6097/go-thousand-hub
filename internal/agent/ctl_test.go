package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

	script := uninstallScript()
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
