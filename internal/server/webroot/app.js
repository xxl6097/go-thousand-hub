/* ============ 千机台 Remote Console 前端逻辑 ============ */
"use strict";

const $ = (s) => document.querySelector(s);
const enc = new TextEncoder();
const utf8dec = new TextDecoder("utf-8");

/* ---------------- 基础工具 ---------------- */
function toB64(str) {
  const bytes = enc.encode(str);
  let bin = "";
  const CH = 0x8000;
  for (let i = 0; i < bytes.length; i += CH) {
    bin += String.fromCharCode.apply(null, bytes.subarray(i, i + CH));
  }
  return btoa(bin);
}
function fromB64(b64, decoder) {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  // streaming 解码:跨包的多字节 UTF-8 不丢字符
  return decoder ? decoder.decode(bytes, { stream: true }) : utf8dec.decode(bytes);
}
function uuid() {
  if (crypto && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID.call(crypto);
  }
  return "xxxxxxxxxxxxxxxx".replace(/x/g, () => (Math.random() * 16 | 0).toString(16));
}
function fmtBytes(v) {
  if (!v) return "—";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return (i === 0 ? v : v.toFixed(1)) + " " + u[i];
}
function fmtUptime(sec) {
  if (!sec) return "—";
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
  return (d ? d + "天" : "") + (h ? h + "时" : "") + m + "分";
}
function fmtTime(unix) { return unix ? new Date(unix * 1000).toLocaleString() : "—"; }
function toast(msg, isErr, ms) {
  const el = document.createElement("div");
  el.className = "toast" + (isErr ? " err" : "");
  el.textContent = msg;
  $("#toastWrap").appendChild(el);
  setTimeout(() => el.remove(), ms || 3200);
}
async function api(path, opts) {
  const r = await fetch(path, Object.assign({ headers: { "Content-Type": "application/json" } }, opts || {}));
  if (r.status === 401) { onAuthLost(); throw new Error("未登录"); }
  return r.json();
}

/* ---------------- 全局状态 ---------------- */
const S = {
  agents: [],        // 主机列表快照
  filterText: "",
  filter: "all",
  sessions: new Map(), // termID -> session
  ws: null,
  wsRetry: 0,
  logged: false,
  user: "",
  termSeq: 0,
};

/* ---------------- 登录 ---------------- */
async function doLogin(user, pass) {
  const r = await fetch("/api/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: user, password: pass }),
  });
  if (!r.ok) {
    let msg = "用户名或密码错误";
    try { msg = (await r.json()).error || msg; } catch (e) {}
    throw new Error(msg);
  }
  return r.json();
}
function initLogin() {
  $("#loginForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    $("#loginErr").textContent = "";
    try {
      const j = await doLogin($("#loginUser").value.trim(), $("#loginPass").value);
      afterLogin(j.user);
    } catch (err) {
      $("#loginErr").textContent = err.message;
    }
  });
  $("#loginPass").addEventListener("keydown", (e) => { if (e.key === "Enter") $("#loginForm").requestSubmit(); });
}
function afterLogin(user) {
  S.logged = true; S.user = user;
  $("#loginOverlay").classList.add("hidden");
  $("#main").classList.remove("hidden");
  $("#whoAmI").textContent = user;
  connectConsole();
  refreshAgents();
  setInterval(refreshAgents, 3000);
}
function onAuthLost() {
  if (!S.logged) return;
  S.logged = false;
  closeConsole();
  closeAllSessions("登录已失效");
  $("#main").classList.add("hidden");
  $("#loginOverlay").classList.remove("hidden");
  toast("登录已失效,请重新登录", true);
}
$("#btnLogout").addEventListener("click", async () => {
  try { await api("/api/logout", { method: "POST" }); } catch (e) {}
  onAuthLost();
});

