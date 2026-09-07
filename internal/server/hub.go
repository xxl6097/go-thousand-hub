package server

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"

	"remoteconsole/internal/protocol"
)

// AgentInfo 主机在服务端的注册视图(含最近一次指标)
type AgentInfo struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Hostname    string            `json:"hostname"`
	OS          string            `json:"os"`
	Arch        string            `json:"arch"`
	Kernel      string            `json:"kernel"`
	IP          string            `json:"ip"`
	Shell       string            `json:"shell"`
	Version     string            `json:"version"`
	Online      bool              `json:"online"`
	BootAt      int64             `json:"boot_at"`
	ConnectedAt int64             `json:"connected_at"`
	LastSeen    int64             `json:"last_seen"`
	M           *protocol.Metrics `json:"metrics,omitempty"`
}

// hub 持有:当前在线 agent、历史注册主机、终端会话路由、主机控制归属
type hub struct {
	mu         sync.Mutex
	online     map[string]*agentConn
	known      map[string]*AgentInfo
	terms      map[string]*termRoute
	ctlOwners  map[string]*consoleConn // agentID -> 最近一次发起 host_ctl 的控制台(用于回执路由)
}

// agentConn agent 侧连接(服务端视图)
type agentConn struct {
	hub     *hub
	id      string
	info    *AgentInfo
	ws      *websocket.Conn
	writeMu sync.Mutex
}

// consoleConn 浏览器控制台连接(服务端视图)
type consoleConn struct {
	hub     *hub
	ws      *websocket.Conn
	writeMu sync.Mutex
}

// termRoute 一个终端会话的路由:属于哪个 agent、归哪个控制台所有
type termRoute struct {
	agentID string
	console *consoleConn
}

func newHub() *hub {
	return &hub{
		online:    map[string]*agentConn{},
		known:     map[string]*AgentInfo{},
		terms:     map[string]*termRoute{},
		ctlOwners: map[string]*consoleConn{},
	}
}

// snapshot 返回全部已知主机的状态视图(在线状态实时计算)
func (h *hub) snapshot() []AgentInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]AgentInfo, 0, len(h.known))
	for _, info := range h.known {
		c := AgentInfo(*info)
		c.Online = false
		if _, ok := h.online[info.ID]; ok {
			c.Online = true
		}
		out = append(out, c)
	}
	return out
}

func (h *hub) agent(id string) *agentConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.online[id]
}

// ---------- Agent 连接处理 ----------

