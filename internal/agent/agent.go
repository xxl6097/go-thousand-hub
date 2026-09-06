// Package agent 实现被管主机上的常驻客户端:
//   - 主动反向连接服务端(无需被管机开放任何入站端口,NAT 后也可用)
//   - 周期上报主机指标(CPU/内存/磁盘/负载/uptime)
//   - 接收服务端指令,为浏览器控制台打开真实 PTY shell(默认 root,可任意切换用户执行所有命令)
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"remoteconsole/internal/protocol"
)

const Version = "1.0.0"

// Options 运行参数(由 main 从环境变量/flag 组装)
type Options struct {
	ServerURL string // 形如 ws://host:port/ws/agent 或 wss://...
	Token     string // 与服务器共享的 agent 认证令牌
	IDFile    string // 持久化 agent ID 的文件路径
	Name      string // 展示名(默认取主机名)
	Interval  time.Duration // 指标上报周期
}

// Agent 常驻客户端
type Agent struct {
	opts    Options
	id      string
	name    string // 展示名(默认真实主机名)
	host    string // 真实主机名
	shell   string
	startAt time.Time

	mu      sync.Mutex
	conn    *websocket.Conn
	writeMu sync.Mutex

	sessMu sync.Mutex
	sess   map[string]*ptySession
}

func New(opts Options) *Agent {
	host, _ := os.Hostname()
	a := &Agent{
		opts:    opts,
		shell:   defaultShell(),
		host:    host,
		startAt: time.Now(),
		sess:    map[string]*ptySession{},
	}
	a.id = loadOrCreateID(opts.IDFile)
	a.name = opts.Name
	if a.name == "" {
		a.name = host
	}
	return a
}

// Run 主循环:启动信号监听 + 无限重连
func (a *Agent) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		a.closeAllSessions("agent shutdown")
		a.mu.Lock()
		if a.conn != nil {
			_ = a.conn.Close(websocket.StatusNormalClosure, "bye")
		}
		a.mu.Unlock()
	}()

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}
		err := a.connectOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		log.Printf("连接断开: %v, %s 后重连", err, backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) connectOnce(ctx context.Context) error {
	log.Printf("连接服务端 %s (id=%s name=%s shell=%s)", a.opts.ServerURL, a.id, a.name, a.shell)

	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+a.opts.Token)
	c, _, err := websocket.Dial(ctx, a.opts.ServerURL, &websocket.DialOptions{
		HTTPHeader: hdr,
	})
	if err != nil {
		return err
	}
	c.SetReadLimit(1 << 24) // 16MB

	a.mu.Lock()
	a.conn = c
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.conn == c {
			a.conn = nil
		}
		a.mu.Unlock()
		_ = c.Close(websocket.StatusNormalClosure, "gone")
	}()

	if err := a.sendHello(c); err != nil {
		return err
	}

	// 指标心跳
	go a.metricsLoop(ctx, c)

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return err
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			log.Printf("非法消息: %v", err)
			continue
		}
		if err := a.handleCommand(ctx, c, env); err != nil {
			log.Printf("处理命令 %s 失败: %v", env.Type, err)
		}
	}
}

func (a *Agent) sendHello(c *websocket.Conn) error {
	goos, arch := osInfo()
	hello := protocol.Hello{
		Version:  Version,
		Hostname: a.host,
		Name:     a.name,
		OS:       goos,
		Arch:     arch,
		Kernel:   unameInfo(),
		IP:       outboundIP(),
		Shell:    a.shell,
		BootAt:   a.startAt.Unix(),
	}
	return a.write(c, protocol.Envelope{Type: protocol.MsgHello, AgentID: a.id, Data: protocol.Enc(hello)})
}

func (a *Agent) metricsLoop(ctx context.Context, c *websocket.Conn) {
	t := time.NewTicker(a.opts.Interval)
	defer t.Stop()
	a.reportMetrics(c)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.reportMetrics(c)
		}
	}
}

func (a *Agent) reportMetrics(c *websocket.Conn) {
	m := collectMetrics(a.host)
	m.NowUnix = time.Now().Unix()
	env := protocol.Envelope{Type: protocol.MsgMetrics, AgentID: a.id, Data: protocol.Enc(m)}
	if err := a.write(c, env); err != nil {
		log.Printf("指标上报失败: %v", err)
	}
}

// handleCommand 处理服务端下发的控制指令(均可能并发,内部自带锁)
func (a *Agent) handleCommand(ctx context.Context, c *websocket.Conn, env protocol.Envelope) error {
	switch env.Type {
	case protocol.MsgOpenPty:
		var req protocol.PtyOpen
		if err := json.Unmarshal(env.Data, &req); err != nil {
			return a.write(c, protocol.Envelope{Type: protocol.MsgPtyErr, AgentID: a.id, Data: protocol.Enc(protocol.PtyErr{Msg: "bad pty_open: " + err.Error()})})
		}
		return a.openPty(c, req)

	case protocol.MsgPtyIn:
		var io protocol.PtyIO
		if err := json.Unmarshal(env.Data, &io); err != nil {
			return err
		}
		a.sessMu.Lock()
		s := a.sess[io.TermID]
		a.sessMu.Unlock()
		if s == nil {
			return nil // 会话已不存在,忽略输入
		}
		return s.writeInput(io.Data64)

	case protocol.MsgPtyResize:
		var rz protocol.PtyResize
		if err := json.Unmarshal(env.Data, &rz); err != nil {
			return err
		}
		a.sessMu.Lock()
		s := a.sess[rz.TermID]
		a.sessMu.Unlock()
		if s != nil {
			s.resize(rz.Cols, rz.Rows)
		}
		return nil

	case protocol.MsgPtyKill:
		a.sessMu.Lock()
		s := a.sess[env.TermID]
		a.sessMu.Unlock()
		if s != nil {
			s.kill()
		}
		return nil

	case protocol.MsgHostCtl:
		return a.handleHostCtl(c, env)
	}
	return nil
}

func (a *Agent) closeAllSessions(reason string) {
	a.sessMu.Lock()
	ss := make([]*ptySession, 0, len(a.sess))
	for _, s := range a.sess {
		ss = append(ss, s)
	}
	a.sessMu.Unlock()
	for _, s := range ss {
		s.kill()
	}
}

func (a *Agent) write(c *websocket.Conn, env protocol.Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, data)
}

// ---------- 工具 ----------

func defaultShell() string {
	if s := os.Getenv("SHELL"); strings.TrimSpace(s) != "" {
		return s
	}
	for _, s := range []string{"/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(s); err == nil {
			return s
		}
	}
	return "bash"
}

// loadOrCreateID 持久化一个随机 agent ID,保证重连后身份不变
func loadOrCreateID(path string) string {
	if b, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(b))
		if len(id) == 32 {
			return id
		}
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		buf = []byte(fmt.Sprintf("%d", time.Now().UnixNano()))
	}
	id := hex.EncodeToString(buf)
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	_ = os.WriteFile(path, []byte(id+"\n"), 0o600)
	return id
}

// outboundIP 尽力探测本机出口 IP,仅作展示
func outboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr := conn.LocalAddr().String()
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}