/* ---------------- 主机列表 ---------------- */
async function refreshAgents() {
  try {
    const list = await api("/api/agents");
    S.agents = list;
    renderAgents();
    // 主机掉线时关掉它的会话
    for (const [id, s] of S.sessions) {
      const a = list.find((x) => x.id === s.agentId);
      if (a && !a.online && !s.notifiedDown) {
        s.notifiedDown = true;
        s.dead = true;
        termWrite(s, "\r\n\x1b[31m[主机已离线,会话自动结束]\x1b[0m\r\n");
        markTab(s, "dead");
      }
    }
  } catch (e) { /* 401 已处理 */ }
}
function renderAgents() {
  const kw = S.filterText.trim().toLowerCase();
  let list = S.agents.filter((a) => {
    if (S.filter === "online" && !a.online) return false;
    if (S.filter === "offline" && a.online) return false;
    if (!kw) return true;
    const hay = [a.name, a.hostname, a.ip, a.os, a.arch, a.kernel].join(" ").toLowerCase();
    return hay.includes(kw);
  });
  list.sort((a, b) => (b.online - a.online) || a.name.localeCompare(b.name, "zh"));
  const online = S.agents.filter((a) => a.online).length;
  $("#brandStats").innerHTML = `<b>${online}</b> 在线 / <b>${S.agents.length - online}</b> 离线`;
  $("#agentEmpty").classList.toggle("hidden", list.length > 0);
  const host = $("#agentList");
  host.innerHTML = "";
  for (const a of list) host.appendChild(agentItem(a));
}
function agentItem(a) {
  const el = document.createElement("div");
  el.className = "agent-item " + (a.online ? "online" : "offline");
  el.innerHTML = `
    <span class="dot"></span>
    <div class="a-head">
      <div class="a-name"></div>
      <span class="a-badge">${a.online ? (a.os || "") + " · 在线" : "离线"}</span>
    </div>
    <div class="a-meta"></div>
    <div class="a-shell"></div>
    <div class="meters"></div>
    <div class="a-id"></div>`;
  el.querySelector(".a-name").textContent = a.name || a.hostname;
  const meta = `${a.hostname || "?"}  ${a.ip ? "· " + a.ip : ""}`;
  el.querySelector(".a-meta").textContent = meta;
  el.querySelector(".a-shell").textContent = `${a.os || ""} ${a.arch || ""}${a.kernel ? " · " + a.kernel : ""}  shell=${a.shell || "-"}`;
  el.querySelector(".a-id").textContent = "id " + a.id;
  if (a.online) {
    const m = a.metrics;
    const meterHost = el.querySelector(".meters");
    if (m) {
      meterHost.appendChild(meterBar("CPU", m.cpu_percent, 1));
      const memPct = m.mem_total ? (m.mem_used / m.mem_total) * 100 : 0;
      meterHost.appendChild(meterBar("内存", memPct, 1, `${fmtBytes(m.mem_used)} / ${fmtBytes(m.mem_total)}`));
      const diskPct = m.disk_total ? (m.disk_used / m.disk_total) * 100 : 0;
      meterHost.appendChild(meterBar("磁盘", diskPct, 1, `${fmtBytes(m.disk_used)} / ${fmtBytes(m.disk_total)}`));
    }
    const tip = document.createElement("div");
    tip.className = "a-shell";
    const load = m ? `load ${m.load1.toFixed ? m.load1.toFixed(2) : "—"} · 运行 ${fmtUptime(m.uptime_sec)} · agent v${a.version || ""}` : `等待指标… · agent v${a.version || ""}`;
    tip.textContent = load;
    el.appendChild(tip);
  }
  el.title = `主机:${a.name || a.hostname}  最后心跳:${fmtTime(a.last_seen)}  agent:${a.version || "-"}\n点击打开远程终端`;
  el.addEventListener("click", () => openSession(a));
  return el;
}
function meterBar(label, pct, max, hint) {
  const p = Math.max(0, Math.min(100, (pct / max) * 100));
  const wrap = document.createElement("div");
  wrap.className = "meter" + (p > 85 ? " crit" : p > 65 ? " hot" : "");
  wrap.innerHTML = `<div>${label} ${pct.toFixed ? pct.toFixed(1) + "%" : "—"}${hint ? " · " + hint : ""}</div><div class="bar"><i style="width:${p.toFixed(1)}%"></i></div>`;
  return wrap;
}

