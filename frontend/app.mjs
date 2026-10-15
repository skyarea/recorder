// The application shell: state from GET /api/state, every write back through
// the /api/* endpoints, and the tree/notes/reader UI built from it.
//
// The two pieces of real logic live in their own tested modules:
//   - ./lib/tree.mjs — grouping and natural ordering of the file list
//   - ./lib/md.mjs   — the note panel's Markdown
// Everything here is wiring and rendering.
import { Msg, VIEWER_SRC, listener, postToViewer } from "./lib/bridge.mjs";
import { buildTree, hasMatch, match } from "./lib/tree.mjs";
import { mdToHtml } from "./lib/md.mjs";

const STATUS = {
  todo:    { label: "未读", color: "#94a3b8", dot: "○" },
  reading: { label: "在读", color: "#3b82f6", dot: "◐" },
  done:    { label: "已读", color: "#22c55e", dot: "●" },
  skip:    { label: "跳过", color: "#d6d3d1", dot: "—" },
};
const STATUS_KEYS = ["todo", "reading", "done", "skip"];

// The annotation tools. All four ship: 划词 (#10), 画线 (#11), 框选 (#12)
// and 擦除 (#13), all reusing the same overlay and geometry.
const TOOLS = [
  { id: "highlight", label: "划词", hint: "选中文字后松开，得到高亮" },
  { id: "line", label: "画线", hint: "按住拖动，画自由曲线" },
  { id: "box", label: "框选", hint: "按住拖出一个矩形" },
  { id: "erase", label: "擦除", hint: "点一条标注删掉它" },
];

const $ = (sel) => document.querySelector(sel);

// --- state ------------------------------------------------------------------

// The last snapshot from the server, plus the view state that lives in the
// browser until the user changes it.
let state = null;
let query = "";
let editingNoteId = null;
let current = null; // root-relative path open in the reader
// The active annotation tool, or null when the overlay is idle. The viewer is
// told on every change, because it holds the layer that draws them.
let activeTool = null;
// Annotations by page number for the open document, as the API returns them.
let docAnnotations = {};

// Reader bookkeeping. The viewer only accepts a goto once its bridge has
// reported ready, so an early jump is remembered instead of dropped.
let docPages = 0;
let curPage = 1;
let viewerReady = false;
let wantedPage = 0;
// The reader frame is rebuilt by renderDetail, so it is looked up per use
// rather than captured once: a captured one would go stale the moment the
// detail pane redraws.
const frame = () => $("#viewer");