func (h *hub) handleAgent(ctx context.Context, ws *websocket.Conn) {
	// 1. 首条消息必须是 hello
	firstCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	_, data, err := ws.Read(firstCtx)
	cancel()
	if err != nil {
		log.Printf("[agent] 握手读取失败: %v", err)
		_ = ws.Close(websocket.StatusPolicyViolation, "handshake timeout")
		return
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Type != protocol.MsgHello {
		log.Printf("[agent] 首条消息非法")
		_ = ws.Close(websocket.StatusPolicyViolation, "bad hello")
		return
	}
	hello, err := protocol.Dec[protocol.Hello](env.Data)
	if err != nil || env.AgentID == "" {
		log.Printf("[agent] hello 负载非法: %v", err)
		_ = ws.Close(websocket.StatusPolicyViolation, "bad hello")
		return
	}

	now := time.Now().Unix()
	h.mu.Lock()
	if old, ok := h.online[env.AgentID]; ok {
		old.forceClose("replaced by new connection")
	}
	info := &AgentInfo{
		ID:          env.AgentID,
		Name:        firstNonEmpty(hello.Name, hello.Hostname, env.AgentID),
		Hostname:    hello.Hostname,
		OS:          hello.OS,
		Arch:        hello.Arch,
		Kernel:      hello.Kernel,
		IP:          hello.IP,
		Shell:       hello.Shell,
		Version:     hello.Version,
		Online:      true,
		BootAt:      hello.BootAt,
		ConnectedAt: now,
		LastSeen:    now,
	}
	h.known[env.AgentID] = info
	ac := &agentConn{hub: h, id: env.AgentID, info: info, ws: ws}
	h.online[env.AgentID] = ac
	h.mu.Unlock()

	log.Printf("[agent] 上线: id=%s name=%s host=%s os=%s/%s kernel=%s ip=%s",
		env.AgentID, info.Name, hello.Hostname, hello.OS, hello.Arch, hello.Kernel, hello.IP)

	defer func() {
		h.mu.Lock()
		if cur, ok := h.online[env.AgentID]; ok && cur == ac {
			delete(h.online, env.AgentID)
		}
		delete(h.ctlOwners, env.AgentID)
		h.mu.Unlock()
		h.notifyTermsAgentGone(env.AgentID)
		log.Printf("[agent] 离线: id=%s name=%s", env.AgentID, info.Name)
		_ = ws.Close(websocket.StatusNormalClosure, "bye")
	}()

	for {
		_, data, err := ws.Read(context.Background())
		if err != nil {
			return
		}
		var e protocol.Envelope
		if err := json.Unmarshal(data, &e); err != nil {
			continue
		}
		h.mu.Lock()
		info.LastSeen = time.Now().Unix()
		h.mu.Unlock()

		switch e.Type {
		case protocol.MsgMetrics:
			if m, err := protocol.Dec[protocol.Metrics](e.Data); err == nil {
				h.mu.Lock()
				info.M = &m
				h.mu.Unlock()
			}
		case protocol.MsgPtyOut:
			if io, err := protocol.Dec[protocol.PtyIO](e.Data); err == nil {
				h.forwardToTerm(io.TermID, protocol.Envelope{
					Type:   protocol.MsgTermOut,
					TermID: io.TermID,
					Data:   e.Data,
				})
			}
		case protocol.MsgPtyExit:
			if ex, err := protocol.Dec[protocol.PtyExit](e.Data); err == nil {
				h.forwardToTerm(ex.TermID, protocol.Envelope{
					Type:   protocol.MsgTermExit,
					TermID: ex.TermID,
					Data:   e.Data,
				})
				h.releaseTerm(ex.TermID)
			}
		case protocol.MsgPtyErr:
			if pe, err := protocol.Dec[protocol.PtyErr](e.Data); err == nil {
				log.Printf("[relay] agent %s pty_error term=%s msg=%s", e.AgentID, pe.TermID, pe.Msg)
				h.forwardToTerm(pe.TermID, protocol.Envelope{
					Type:   protocol.MsgTermErr,
					TermID: pe.TermID,
					Data:   e.Data,
				})
				h.releaseTerm(pe.TermID)
			}
		case protocol.MsgHostCtlRes:
			// 主机控制回执 -> 回传给发起该主机控制的控制台
			h.ctlResult(e)
		}
	}
}

func (ac *agentConn) forceClose(reason string) {
	ac.writeMu.Lock()
	_ = ac.ws.Close(websocket.StatusNormalClosure, reason)
	ac.writeMu.Unlock()
}

func (ac *agentConn) send(env protocol.Envelope) bool {
	data, err := json.Marshal(env)
	if err != nil {
		return false
	}
	ac.writeMu.Lock()
	defer ac.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return ac.ws.Write(ctx, websocket.MessageText, data) == nil
}

// consoleConn.send 推送消息给浏览器控制台
func (cc *consoleConn) send(env protocol.Envelope) bool {
	data, err := json.Marshal(env)
	if err != nil {
		return false
	}
	cc.writeMu.Lock()
	defer cc.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return cc.ws.Write(ctx, websocket.MessageText, data) == nil
}

// ---------- 控制台连接处理 ----------

// handleConsole 浏览器控制台消息循环。
// 控制台持有全局唯一 termID,服务端据此路由输入/输出。
func (h *hub) handleConsole(ctx context.Context, ws *websocket.Conn) {
	cc := &consoleConn{hub: h, ws: ws}
	defer func() {
		h.cleanupConsoleTerms(cc)
		h.releaseCtlOwner(cc)
		_ = ws.Close(websocket.StatusNormalClosure, "bye")
	}()

	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		switch env.Type {
		case protocol.MsgTermOpen:
			req, err := protocol.Dec[protocol.TermOpen](env.Data)
			if err != nil {
				continue
			}
			h.openTerm(cc, req)
		case protocol.MsgTermIn:
			io, err := protocol.Dec[protocol.PtyIO](env.Data)
			if err != nil {
				continue
			}
			h.consoleIO(cc, io.TermID, protocol.MsgPtyIn, eData(env.Data))
		case protocol.MsgTermResize:
			rz, err := protocol.Dec[protocol.PtyResize](env.Data)
			if err != nil {
				continue
			}
			h.consoleIO(cc, rz.TermID, protocol.MsgPtyResize, eData(env.Data))
		case protocol.MsgTermClose:
			io, err := protocol.Dec[protocol.PtyIO](env.Data)
			if err != nil {
				continue
			}
			h.closeTerm(cc, io.TermID)
		case protocol.MsgHostCtl:
			h.ctlFromConsole(cc, env)
		}
	}
}

