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

/* 主机显示名(与二次确认输入比较) */
function hostLabel(a) { return (a && (a.name || a.hostname)) || "未知主机"; }

/* ---------------- 操作/确认弹层 ---------------- */
function confirmDialog(opts) {
  return new Promise((resolve) => {
    const modal = $("#modal");
    const input = $("#modalInput");
    const ok = $("#modalOk");
    const cancel = $("#modalCancel");
    cancel.classList.remove("hidden"); // 确认模式下显示"取消"
    $("#modalTitle").textContent = opts.title || "确认";
    const body = $("#modalBody");
    body.innerHTML = "";
    if (opts.body) body.appendChild(opts.body);
    input.classList.toggle("hidden", !opts.requireType);
    input.value = "";
    ok.textContent = opts.okText || "确认";
    ok.classList.toggle("primary", !opts.danger);
    ok.classList.toggle("danger", !!opts.danger);
    const check = () => { ok.disabled = opts.requireType ? input.value.trim() !== opts.typeTarget : false; };
    check();
    input.oninput = check;
    if (opts.requireType) setTimeout(() => input.focus(), 40);
    let done = false;
    const close = (v) => { if (done) return; done = true; modal.classList.add("hidden"); resolve(v); };
    ok.onclick = () => { if (opts.requireType && input.value.trim() !== opts.typeTarget) return; close(true); };
    cancel.onclick = () => close(false);
    const onKey = (ev) => {
      if (ev.key === "Escape") { close(false); document.removeEventListener("keydown", onKey); }
      if (ev.key === "Enter" && !ok.disabled) { close(true); document.removeEventListener("keydown", onKey); }
    };
    document.addEventListener("keydown", onKey);
    modal.classList.remove("hidden");
  });
}

function listDialog(title, items) {
  // items: [{ key, icon, label, desc, danger }]  返回选中的 key 或 null
  return new Promise((resolve) => {
    const modal = $("#modal");
    const input = $("#modalInput");
    $("#modalTitle").textContent = title;
    const body = $("#modalBody");
    body.innerHTML = "";
    const list = document.createElement("div");
    list.className = "op-list";
    for (const it of items) {
      const row = document.createElement("button");
      row.className = "op-row" + (it.danger ? " danger" : "");
      const ico = document.createElement("span");
      ico.className = "op-ico";
      ico.textContent = it.icon;
      const txt = document.createElement("span");
      const t = document.createElement("span");
      t.className = "op-t";
      t.textContent = it.label;
      const d = document.createElement("span");
      d.className = "op-d";
      d.textContent = it.desc || "";
      txt.appendChild(t);
      txt.appendChild(d);
      row.appendChild(ico);
      row.appendChild(txt);
      row.addEventListener("click", () => close(it.key));
      list.appendChild(row);
    }
    body.appendChild(list);
    input.classList.add("hidden");
    input.value = "";
    const ok = $("#modalOk");
    ok.textContent = "取消";
    ok.classList.add("primary");
    ok.classList.remove("danger");
    ok.disabled = false;
    $("#modalCancel").classList.add("hidden"); // 列表模式只保留右侧一个"取消"
    let done = false;
    const close = (v) => { if (done) return; done = true; modal.classList.add("hidden"); resolve(v); };
    ok.onclick = () => close(null);
    const cancelBtn = $("#modalCancel");
    cancelBtn.onclick = () => close(null);
    const onKey = (ev) => { if (ev.key === "Escape") { close(null); document.removeEventListener("keydown", onKey); } };
    document.addEventListener("keydown", onKey);
    modal.classList.remove("hidden");
  });
}