async function api(path, body) {
  const resp = await fetch(path, body === undefined ? undefined : {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data = await resp.json();
  if (!resp.ok) throw new Error(data.error || resp.statusText);
  return data;
}

function rec(path) {
  return (state.records && state.records[path]) ||
    { status: "todo", page: 0, totalPages: 0, starred: false, notes: [] };
}

// The paths the list should show: scanned files, in the tree's own order.
function visiblePaths() {
  if (!state) return [];
  return state.scanned.map((f) => f.path);
}

function matchFn(path) {
  return match(path, rec(path), state.ui.filter, query);
}

function toast(msg) {
  const el = $("#toast");
  el.textContent = msg;
  el.classList.add("show");
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => el.classList.remove("show"), 2200);
}

// --- theme ------------------------------------------------------------------

// The system preference is the default; the API returns "" until the user has
// chosen, and the choice then lives in the ui_theme setting.
function applyTheme() {
  const theme = (state && state.ui && state.ui.theme) || "";
  const root = document.documentElement;
  if (theme) {
    root.dataset.theme = theme;
  } else {
    delete root.dataset.theme;
  }
}

async function toggleTheme() {
  const current = (state.ui && state.ui.theme) ||
    (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
  const next = current === "dark" ? "light" : "dark";
  try {
    await api("/api/ui", { theme: next });
    await refresh();
    toast(next === "dark" ? "已切换到深色" : "已切换到浅色");
  } catch (e) {
    toast("切换主题失败：" + e.message);
  }
}

// --- tree -------------------------------------------------------------------

function renderTree() {
  const box = $("#tree");
  box.replaceChildren();

  if (!state.scanned.length) {
    const empty = document.createElement("div");
    empty.className = "empty";
    const n = state.orphanCount;
    empty.textContent = n
      ? `本地已有 ${n} 条记录不在当前目录中。`
      : "还没有载入资料。点击左上角「选择目录」。";
    box.append(empty);
    return;
  }

  const forceOpen = !!query || state.ui.filter !== "all";
  const tree = buildTree(visiblePaths());
  if (!hasMatch(tree, matchFn)) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "没有符合条件的文件";
    box.append(empty);
    return;
  }

  const frag = document.createDocumentFragment();
  renderNode(tree, frag, 0, forceOpen);
  box.appendChild(frag);
}

function renderNode(node, parentEl, depth, forceOpen) {
  for (const child of node.dirs.values()) {
    if (!hasMatch(child, matchFn)) continue;

    const collapsed = !forceOpen && state.ui.collapsed.includes(child.path);
    const row = document.createElement("div");
    row.className = "node dir";
    row.style.paddingLeft = 8 + depth * 13 + "px";
    row.dataset.path = child.path;
    row.append(tw(collapsed), folderIcon(), nameSpan(child.name));
    row.addEventListener("click", () => toggleCollapsed(child.path));
    parentEl.appendChild(row);

    if (!collapsed) renderNode(child, parentEl, depth + 1, forceOpen);
  }

  for (const f of node.files) {
    if (!matchFn(f.path)) continue;
    const r = rec(f.path);
    const st = STATUS[r.status] || STATUS.todo;
    const row = document.createElement("div");
    row.className = "node file" + (f.path === current ? " active" : "");
    row.style.paddingLeft = 8 + depth * 13 + 12 + "px";
    row.title = f.path;
    row.dataset.path = f.path;
    row.append(dot(st.color), nameSpan(f.name));
    if (r.starred) {
      const s = document.createElement("span");
      s.className = "star";
      s.textContent = "★";
      row.append(s);
    }
    row.addEventListener("click", () => selectFile(f.path));
    parentEl.appendChild(row);
  }
}

const dot = (color) => span("dot", "●", color);
const folderIcon = () => span("tw", "📁");
const tw = (collapsed) => span("tw", collapsed ? "▸" : "▾");

function span(cls, text, color) {
  const el = document.createElement("span");
  el.className = cls;
  el.textContent = text;
  if (color) el.style.color = color;
  return el;
}

function nameSpan(text) {
  const el = document.createElement("span");
  el.className = "nm";
  el.textContent = text;
  return el;
}

async function toggleCollapsed(path) {
  const list = state.ui.collapsed.slice();
  const i = list.indexOf(path);
  if (i >= 0) list.splice(i, 1); else list.push(path);
  await api("/api/ui", { collapsed: list });
  await refresh();
}

// --- stats ------------------------------------------------------------------

function renderStats() {
  const box = $("#stats");
  box.replaceChildren();
  $("#rootName").textContent = state.scanned.length ? (state.rootName || "") : "";

  if (!state.scanned.length) return;

  const c = { todo: 0, reading: 0, done: 0, skip: 0 };
  let starred = 0;
  for (const f of state.scanned) {
    const r = rec(f.path);
    c[r.status] = (c[r.status] || 0) + 1;
    if (r.starred) starred++;
  }
  const total = state.scanned.length;
  const pct = Math.round((c.done / total) * 100);

  const chip = (key, color, label, n) => {
    const b = document.createElement("button");
    b.className = "chip" + (state.ui.filter === key ? " on" : "");
    b.dataset.filter = key;
    if (color) {
      const i = document.createElement("i");
      i.style.background = color;
      b.append(i);
    }
    b.append(document.createTextNode(label + " "));
    const strong = document.createElement("b");
    strong.textContent = n;
    b.append(strong);
    b.addEventListener("click", () => setFilter(key));
    return b;
  };

  box.append(
    chip("all", null, "全部", total),
    chip("reading", STATUS.reading.color, "在读", c.reading),
    chip("done", STATUS.done.color, "已读", c.done),
    chip("todo", STATUS.todo.color, "未读", c.todo),
  );
  if (c.skip) box.append(chip("skip", STATUS.skip.color, "跳过", c.skip));
  if (starred) box.append(chip("star", "#f59e0b", "★", starred));

  const pctChip = document.createElement("span");
  pctChip.className = "chip";
  pctChip.style.cursor = "default";
  pctChip.textContent = "完成 " + pct + "%";
  box.append(pctChip);
}

async function setFilter(key) {
  await api("/api/ui", { filter: key });
  await refresh();
}

// --- notice (orphans) -------------------------------------------------------

function renderNotice() {
  const el = $("#notice");
  const n = state.orphanCount;
  if (!state.scanned.length || !n) {
    el.classList.add("hidden");
    el.replaceChildren();
    return;
  }
  el.classList.remove("hidden");
  el.replaceChildren();

  const text = document.createElement("span");
  text.innerHTML = `⚠ 有 <b>${n}</b> 条记录不在当前目录中（可能被移动或改名了），数据仍保留。`;
  el.append(text);

  const spacer = document.createElement("span");
  spacer.className = "spacer";
  el.append(spacer);

  const reveal = document.createElement("button");
  reveal.className = "btn sm link";
  reveal.textContent = "在资源管理器中显示";
  reveal.addEventListener("click", () => revealCurrent());
  el.append(reveal);

  const dismiss = document.createElement("button");
  dismiss.className = "btn sm link";
  dismiss.textContent = "忽略";
  dismiss.addEventListener("click", () => el.classList.add("hidden"));
  el.append(dismiss);
}

// --- annotations -----------------------------------------------------------

// Switching tools tells the viewer, which owns the layer that draws them. The
// same tool clicked again turns it off, so there is always a way back to plain
// reading.
function setTool(id) {
  activeTool = id || null;
  postToViewer(frame(), Msg.Tool, { tool: activeTool });
  for (const b of document.querySelectorAll('[data-act="tool"]')) {
    b.classList.toggle("on", b.dataset.v === activeTool);
  }
  // The pager is the one place a hint can live without covering the page.
  const pager = $("#pager");
  if (pager && activeTool) {
    const t = TOOLS.find((x) => x.id === activeTool);
    pager.textContent = t ? t.hint : "";
  } else {
    renderPager();
  }
}

// The viewer draws what the host sends, so every mutation is followed by a
// re-fetch and a re-send: the stored shape is the only source of truth.
async function loadAnnotations() {
  if (!current) {
    docAnnotations = {};
    return;
  }
  const res = await api("/api/annotations?path=" + encodeURIComponent(current));
  docAnnotations = {};
  for (const a of res.annotations || []) {
    (docAnnotations[a.page] ||= []).push({ id: a.id, kind: a.kind, data: JSON.parse(a.data) });
  }
  postToViewer(frame(), Msg.Annotations, { annotations: docAnnotations });
}

async function addAnnotation(page, kind, data) {
  try {
    await api("/api/annotations", { path: current, page, kind, data: JSON.stringify(data) });
    await loadAnnotations();
    await refresh();
  } catch (e) {
    toast("保存标注失败：" + e.message);
  }
}

async function deleteAnnotation(id) {
  try {
    await api("/api/annotations/delete", { id });
    await loadAnnotations();
    await refresh();
  } catch (e) {
    toast("删除标注失败：" + e.message);
  }
}

// --- reader -----------------------------------------------------------------

function renderPager() {
  const pager = $("#pager");
  if (!pager) return;
  pager.textContent = docPages ? `第 ${curPage} / ${docPages} 页` : "载入中…";
  const goto = $("#goto-page");
  if (goto) goto.max = docPages || "";
}

function resetReaderState() {
  docPages = 0;
  curPage = 1;
  viewerReady = false;
  wantedPage = 0;
}

// Each segment is encoded on its own so '/' keeps naming the path that
// /files/* will decode back into a root-relative document.
function filesURL(rel) {
  return "/files/" + rel.split("/").map(encodeURIComponent).join("/");
}

function openDoc(rel) {
  if (rel === current) return;
  current = rel;
  resetReaderState();
  activeTool = null;
  docAnnotations = {};
  markSelected();
  // The frame itself is created by renderDetail, which the caller runs next;
  // it navigates to the document there, so the first open and every later one
  // go through the same code path.
  // The overlay is redrawn from the API once the new document is ready, so
  // annotations never appear on the wrong file.
  loadAnnotations().catch(() => {});
}

function closeReader() {
  current = null;
  resetReaderState();
  // Drop the document, not only the pane: a hidden frame would keep the old
  // PDF and its state alive until the next open. Removing the attribute
  // leaves a live frame alone, so navigate it away instead.
  frame().src = "about:blank";
  markSelected();
  renderDetail();
}

function markSelected() {
  for (const row of $("#tree").querySelectorAll(".node.file")) {
    row.classList.toggle("active", row.dataset.path === current);
  }
}

function selectFile(rel) {
  openDoc(rel);
  renderDetail();
}

function gotoPage() {
  const input = $("#goto-page");
  const page = Number.parseInt(input.value, 10);
  if (!Number.isInteger(page) || page < 1) return;
  if (viewerReady) {
    postToViewer(frame(), Msg.Goto, { page });
  } else {
    wantedPage = page;
  }
}

window.addEventListener("message", listener(VIEWER_SRC, (m) => {
  if (m.type === Msg.Ready) {
    viewerReady = true;
    // The overlay is live from the moment it reports ready, so anything the
    // host already knows about is drawn before the user can interact.
    postToViewer(frame(), Msg.Annotations, { annotations: docAnnotations });
    postToViewer(frame(), Msg.Tool, { tool: activeTool });
    if (wantedPage) {
      postToViewer(frame(), Msg.Goto, { page: wantedPage });
      wantedPage = 0;
    }
    return;
  }
  // The bridge always says ready before it reports any page, so a message
  // from a frame that has not is the previous document still winding down.
  if (!viewerReady) return;
  if (m.type === Msg.Add) {
    addAnnotation(m.page, m.kind, m.data);
    return;
  }
  if (m.type === Msg.Delete) {
    deleteAnnotation(m.id);
    return;
  }
  if (m.type === Msg.Pages) {
    docPages = m.total || 0;
  } else if (m.type === Msg.Page) {
    curPage = m.page || 1;
  } else {
    return;
  }
  renderPager();
}));

async function revealCurrent() {
  if (!current) return;
  try {
    await api("/api/reveal", { path: current });
  } catch (e) {
    toast("无法打开资源管理器：" + e.message);
  }
}

// --- detail (reader bar + notes) --------------------------------------------

function renderDetail() {
  const el = $("#detail");
  el.replaceChildren();

  if (!current) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.style.margin = "auto";
    empty.textContent = state.scanned.length
      ? "从左侧选择一个 PDF 开始阅读"
      : "使用方法：\n1. 点击左上角「选择目录」\n2. 左侧列出所有 PDF，点击即可阅读\n3. 右下角是笔记面板\n4. 数据保存在本地，可随时导出备份";
    el.append(empty);
    return;
  }

  const r = rec(current);
  const hasPct = r.totalPages > 0 && r.page > 0;
  const pct = hasPct ? Math.round((r.page / r.totalPages) * 100) : 0;

  const top = document.createElement("div");
  top.className = "reader-top";
  top.innerHTML = `
    <div class="rt-row">
      <span class="d-path"></span>
      <button class="star-btn" data-act="star" title="标记重点 (S)">${r.starred ? "★" : "☆"}</button>
      <button class="btn sm" data-act="reveal" title="在资源管理器中显示">📂</button>
      <button class="btn sm" data-act="close">关闭</button>
    </div>
    <div class="rt-row">
      ${STATUS_KEYS.map((k, i) => `
        <button class="stbtn${r.status === k ? " on" : ""}" data-act="status" data-v="${k}"
                style="--c:${STATUS[k].color}">
          <span class="dot">${STATUS[k].dot}</span>${STATUS[k].label}<kbd>${i + 1}</kbd>
        </button>`).join("")}
    </div>
    <div class="rt-row">
      <span class="muted small">标注</span>
      ${TOOLS.map((t) => `
        <button class="tbtn${activeTool === t.id ? " on" : ""}" data-act="tool" data-v="${t.id}"
                title="${t.hint}">${t.label}</button>`).join("")}
    </div>
    <div class="rt-row">
      <label class="page-row">页码 <input type="number" min="0" id="page-input" value="${r.page || 0}"></label>
      <span class="muted small">/</span>
      <label class="page-row">总页数 <input type="number" min="0" id="total-input" value="${r.totalPages || 0}"></label>
      ${hasPct ? `<span class="pct">${pct}%</span><span class="mini-bar"><i style="width:${pct}%"></i></span>` : ""}
      <button class="btn sm" data-act="save-page">保存页码</button>
      <span class="spacer"></span>
      <span class="pager muted small" id="pager"></span>
      <input type="number" min="1" step="1" id="goto-page" class="page-row" aria-label="跳转页码"
             style="width:64px;padding:3px 7px;border:1px solid var(--border);border-radius:6px;background:var(--bg)">
      <button class="btn sm" id="goto-btn">跳转</button>
    </div>`;
  top.querySelector(".d-path").textContent = current;
  el.append(top);

  const wrap = document.createElement("div");
  wrap.id = "pdfWrap";
  // The iframe lives here rather than in index.html so that every open of a
  // document starts from a fresh frame: a reused one would keep the previous
  // document's state until it navigated.
  const viewerFrame = document.createElement("iframe");
  viewerFrame.id = "viewer";
  viewerFrame.title = "PDF 阅读区";
  // Navigating a fresh frame to the document is what stops thumbnails,
  // outline, find results and scroll position carrying over from the previous
  // file: a new URL is a whole new viewer document.
  viewerFrame.src = "/vendor/pdfjs/web/viewer.html?file=" +
    encodeURIComponent(filesURL(current));
  wrap.append(viewerFrame);
  el.append(wrap);

  const notes = document.createElement("div");
  notes.className = "notes-panel" + (state.ui.notesOpen ? " open" : "");
  notes.id = "notesPanel";
  notes.innerHTML = `
    <div class="notes-bar" data-act="toggle-notes">
      <span>${state.ui.notesOpen ? "▾" : "▸"}</span>
      <span>笔记</span>
      <span class="count">${r.notes.length}</span>
      <span class="spacer"></span>
      <span class="muted small">点击${state.ui.notesOpen ? "收起" : "展开"}</span>
    </div>
    <div class="notes-inner">
      <div class="notes-list" id="notes-list"></div>
      <div class="note-editor">
        <textarea id="note-text" placeholder="写点什么… 支持 **加粗**、\`代码\`、- 列表、# 标题。Ctrl / ⌘ + Enter 保存"></textarea>
        <div class="editor-foot">
          <label class="page-row">页码 <input type="number" min="0" id="note-page" placeholder="可选" value="${r.page || ""}"></label>
          <span class="muted small">Markdown 语法</span>
          <span class="spacer"></span>
          <button class="btn primary sm" data-act="add-note">${editingNoteId ? "保存修改" : "添加笔记"}</button>
          ${editingNoteId ? `<button class="btn sm" data-act="cancel-note">取消</button>` : ""}
        </div>
      </div>
    </div>`;
  el.append(notes);

  renderNotes();
  renderPager();
  wireDetail(top, notes);
}