// ---------- 主机控制路由 ----------

// ctlFromConsole 控制台发起主机控制:校验动作白名单 -> 在线校验 -> 下发 agent 并登记回执归属
func (h *hub) ctlFromConsole(cc *consoleConn, env protocol.Envelope) {
	ctl, err := protocol.Dec[protocol.HostCtl](env.Data)
	if err != nil || !isCtlAction(ctl.Action) {
		cc.send(protocol.Envelope{Type: protocol.MsgHostCtlRes, AgentID: env.AgentID,
			Data: protocol.Enc(protocol.HostCtlResult{Action: ctl.Action, Ok: false, Msg: "不支持的控制动作"})})
		return
	}
	if env.AgentID == "" {
		cc.send(protocol.Envelope{Type: protocol.MsgHostCtlRes, AgentID: env.AgentID,
			Data: protocol.Enc(protocol.HostCtlResult{Action: ctl.Action, Ok: false, Msg: "缺少主机 ID"})})
		return
	}
	ac := h.agent(env.AgentID)
	if ac == nil {
		cc.send(protocol.Envelope{Type: protocol.MsgHostCtlRes, AgentID: env.AgentID,
			Data: protocol.Enc(protocol.HostCtlResult{Action: ctl.Action, Ok: false, Msg: "主机不在线"})})
		return
	}
	h.mu.Lock()
	h.ctlOwners[env.AgentID] = cc
	h.mu.Unlock()
	if !ac.send(protocol.Envelope{Type: protocol.MsgHostCtl, AgentID: env.AgentID, Data: eData(env.Data)}) {
		h.releaseCtl(env.AgentID)
		cc.send(protocol.Envelope{Type: protocol.MsgHostCtlRes, AgentID: env.AgentID,
			Data: protocol.Enc(protocol.HostCtlResult{Action: ctl.Action, Ok: false, Msg: "下发失败,请重试"})})
	}
}

// ctlResult agent 回执 -> 回传发起控制台
func (h *hub) ctlResult(env protocol.Envelope) {
	h.mu.Lock()
	cc := h.ctlOwners[env.AgentID]
	h.mu.Unlock()
	if cc == nil {
		return
	}
	cc.send(protocol.Envelope{Type: protocol.MsgHostCtlRes, AgentID: env.AgentID, Data: eData(env.Data)})
}

func (h *hub) releaseCtl(agentID string) {
	h.mu.Lock()
	delete(h.ctlOwners, agentID)
	h.mu.Unlock()
}

// releaseCtlOwner 控制台退出时清理其持有的控制归属
func (h *hub) releaseCtlOwner(cc *consoleConn) {
	h.mu.Lock()
	for id, owner := range h.ctlOwners {
		if owner == cc {
			delete(h.ctlOwners, id)
		}
	}
	h.mu.Unlock()
}

func isCtlAction(a string) bool {
	switch a {
	case protocol.CtlReboot, protocol.CtlShutdown, protocol.CtlRestartAgent, protocol.CtlUninstall:
		return true
	}
	return false
}

