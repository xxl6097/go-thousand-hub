package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Config 服务端配置
type Config struct {
	Listen    string // 监听地址,如 :8080
	AgentToken string // agent 接入令牌
	AdminUser string
	AdminPass string
	Secret    string // 会话签名随机源(默认每次启动随机,重启需重新登录)
	WebDir    string // 前端静态资源目录覆盖(可选)
	TLS       bool
	CertFile  string
	KeyFile   string
	Dev       bool // 开发模式:允许任意 Origin
}

type session struct {
	user string
	exp  time.Time
}

// Server HTTP 服务
type Server struct {
	cfg    Config
	hub    *hub
	mu     sync.Mutex
	sess   map[string]session
}

func New(cfg Config) *Server {
	if cfg.Secret == "" {
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		cfg.Secret = hex.EncodeToString(b)
		log.Printf("警告: 未设置会话密钥 RC_SECRET,已随机生成(服务重启后所有会话失效)")
	}
	if cfg.AdminUser == "" {
		cfg.AdminUser = "admin"
	}
	if cfg.AdminPass == "" {
		cfg.AdminPass = "admin123"
		log.Printf("警告: 使用默认管理员密码 admin123,请立即通过 RC_ADMIN_PASS 修改!")
	}
	if cfg.AgentToken == "" {
		cfg.AgentToken = "rc-agent-token"
		log.Printf("警告: 使用默认 agent 令牌 %q,请通过 RC_AGENT_TOKEN 修改!", cfg.AgentToken)
	}
	return &Server{cfg: cfg, hub: newHub(), sess: map[string]session{}}
}

// Run 启动 HTTP 服务
func (s *Server) Run() error {
	mux := http.NewServeMux()

	// 静态资源与页面
	var static fs.FS = mustSubFS(webFS, "webroot")
	if s.cfg.WebDir != "" {
		static = os.DirFS(s.cfg.WebDir)
	}
	mux.Handle("/", http.FileServer(http.FS(static)))

	// API
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.withAuth(s.handleLogout))
	mux.HandleFunc("/api/me", s.withAuth(s.handleMe))
	mux.HandleFunc("/api/agents", s.withAuth(s.handleAgents))

	// WebSocket
	mux.HandleFunc("/ws/agent", s.handleAgentWS)
	mux.HandleFunc("/ws/console", s.withAuthWS(s.handleConsoleWS))

	addr := s.cfg.Listen
	log.Printf("rc-server 控制台就绪: http%s://%s  (admin=%s 默认入口见日志)", tlsScheme(s.cfg), addr, s.cfg.AdminUser)
	if s.cfg.TLS {
		return http.ListenAndServeTLS(addr, s.cfg.CertFile, s.cfg.KeyFile, mux)
	}
	return http.ListenAndServe(addr, mux)
}

func tlsScheme(c Config) string {
	if c.TLS {
		return "s"
	}
	return ""
}

// ---------- 鉴权 ----------

func (s *Server) login(username, password string) bool {
	uOK := subtle.ConstantTimeCompare([]byte(username), []byte(s.cfg.AdminUser)) == 1
	pOK := subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.AdminPass)) == 1
	return uOK && pOK
}

func (s *Server) newSession(user string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	raw := hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw + ":" + s.cfg.Secret))
	tok := hex.EncodeToString(sum[:])
	s.mu.Lock()
	s.sess[tok] = session{user: user, exp: time.Now().Add(12 * time.Hour)}
	s.mu.Unlock()
	return tok
}

func (s *Server) checkSession(tok string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sess[tok]
	if !ok {
		return "", false
	}
	if time.Now().After(sess.exp) {
		delete(s.sess, tok)
		return "", false
	}
	return sess.user, true
}

func (s *Server) deleteSession(tok string) {
	s.mu.Lock()
	delete(s.sess, tok)
	s.mu.Unlock()
}

const cookieName = "rc_session"

func readCookie(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) setSessionCookie(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   12 * 3600,
	})
}

// withAuth REST 鉴权中间件
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.checkSession(readCookie(r)); !ok {
			http.Error(w, `{"error":"未登录"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// withAuthWS WebSocket 鉴权中间件(先鉴权再升级)
func (s *Server) withAuthWS(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.checkSession(readCookie(r)); !ok {
			http.Error(w, `{"error":"未登录"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ---------- REST ----------

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req loginReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, `{"error":"参数错误"}`, 400)
		return
	}
	if !s.login(strings.TrimSpace(req.Username), req.Password) {
		http.Error(w, `{"error":"用户名或密码错误"}`, http.StatusUnauthorized)
		return
	}
	tok := s.newSession(req.Username)
	s.setSessionCookie(w, tok)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true,"user":"` + req.Username + `"}`))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.deleteSession(readCookie(r))
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := s.checkSession(readCookie(r))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"user":"` + user + `"}`))
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.hub.snapshot())
}

// ---------- WebSocket 接入 ----------

// handleAgentWS agent 反向连接入口
func (s *Server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	// 令牌校验(Bearer)
	auth := r.Header.Get("Authorization")
	want := "Bearer " + s.cfg.AgentToken
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(auth)), []byte(want)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.upgradeAgent(w, r)
}

func (s *Server) upgradeAgent(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // agent 端不设 Origin,由令牌保证
	})
	if err != nil {
		log.Printf("[ws] agent 升级失败: %v", err)
		return
	}
	c.SetReadLimit(1 << 24)
	s.hub.handleAgent(r.Context(), c)
}

// handleConsoleWS 浏览器控制台入口
func (s *Server) handleConsoleWS(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Dev {
		// 跨站防护:Origin 必须同源(缺失 Origin 的本地工具放行)
		if origin := r.Header.Get("Origin"); origin != "" {
			if !sameOrigin(origin, r.Host) {
				http.Error(w, "origin rejected", http.StatusForbidden)
				return
			}
		}
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(1 << 22)
	s.hub.handleConsole(r.Context(), c)
}

func sameOrigin(origin, host string) bool {
	origin = strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	origin = strings.TrimSuffix(origin, "/")
	return origin == host
}