function wireDetail(top, notes) {
  top.addEventListener("click", async (e) => {
    const btn = e.target.closest("[data-act]");
    if (!btn) return;
    const act = btn.dataset.act;
    if (act === "star") await setStar(current, !rec(current).starred);
    else if (act === "status") await setStatus(current, btn.dataset.v);
    else if (act === "tool") setTool(activeTool === btn.dataset.v ? null : btn.dataset.v);
    else if (act === "save-page") await savePage();
    else if (act === "reveal") await revealCurrent();
    else if (act === "close") closeReader();
  });
  $("#goto-btn").addEventListener("click", gotoPage);
  $("#goto-page").addEventListener("keydown", (e) => { if (e.key === "Enter") gotoPage(); });

  notes.addEventListener("click", async (e) => {
    const bar = e.target.closest('[data-act="toggle-notes"]');
    if (bar) {
      await api("/api/ui", { notesOpen: !state.ui.notesOpen });
      await refresh();
      return;
    }
    const btn = e.target.closest("[data-act]");
    if (!btn) return;
    const act = btn.dataset.act;
    if (act === "add-note") await submitNote();
    else if (act === "cancel-note") { editingNoteId = null; await refresh(); }
    else if (act === "edit-note") startEditNote(btn.dataset.id);
    else if (act === "delete-note") await deleteNote(btn.dataset.id);
  });
}

