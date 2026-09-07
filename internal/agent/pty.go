package agent

import (
	"encoding/base64"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/xxl6097/go-thousand-hub/internal/protocol"
)

// ptySession 一个远程终端会话:PTY + 子进程(shell)
type ptySession struct {
	agent  *Agent
	conn   *websocket.Conn
	termID string
	ptmx   *os.File
	cmd    *exec.Cmd
	closed bool
	mu     sync.Mutex
}

// openPty 在 agent 上启动一个 PTY shell
func (a *Agent) openPty(c *websocket.Conn, req protocol.PtyOpen) error {
	a.sessMu.Lock()
	if _, dup := a.sess[req.TermID]; dup {
		a.sessMu.Unlock()
		return a.write(c, protocol.Envelope{Type: protocol.MsgPtyErr, AgentID: a.id, Data: protocol.Enc(protocol.PtyErr{TermID: req.TermID, Msg: "term already open"})})
	}
	a.sessMu.Unlock()

	shell := strings.TrimSpace(req.Cmd)
	args := []string{}
	if shell == "" {
		shell = a.shell
	} else {
		parts := strings.Fields(shell)
		shell = parts[0]
		args = parts[1:]
	}

	cmd := exec.Command(shell, args...)
	if strings.TrimSpace(req.Cmd) == "" {
		// 默认登录式交互 shell,模拟 ssh 登录体验
		cmd.Args = append([]string{shell}, "-l")
	}
	term := req.Term
	if term == "" {
		term = "xterm-256color"
	}
	cmd.Env = append(cleanEnv(os.Environ(), term), "TERM="+term)
	// 说明:creack/pty 会自行设置 Setsid(子进程自成会话与进程组),
	// 因此无需再额外 Setpgid —— 杀掉 -pid 即可整组回收。

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: req.Cols, Rows: req.Rows})
	if err != nil {
		log.Printf("[pty] 启动失败 term=%s err=%v", req.TermID, err)
		return a.write(c, protocol.Envelope{Type: protocol.MsgPtyErr, AgentID: a.id, Data: protocol.Enc(protocol.PtyErr{TermID: req.TermID, Msg: "pty start failed: " + err.Error()})})
	}
	log.Printf("[pty] 已启动 term=%s shell=%s pid=%d", req.TermID, cmd.Path, cmd.Process.Pid)

	s := &ptySession{agent: a, conn: c, termID: req.TermID, ptmx: f, cmd: cmd}
	a.sessMu.Lock()
	a.sess[req.TermID] = s
	a.sessMu.Unlock()

	// 拷贝 PTY 输出 -> 服务端
	go s.pumpOutput()

	// 进程结束 -> 通知并清理
	go func() {
		_ = cmd.Wait()
		a.sessMu.Lock()
		if cur, ok := a.sess[req.TermID]; ok && cur == s {
			delete(a.sess, req.TermID)
		}
		a.sessMu.Unlock()
		s.markClosed()

		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		_ = a.write(c, protocol.Envelope{Type: protocol.MsgPtyExit, AgentID: a.id, Data: protocol.Enc(protocol.PtyExit{TermID: req.TermID, Code: code})})
	}()
	return nil
}

// pumpOutput 持续读取 PTY 输出并 base64 上报
func (s *ptySession) pumpOutput() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if !closed {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				msg := base64.StdEncoding.EncodeToString(chunk)
				_ = s.agent.write(s.conn, protocol.Envelope{Type: protocol.MsgPtyOut, AgentID: s.agent.id, Data: protocol.Enc(protocol.PtyIO{TermID: s.termID, Data64: msg})})
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *ptySession) writeInput(data64 string) error {
	raw, err := base64.StdEncoding.DecodeString(data64)
	if err != nil {
		return err
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return io.EOF
	}
	_, err = s.ptmx.Write(raw)
	return err
}

func (s *ptySession) resize(cols, rows uint16) {
	if cols == 0 || rows == 0 {
		return
	}
	_ = pty.Setsize(s.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

func (s *ptySession) kill() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL) // 杀整个进程组
		_ = s.cmd.Process.Kill()
	}
	_ = s.ptmx.Close()
}

func (s *ptySession) markClosed() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	_ = s.ptmx.Close()
}

// cleanEnv 过滤影响终端显示的变量并统一 locale,避免输出乱码
func cleanEnv(env []string, term string) []string {
	out := make([]string, 0, len(env)+3)
	for _, e := range env {
		if strings.HasPrefix(e, "TERM=") || strings.HasPrefix(e, "LC_ALL=") {
			continue
		}
		out = append(out, e)
	}
	out = append(out, "LC_ALL=C.UTF-8", "LANG=C.UTF-8")
	return out
}