/* 主机控制动作元信息 */
const CTL = {
  reboot:        { label: "重启主机",     ico: "⏻", danger: true },
  shutdown:      { label: "关机",         ico: "⏼", danger: true },
  restart_agent: { label: "重启 Agent",   ico: "↻", danger: false },
  uninstall:     { label: "卸载 Agent",   ico: "⌫", danger: true },
};
function ctlSend(a, action) {
  if (!wsSend({ type: "host_ctl", agent_id: a.id, data: { action } })) {
    toast("控制通道未连接,无法下发指令", true);
    return;
  }
  const meta = CTL[action] || { label: action };
  toast(`已向「${hostLabel(a)}」下发:${meta.label}`, false, 2600);
}

async function openHostOps(a) {
  if (!a.online) { toast(`主机「${hostLabel(a)}」当前离线`, true); return; }
  const items = [
    { key: "reboot", icon: CTL.reboot.ico, label: "重启主机", desc: "远程重启该 Linux 主机(reboot),数秒后失联", danger: true },
    { key: "shutdown", icon: CTL.shutdown.ico, label: "关机", desc: "远程关机(poweroff),将无法再远程操作", danger: true },
    { key: "restart_agent", icon: CTL.restart_agent.ico, label: "重启 Agent", desc: "仅重启本机 rc-agent 进程,不影响主机" },
    { key: "uninstall", icon: CTL.uninstall.ico, label: "卸载 Agent", desc: "彻底删除 agent:停止服务、删除二进制/配置/ID/单元,不再受控", danger: true },
  ];
  const sel = await listDialog(`${hostLabel(a)} · 主机操作`, items);
  if (!sel) return;

  if (sel === "uninstall") {
    const confirmBody = document.createElement("div");
    const warn = document.createElement("div");
    warn.className = "warn-box";
    const b = document.createElement("b");
    b.textContent = "危险操作";
    warn.appendChild(b);
    warn.appendChild(document.createTextNode(":卸载后该主机将从控制台失去 agent,需重新人工安装才能恢复控制。卸载会清除 rc-agent 二进制、/etc/rc-agent、/var/lib/rc-agent、systemd 单元并停止自启。"));
    const p = document.createElement("p");
    p.className = "dim";
    p.style.marginTop = "4px";
    p.textContent = `请输入主机名「${hostLabel(a)}」以确认卸载:`;
    confirmBody.appendChild(warn);
    confirmBody.appendChild(p);
    const go = await confirmDialog({ title: `卸载 Agent · ${hostLabel(a)}`, body: confirmBody, danger: true, okText: "确认卸载", requireType: true, typeTarget: hostLabel(a) });
    if (go) ctlSend(a, "uninstall");
    return;
  }
  if (sel === "reboot") {
    const confirmBody = document.createElement("div");
    const warn = document.createElement("div");
    warn.className = "warn-box";
    warn.appendChild(document.createTextNode("该主机的所有运行中服务将随重启中断(数据库等请先手动落盘)。"));
    const p = document.createElement("p");
    p.className = "dim";
    p.textContent = `请输入主机名「${hostLabel(a)}」以确认重启:`;
    confirmBody.appendChild(warn);
    confirmBody.appendChild(p);
    const go = await confirmDialog({ title: `重启主机 · ${hostLabel(a)}`, body: confirmBody, danger: true, okText: "确认重启", requireType: true, typeTarget: hostLabel(a) });
    if (go) ctlSend(a, "reboot");
    return;
  }
  if (sel === "shutdown") {
    const confirmBody = document.createElement("div");
    const warn = document.createElement("div");
    warn.className = "warn-box";
    warn.appendChild(document.createTextNode("关机后需人工上电才能恢复,请确认业务已停止。"));
    confirmBody.appendChild(warn);
    const go = await confirmDialog({ title: `关机 · ${hostLabel(a)}`, body: confirmBody, danger: true, okText: "确认关机" });
    if (go) ctlSend(a, "shutdown");
    return;
  }
  if (sel === "restart_agent") {
    const go = await confirmDialog({ title: `重启 Agent · ${hostLabel(a)}`, body: (() => { const p = document.createElement("p"); p.className = "dim"; p.textContent = "仅重启 rc-agent(systemd 托管时约几秒内自动恢复,期间该主机短暂离线)。"; return p; })(), okText: "确认重启" });
    if (go) ctlSend(a, "restart_agent");
  }
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
  document.documentElement.classList.remove("boot"); // 登录态已判定
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
      <button class="op-btn" title="主机操作(重启/关机/重启Agent/卸载)">操作</button>
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
  const opBtn = el.querySelector(".op-btn");
  if (a.online) {
    opBtn.addEventListener("click", (ev) => { ev.stopPropagation(); openHostOps(a); });
  } else {
    opBtn.disabled = true;
    opBtn.style.opacity = ".35";
    opBtn.style.cursor = "not-allowed";
  }
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
  if (isMobileNow()) setView("term");
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
  refreshMNav();
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
  refreshMNav();
  if (isMobileNow() && !S.sessions.size) setView("list"); // 手机端最后一个会话关闭回到主机列表
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

/* ============ 移动端两屏(主机列表 / 终端) ============ */
const mqMobile = window.matchMedia("(max-width: 768px)");
const isMobileNow = () => mqMobile.matches;

function applyMobile() {
  const m = isMobileNow();
  const html = document.documentElement;
  html.classList.toggle("is-mobile", m);
  $("#mobileNav").classList.toggle("hidden", !m); // 手机模式才显示底部导航
  if (!m) {
    html.dataset.v = "";
  } else if (!html.dataset.v) {
    html.dataset.v = "list"; // 进入手机模式默认主机列表
  }
  refreshMNav();
  if (m && html.dataset.v === "term") scheduleFit();
}
function setView(v) {
  document.documentElement.dataset.v = v;
  refreshMNav();
  if (v === "term") scheduleFit();
}
function activeSession() {
  for (const s of S.sessions.values()) {
    if (s.pane.classList.contains("active")) return s;
  }
  const arr = [...S.sessions.values()];
  return arr.length ? arr[arr.length - 1] : null;
}
function scheduleFit() {
  setTimeout(() => {
    const s = activeSession();
    if (s && s.fit) { try { s.fit.fit(); } catch (e) {} }
  }, 90);
}
function refreshMNav() {
  const cnt = S.sessions.size;
  const v = document.documentElement.dataset.v;
  const term = $("#mNavTerm");
  term.classList.toggle("active", v === "term");
  $("#mNavHosts").classList.toggle("active", v !== "term");
  const cntEl = $("#mNavTermCnt");
  if (cnt > 0) { cntEl.textContent = cnt; cntEl.classList.remove("hidden"); } else cntEl.classList.add("hidden");
}
function bindMobileUI() {
  mqMobile.addEventListener("change", applyMobile);
  $("#backToHosts").addEventListener("click", () => setView("list"));
  $("#mNavHosts").addEventListener("click", () => setView("list"));
  $("#mNavTerm").addEventListener("click", () => {
    if (!S.sessions.size) return;
    const s = activeSession();
    if (s) activateTab(s.termID);
    setView("term");
  });
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
    case "host_ctl_result": {
      const r = msg.data || {};
      const agent = S.agents.find((x) => x.id === msg.agent_id);
      const who = hostLabel(agent) || msg.agent_id || "主机";
      const label = (CTL[r.action] || { label: r.action || "操作" }).label;
      if (r.ok) toast(`${who} · ${label}:${r.msg || "已受理"}`, false, 3600);
      else toast(`${who} · ${label}失败:${r.msg || "未知原因"}`, true, 5000);
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
  bindMobileUI();
  applyMobile(); // 登录遮罩下先按视口设好模式,登录后即生效
  try {
    const j = await api("/api/me");
    if (j.user) afterLogin(j.user);
  } catch (e) {
    // 未登录:显示登录框
  }
  document.documentElement.classList.remove("boot"); // 已判定:未登录则露出登录框
}
boot();