async function savePage() {
  const page = Number.parseInt($("#page-input").value, 10) || 0;
  const total = Number.parseInt($("#total-input").value, 10) || 0;
  try {
    if (total !== rec(current).totalPages) {
      await api("/api/totalpages", { path: current, totalPages: total });
    }
    await api("/api/progress", { path: current, page });
    await refresh();
  } catch (e) {
    toast("保存页码失败：" + e.message);
  }
}

async function setStatus(path, status) {
  try {
    await api("/api/status", { path, status });
    await refresh();
  } catch (e) {
    toast("设置状态失败：" + e.message);
  }
}

async function setStar(path, starred) {
  try {
    await api("/api/star", { path, starred });
    await refresh();
  } catch (e) {
    toast("设置星标失败：" + e.message);
  }
}

// --- notes ------------------------------------------------------------------

function renderNotes() {
  const box = $("#notes-list");
  if (!box || !current) return;
  const r = rec(current);
  box.replaceChildren();

  if (!r.notes.length) {
    const empty = document.createElement("div");
    empty.className = "muted small";
    empty.textContent = "还没有笔记";
    box.append(empty);
    return;
  }

  const sorted = r.notes.slice().sort((a, b) => (a.ts < b.ts ? -1 : a.ts > b.ts ? 1 : 0));
  for (const n of sorted) {
    const item = document.createElement("div");
    item.className = "note";
    item.dataset.id = n.id;

    const meta = document.createElement("div");
    meta.className = "note-meta";
    if (n.page !== null && n.page !== undefined) {
      const p = document.createElement("span");
      p.className = "note-page";
      p.textContent = "p." + n.page;
      meta.append(p);
    }
    const time = document.createElement("span");
    time.textContent = fmtTime(n.ts);
    meta.append(time);
    item.append(meta);

    if (n.id === editingNoteId) {
      const ta = document.createElement("textarea");
      ta.id = "note-text";
      ta.value = n.text;
      ta.style.minHeight = "64px";
      item.append(ta);
      const actions = document.createElement("div");
      actions.className = "note-actions";
      const save = document.createElement("button");
      save.className = "btn sm primary";
      save.dataset.act = "save-note";
      save.textContent = "保存";
      const cancel = document.createElement("button");
      cancel.className = "btn sm";
      cancel.dataset.act = "cancel-note";
      cancel.textContent = "取消";
      actions.append(save, cancel);
      item.append(actions);
    } else {
      const body = document.createElement("div");
      body.className = "note-body";
      body.innerHTML = mdToHtml(n.text);
      item.append(body);
      const actions = document.createElement("div");
      actions.className = "note-actions";
      const edit = document.createElement("button");
      edit.className = "btn sm link";
      edit.dataset.act = "edit-note";
      edit.dataset.id = n.id;
      edit.textContent = "编辑";
      const del = document.createElement("button");
      del.className = "btn sm link";
      del.dataset.act = "delete-note";
      del.dataset.id = n.id;
      del.textContent = "删除";
      actions.append(edit, del);
      item.append(actions);
    }
    box.append(item);
  }
}