/* ---------------- 控制通道(浏览器 -> 服务端) ---------------- */
function wsURL() {
  const proto = location.protocol === "https:" ? "wss://" : "ws://";
  return proto + location.host + "/ws/console";
}
function connectConsole() {
  closeConsole();
  S.wsRetry = 0;
  try { S.ws = new WebSocket(wsURL()); } catch (e) { scheduleReconnect(); return; }
  const ws = S.ws;
  ws.onopen = () => { S.wsRetry = 0; $("#btnReconnect").classList.add("hidden"); };
  ws.onmessage = (ev) => handleServerMsg(JSON.parse(ev.data));
  ws.onclose = () => {
    if (S.logged) {
      $("#btnReconnect").classList.remove("hidden");
      scheduleReconnect();
    }
  };
  ws.onerror = () => ws.close();
}
function scheduleReconnect() {
  S.wsRetry++;
  const delay = Math.min(1000 * Math.pow(2, S.wsRetry), 10000);
  setTimeout(() => { if (S.logged && (!S.ws || S.ws.readyState > 1)) connectConsole(); }, delay);
}
function closeConsole() { if (S.ws) { try { S.ws.onclose = null; S.ws.close(); } catch (e) {} S.ws = null; } }
$("#btnReconnect").addEventListener("click", () => { connectConsole(); });

function wsSend(obj) {
  if (!S.ws || S.ws.readyState !== WebSocket.OPEN) return false;
  S.ws.send(JSON.stringify(obj));
  return true;
}

/* ---------------- 终端会话 ---------------- */
function openSession(agent) {
  if (!agent.online) { toast(`主机「${agent.name || agent.hostname}」当前离线`, true); return; }
  if (!S.logged) return;
  const termID = uuid();
  const title = `${agent.name || agent.hostname}`;
  const s = {
    termID,
    agentId: agent.id,
    agentName: agent.name || agent.hostname,
    title,
    no: ++S.termSeq,
    dead: false,
    notifiedDown: false,
    dec: null,
    term: null,
    fit: null,
  };
  // 建 DOM
  const pane = document.createElement("div");
  pane.className = "term-pane";
  pane.id = "pane-" + termID;
  $("#termHost").appendChild(pane);

  const tab = document.createElement("div");
  tab.className = "term-tab";
  tab.innerHTML = `<span class="tdot run"></span><span class="tname"></span><button class="x" title="关闭">×</button>`;
  tab.querySelector(".tname").textContent = `${title}`;
  tab.title = `${agent.name || agent.hostname} @ ${agent.ip || agent.hostname}`;
  tab.addEventListener("click", () => activateTab(termID));
  tab.querySelector(".x").addEventListener("click", (e) => { e.stopPropagation(); closeSession(s, true); });
  $("#tabbar").appendChild(tab);

  s.pane = pane; s.tab = tab;
  S.sessions.set(termID, s);

  // xterm
  const term = new Terminal({
    cursorBlink: true,
    fontSize: 13,
    fontFamily: '"SF Mono", Menlo, Consolas, "Courier New", monospace',
    theme: {
      background: "#0d1117", foreground: "#dbe2ec", cursor: "#60a5fa",
      selectionBackground: "rgba(59,130,246,.35)",
      black: "#161b22", red: "#f85149", green: "#3fb950", yellow: "#d29922",
      blue: "#58a6ff", magenta: "#bc8cff", cyan: "#39c5cf", white: "#dbe2ec",
      brightBlack: "#7d8590",
    },
    scrollback: 5000,
    allowProposedApi: true,
    accessibility: true,
  });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  s.dec = new TextDecoder("utf-8");
  term.open(pane);
  fit.fit();
  s.term = term; s.fit = fit;

  term.onData((data) => {
    if (s.dead) {
      if (data === "\r") reopenSession(s);
      return;
    }
    wsSend({ type: "input", data: { term_id: s.termID, data_b64: toB64(data) } });
  });
  term.onResize(({ cols, rows }) => {
    if (!s.dead) wsSend({ type: "resize", data: { term_id: s.termID, cols, rows } });
  });

  activateTab(termID);
  requestOpen(s);
}
function requestOpen(s) {
  const cols = s.term.cols, rows = s.term.rows;
  const ok = wsSend({ type: "open", data: { agent_id: s.agentId, term_id: s.termID, cols, rows, cmd: "" } });
  if (!ok) {
    termWrite(s, "\r\n\x1b[31m控制通道未连接…\x1b[0m\r\n");
    toast("控制通道未连接,正在重连…", true);
  }
}
function reopenSession(s) {
  if (S.sessions.get(s.termID) !== s) return;
  // 复用标签与终端对象,换新 termID 重新打开
  const oldID = s.termID;
  s.termID = uuid();
  S.sessions.delete(oldID);
  S.sessions.set(s.termID, s);
  s.dead = false; s.notifiedDown = false;
  s.dec = new TextDecoder("utf-8");
  const dot = s.tab.querySelector(".tdot");
  dot.className = "tdot run";
  s.term.clear();
  s.term.focus();
  requestOpen(s);
}
function activateTab(termID) {
  for (const [id, s] of S.sessions) {
    s.pane.classList.toggle("active", id === termID);
    s.tab.classList.toggle("active", id === termID);
    if (id === termID) {
      try { s.fit.fit(); s.term.focus(); } catch (e) {}
      if (!s.dead) {
        wsSend({ type: "resize", data: { term_id: s.termID, cols: s.term.cols, rows: s.term.rows } });
      }
    }
  }
  updatePlaceholder();
}
function closeSession(s, notifyServer) {
  if (notifyServer) wsSend({ type: "close", data: { term_id: s.termID, data_b64: "" } });
  S.sessions.delete(s.termID);
  try { s.term.dispose(); } catch (e) {}
  s.pane.remove();
  s.tab.remove();
  // 激活相邻标签
  const any = [...S.sessions.keys()];
  if (any.length) activateTab(any[any.length - 1]);
  updatePlaceholder();
}
function closeAllSessions(reason) {
  for (const [, s] of [...S.sessions]) {
    S.sessions.delete(s.termID);
    try { s.term.dispose(); } catch (e) {}
    s.pane.remove(); s.tab.remove();
  }
  updatePlaceholder();
  if (reason) toast(reason);
}
function updatePlaceholder() {
  $("#tabbar").querySelector(".tab-placeholder").style.display = S.sessions.size ? "none" : "block";
}
function termWrite(s, str) {
  try { s.term.write(str); } catch (e) {}
}
function markTab(s, kind) {
  const dot = s.tab.querySelector(".tdot");
  if (dot) dot.className = "tdot " + kind;
}

