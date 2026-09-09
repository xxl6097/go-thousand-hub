// 附带一个开箱即用的 HTTP 回调实现(HTTPRemote):
// 第三方开发者无需改动 Go 代码,只要按下方约定提供任意语言实现的
// 升级服务(检测/执行),再给 rc-server 配 RC_UPDATE_URL 即可接入。
// 同时它也作为"如何实现 Updater 接口"的最小参考示例。
package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// NewHTTPRemote 返回一个基于 HTTP 回调的 Updater 实现。
//
// 它把"检测/执行升级"转发给第三方升级服务(base 必填,如 https://upd.example.com/api/upgrade):
//   - GET  {base}/check   → 200 + Release JSON 表示有新版本;204 No Content 表示已是最新;
//   - POST {base}/apply   → 请求体 {"version":"v1.3.0"};2xx 表示升级已受理。
//
// 这是接口的标准参考实现:第三方若希望用 Go 直接内嵌实现,可照此结构
// 自建类型并实现 Updater,注入 server.Config.Updater(见 cmd/server/main.go)。
func NewHTTPRemote(base string) Updater {
	base = strings.TrimRight(base, "/")
	return &httpRemote{base: base, cli: &http.Client{Timeout: 60 * time.Second}}
}

type httpRemote struct {
	base string
	cli  *http.Client
}

func (h *httpRemote) Check(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+"/check", nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("升级服务不可达: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNoContent:
		return nil, nil // 已是最新
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented:
		return nil, fmt.Errorf("升级服务未实现 /check 接口(HTTP %d)", resp.StatusCode)
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		var rel Release
		if err := json.Unmarshal(body, &rel); err != nil {
			return nil, fmt.Errorf("升级服务 /check 返回非法 JSON: %w", err)
		}
		if rel.Version == "" {
			return nil, fmt.Errorf("升级服务 /check 返回缺少 version 字段")
		}
		return &rel, nil
	default:
		return nil, fmt.Errorf("升级服务 /check 失败(HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func (h *httpRemote) Apply(ctx context.Context, rel *Release) error {
	payload, _ := json.Marshal(map[string]string{"version": rel.Version})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.base+"/apply", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.cli.Do(req)
	if err != nil {
		return fmt.Errorf("升级服务不可达: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("升级服务 /apply 失败(HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