function startEditNote(id) {
  editingNoteId = id;
  renderNotes();
  const ta = $("#note-list textarea, #notes-list textarea");
  if (ta) { ta.focus(); ta.setSelectionRange(ta.value.length, ta.value.length); }
}

async function submitNote() {
  const text = $("#note-text").value.trim();
  if (!text) return;
  const pageRaw = $("#note-page").value;
  const page = pageRaw === "" ? null : Number.parseInt(pageRaw, 10);
  try {
    if (editingNoteId) {
      await api("/api/notes/update", { id: editingNoteId, text });
      editingNoteId = null;
    } else {
      await api("/api/notes", { path: current, text, page });
    }
    await refresh();
    toast("笔记已保存");
  } catch (e) {
    toast("保存笔记失败：" + e.message);
  }
}

async function deleteNote(id) {
  if (!confirm("删除这条笔记？不可撤销。")) return;
  try {
    await api("/api/notes/delete", { id });
    if (editingNoteId === id) editingNoteId = null;
    await refresh();
  } catch (e) {
    toast("删除笔记失败：" + e.message);
  }
}

function fmtTime(iso) {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

// --- export / import --------------------------------------------------------

async function exportJSON() {
  try {
    await api("/api/export", {});
    toast("已导出到下载目录");
    await refresh();
  } catch (e) {
    toast("导出失败：" + e.message);
  }
}

async function importJSON(file) {
  if (!file) return;
  if (!confirm("导入会与现有数据合并（按更新时间取较新的一条）。继续？")) return;
  try {
    const text = await file.text();
    // The server rejects a bad document whole and leaves the store untouched,
    // so a failed import cannot cost the user their library.
    const res = await api("/api/import?" + new URLSearchParams({ replace: "false" }), text);
    toast(`已导入 ${res.imported} 条记录`);
    await refresh();
  } catch (e) {
    toast("导入失败：" + e.message);
  }
}

// --- shortcuts --------------------------------------------------------------

function installShortcuts() {
  document.addEventListener("keydown", (e) => {
    const t = e.target;
    const typing = t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable);

    if (!typing && e.key === "/") {
      e.preventDefault();
      $("#search").focus();
      return;
    }
    if (typing) return;
    if (e.ctrlKey || e.metaKey || e.altKey) return;

    const list = visiblePaths().filter(matchFn);
    if ((e.key === "j" || e.key === "J" || e.key === "ArrowDown") && list.length) {
      e.preventDefault();
      const i = list.indexOf(current);
      const next = list[Math.min(list.length - 1, i < 0 ? 0 : i + 1)];
      if (next) selectFile(next);
      return;
    }
    if ((e.key === "k" || e.key === "K" || e.key === "ArrowUp") && list.length) {
      e.preventDefault();
      const i = list.indexOf(current);
      const prev = list[Math.max(0, i < 0 ? 0 : i - 1)];
      if (prev) selectFile(prev);
      return;
    }
    if (!current) return;

    const idx = ["1", "2", "3", "4"].indexOf(e.key);
    if (idx >= 0) {
      e.preventDefault();
      setStatus(current, STATUS_KEYS[idx]);
      return;
    }
    if (e.key === "s" || e.key === "S") {
      e.preventDefault();
      setStar(current, !rec(current).starred);
    }
  });

  // Ctrl/⌘+Enter in the note box saves it, Escape cancels an edit.
  document.addEventListener("keydown", (e) => {
    if (e.target.id === "note-text" && (e.ctrlKey || e.metaKey) && e.key === "Enter") {
      e.preventDefault();
      submitNote();
    }
    if (e.target.tagName === "TEXTAREA" && e.key === "Escape" && editingNoteId) {
      e.preventDefault();
      editingNoteId = null;
      refresh();
    }
  });
}

// --- render -----------------------------------------------------------------

function renderAll() {
  applyTheme();
  renderStats();
  renderTree();
  renderNotice();
  renderDetail();
}

async function refresh() {
  state = await api("/api/state");
  renderAll();
}

// --- wiring -----------------------------------------------------------------

function install() {
  $("#pick").addEventListener("click", async () => {
    try {
      closeReader();
      await api("/api/root", {});
      await refresh();
    } catch (e) {
      alert(e.message);
    }
  });
  $("#search").addEventListener("input", (e) => {
    query = e.target.value.trim();
    renderTree();
  });
  $("#theme-btn").addEventListener("click", toggleTheme);
  $("#export-btn").addEventListener("click", exportJSON);
  $("#import-input").addEventListener("change", (e) => {
    importJSON(e.target.files[0]);
    e.target.value = "";
  });
  installShortcuts();

  // The seam later tasks hook onto: the current snapshot and a way to redraw.
  window.__recorder = {
    get state() { return state; },
    refresh,
  };
}

install();
refresh().catch((e) => {
  const el = $("#detail");
  el.replaceChildren();
  const empty = document.createElement("div");
  empty.className = "empty";
  empty.textContent = "本地服务未就绪：" + e.message;
  el.append(empty);
});