/* ---------------- 服务端消息 ---------------- */
function handleServerMsg(msg) {
  switch (msg.type) {
    case "output": {
      const s = S.sessions.get(msg.data.term_id || msg.term_id);
      if (!s) return;
      termWrite(s, fromB64(msg.data.data_b64, s.dec));
      break;
    }
    case "exit": {
      const s = S.sessions.get(msg.data.term_id || msg.term_id);
      if (!s) return;
      s.dead = true;
      const code = msg.data.code;
      termWrite(s, `\r\n\x1b[90m[会话已结束 code=${code}] 回车重连 · × 关闭\x1b[0m\r\n`);
      markTab(s, "dead");
      break;
    }
    case "error": {
      const tid = msg.data ? msg.data.term_id : "";
      if (tid) {
        const s = S.sessions.get(tid);
        if (s) {
          s.dead = true;
          termWrite(s, `\r\n\x1b[31m[${msg.data.msg || "会话错误"}]\x1b[0m\r\n`);
          markTab(s, "dead");
        }
      } else {
        toast(msg.data ? msg.data.msg : "通道错误", true);
      }
      break;
    }
  }
}

/* ---------------- 事件与启动 ---------------- */
function bindUI() {
  $("#filterText").addEventListener("input", (e) => { S.filterText = e.target.value; renderAgents(); });
  document.querySelectorAll(".filters .chip").forEach((b) =>
    b.addEventListener("click", () => {
      document.querySelectorAll(".filters .chip").forEach((x) => x.classList.remove("active"));
      b.classList.add("active");
      S.filter = b.dataset.f;
      renderAgents();
    })
  );
  let rsTimer = null;
  window.addEventListener("resize", () => {
    clearTimeout(rsTimer);
    rsTimer = setTimeout(() => {
      for (const s of S.sessions) if (s[1].pane.classList.contains("active")) { try { s[1].fit.fit(); } catch (e) {} }
    }, 120);
  });
}
async function boot() {
  initLogin();
  bindUI();
  try {
    const j = await api("/api/me");
    if (j.user) afterLogin(j.user);
  } catch (e) {
    // 未登录:显示登录框
  }
}
boot();