// openTerm 控制台请求在某 agent 上打开终端
func (h *hub) openTerm(cc *consoleConn, req protocol.TermOpen) {
	if req.TermID == "" || req.AgentID == "" {
		cc.send(protocol.Envelope{Type: protocol.MsgTermErr, TermID: req.TermID, Data: protocol.Enc(protocol.TermErr{TermID: req.TermID, Msg: "参数不完整"})})
		return
	}
	ac := h.agent(req.AgentID)
	if ac == nil {
		cc.send(protocol.Envelope{Type: protocol.MsgTermErr, TermID: req.TermID, Data: protocol.Enc(protocol.TermErr{TermID: req.TermID, Msg: "主机不在线"})})
		return
	}
	if ac.info.Shell != "" {
		// 允许控制台指定 shell;默认用 agent 默认 shell
	}
	h.mu.Lock()
	h.terms[req.TermID] = &termRoute{agentID: req.AgentID, console: cc}
	h.mu.Unlock()

	ok := ac.send(protocol.Envelope{
		Type:   protocol.MsgOpenPty,
		AgentID: req.AgentID,
		Data: protocol.Enc(protocol.PtyOpen{
			TermID: req.TermID,
			Cols:   req.Cols,
			Rows:   req.Rows,
			Cmd:    req.Cmd,
		}),
	})
	if !ok {
		h.releaseTerm(req.TermID)
		cc.send(protocol.Envelope{Type: protocol.MsgTermErr, TermID: req.TermID, Data: protocol.Enc(protocol.TermErr{TermID: req.TermID, Msg: "下发失败,请重试"})})
	}
}

func (h *hub) consoleIO(cc *consoleConn, termID, typ string, raw []byte) {
	tr := h.term(termID)
	if tr == nil || tr.console != cc {
		return // 非本控制台持有的终端,忽略(防串扰)
	}
	ac := h.agent(tr.agentID)
	if ac == nil {
		return
	}
	ac.send(protocol.Envelope{Type: typ, TermID: termID, Data: raw})
}

func (h *hub) closeTerm(cc *consoleConn, termID string) {
	h.mu.Lock()
	tr, ok := h.terms[termID]
	if ok && tr.console == cc {
		delete(h.terms, termID)
	}
	h.mu.Unlock()
	if ok && tr.console == cc {
		if ac := h.agent(tr.agentID); ac != nil {
			ac.send(protocol.Envelope{Type: protocol.MsgPtyKill, TermID: termID})
		}
	}
}

func (h *hub) term(termID string) *termRoute {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.terms[termID]
}

func (h *hub) releaseTerm(termID string) {
	h.mu.Lock()
	delete(h.terms, termID)
	h.mu.Unlock()
}

func (h *hub) forwardToTerm(termID string, env protocol.Envelope) {
	tr := h.term(termID)
	if tr == nil {
		return
	}
	tr.console.send(env)
}

// notifyTermsAgentGone agent 掉线:其名下终端全部失效,通知控制台
func (h *hub) notifyTermsAgentGone(agentID string) {
	h.mu.Lock()
	var notify []*consoleConn
	seen := map[*consoleConn]bool{}
	for id, tr := range h.terms {
		if tr.agentID == agentID {
			delete(h.terms, id)
			if !seen[tr.console] {
				seen[tr.console] = true
				notify = append(notify, tr.console)
			}
		}
	}
	h.mu.Unlock()
	for _, c := range notify {
		c.send(protocol.Envelope{Type: protocol.MsgTermErr, Data: protocol.Enc(protocol.TermErr{Msg: "主机连接已断开,终端会话结束"})})
	}
}

// cleanupConsoleTerms 控制台退出:其所有终端通知对应 agent 关闭
func (h *hub) cleanupConsoleTerms(cc *consoleConn) {
	h.mu.Lock()
	byAgent := map[string][]string{}
	for id, tr := range h.terms {
		if tr.console == cc {
			byAgent[tr.agentID] = append(byAgent[tr.agentID], id)
			delete(h.terms, id)
		}
	}
	h.mu.Unlock()
	for agentID, termIDs := range byAgent {
		if ac := h.agent(agentID); ac != nil {
			for _, termID := range termIDs {
				ac.send(protocol.Envelope{Type: protocol.MsgPtyKill, TermID: termID})
			}
		}
	}
}

func eData(raw []byte) []byte { return raw }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
