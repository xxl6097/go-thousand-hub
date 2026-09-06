// Package protocol 定义服务端 <-> Agent <-> 浏览器控制台三端之间的
// WebSocket JSON 信封协议,以及所有消息的类型常量与数据负载结构。
package protocol

import "encoding/json"

// 消息信封。所有方向的消息都使用同一信封结构,具体负载放在 Data 里。
// PTY 的字节流数据一律 base64 编码进 DataB64,避免 UTF-8/二进制被 JSON 破坏。
type Envelope struct {
	Type    string          `json:"type"`
	AgentID string          `json:"agent_id,omitempty"`
	TermID  string          `json:"term_id,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// ---------- 消息类型常量 ----------

// Agent -> Server
const (
	MsgHello    = "hello"     // agent 注册握手(连接建立后立即发送)
	MsgMetrics  = "metrics"   // 周期指标心跳
	MsgPtyOut   = "pty_output" // PTY 输出回传
	MsgPtyExit  = "pty_exit"  // PTY 进程退出
	MsgPtyErr   = "pty_error" // PTY 相关错误
)

// Server -> Agent
const (
	MsgOpenPty  = "pty_open"  // 打开 PTY(启动 shell)
	MsgPtyIn    = "pty_input" // 写入 PTY 输入
	MsgPtyResize = "pty_resize"
	MsgPtyKill  = "pty_kill"  // 强制结束 PTY 会话
)

// Console(浏览器) -> Server
const (
	MsgTermOpen  = "open"
	MsgTermIn    = "input"
	MsgTermResize = "resize"
	MsgTermClose = "close"
)

// Server -> Console(浏览器)
const (
	MsgTermOut  = "output"
	MsgTermExit = "exit"
	MsgTermErr  = "error"
)

// 主机控制(Console -> Server -> Agent)
const (
	MsgHostCtl    = "host_ctl"
	MsgHostCtlRes = "host_ctl_result"
)

// 主机控制动作
const (
	CtlReboot       = "reboot"        // 重启被管主机
	CtlShutdown     = "shutdown"      // 关机
	CtlRestartAgent = "restart_agent" // 重启本机 agent(systemd 托管时自动拉起)
	CtlUninstall    = "uninstall"     // 彻底卸载 agent 自身
)

// ---------- 数据负载结构 ----------

type Hello struct {
	Version  string `json:"version"`
	Hostname string `json:"hostname"`
	Name     string `json:"name,omitempty"` // 展示名(可自定义,默认主机名)
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Kernel   string `json:"kernel"`
	IP       string `json:"ip"`
	Shell    string `json:"shell"`
	BootAt   int64  `json:"boot_at"` // 进程启动时间戳(秒)
}

// Metrics 客户端主机实时指标
type Metrics struct {
	Hostname  string  `json:"hostname"`
	UptimeSec int64   `json:"uptime_sec"`
	Load1     float64 `json:"load1"`
	Load5     float64 `json:"load5"`
	Load15    float64 `json:"load15"`
	CPU       float64 `json:"cpu_percent"` // 0-100
	MemTotal  uint64  `json:"mem_total"`
	MemUsed   uint64  `json:"mem_used"`
	MemAvail  uint64  `json:"mem_avail"`
	DiskTotal uint64  `json:"disk_total"`
	DiskUsed  uint64  `json:"disk_used"`
	Procs     int     `json:"procs"`
	NowUnix   int64   `json:"now_unix"`
}

type PtyOpen struct {
	TermID string `json:"term_id"`
	Cols   uint16 `json:"cols"`
	Rows   uint16 `json:"rows"`
	Cmd    string `json:"cmd,omitempty"` // 为空则启动 $SHELL
	Term   string `json:"term,omitempty"` // TERM 环境变量
}

type PtyIO struct {
	TermID string `json:"term_id"`
	Data64 string `json:"data_b64"` // base64 字节流
}

type PtyResize struct {
	TermID string `json:"term_id"`
	Cols   uint16 `json:"cols"`
	Rows   uint16 `json:"rows"`
}

type PtyExit struct {
	TermID string `json:"term_id"`
	Code   int    `json:"code"`
}

type PtyErr struct {
	TermID string `json:"term_id"`
	Msg    string `json:"msg"`
}

// Console 打开终端请求
type TermOpen struct {
	AgentID string `json:"agent_id"`
	TermID  string `json:"term_id"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
	Cmd     string `json:"cmd,omitempty"`
}

// 会话错误推送(Server -> Console)
type TermErr struct {
	TermID string `json:"term_id"`
	Msg    string `json:"msg"`
}

type TermExit struct {
	TermID string `json:"term_id"`
	Code   int    `json:"code"`
	Msg    string `json:"msg"`
}

// HostCtl 主机控制指令负载(action 见 Ctl* 常量;agent_id 走信封 AgentID 字段)
type HostCtl struct {
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// HostCtlResult 主机控制回执(agent 先回执,再执行破坏性动作)
type HostCtlResult struct {
	Action string `json:"action"`
	Ok     bool   `json:"ok"`
	Msg    string `json:"msg"`
}

// ---------- 小工具 ----------

func Enc[T any](v T) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func Dec[T any](raw json.RawMessage) (T, error) {
	var v T
	err := json.Unmarshal(raw, &v)
	return v, err
}
