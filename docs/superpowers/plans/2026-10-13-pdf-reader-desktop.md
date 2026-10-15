# PDF 阅读进度 · 笔记单机版 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把单文件 HTML 的 PDF 阅读进度/笔记应用演进为 Windows 双击即用的单机程序，新增"PDF 标注（划词/画线/框选）↔ 笔记"双向关联。

**Architecture:** Go 后端一个进程内起本地 HTTP 服务（嵌入前端静态资源 + `/api/*` JSON 接口 + `/files/*` PDF 流），Wails v3 开一个窗口加载该地址；前端是无框架的网页应用（由参考实现移植），PDF 阅读用 vendor 进来的 pdf.js 官方 viewer，标注层以 bridge 脚本注入 viewer；数据存 SQLite（`%APPDATA%/recorder/`）。借鉴 magpie（MIT）的"无绑定生成器、无打包器、embed + 自有 HTTP handler"模式。

**Tech Stack:** Go 1.26、Wails v3（`github.com/wailsapp/wails/v3`）、`modernc.org/sqlite`、pdf.js **6.4.299**（从 GitHub Release 的官方 dist zip vendored 进 `frontend/vendor/pdfjs/`）、原生 ES Modules + `node --test`。

**Spec:** `docs/superpowers/specs/2026-10-13-pdf-reader-desktop-design.md`

## Global Constraints

- 目标平台 Windows 10/11；产物为单文件 `dist/recorder.exe`，不做安装包、不做代码签名。
- 禁止 CGO / C 工具链：SQLite 必须用 `modernc.org/sqlite`。
- 界面文案沿用参考实现的中文（"选择资料文件夹""未读/在读/已读/跳过"等）。
- 不做 OCR、不做云同步、不做旧版 localStorage 数据迁移（导出/导入 JSON 仅作备份功能）。
- 单根目录：记住上次扫描目录并启动自动重扫；文件身份为"根目录相对路径、`/` 分隔"。
- 进度自动跟随滚动记录；页码输入框保留用于跳页。
- 阅读器基于 vendored 的 pdf.js 官方 viewer（`frontend/vendor/pdfjs/web/viewer.html`，**原样布局、不改第三方源码**），保留其查找/打印/缩略图/大纲/缩放。
  > 版本来源更正（#4）：官方 `viewer.html` 只存在于 GitHub Release 的 dist zip，npm 的 `pdfjs-dist` 任何版本（已核实 3.11 / 4.10 / 5.4 / 6.4）都没有它，只有 `PDFViewer` 组件库。所以 Task 9 的「`npm pack pdfjs-dist` 解出 `web/`」是**走不通的路**，已改为从 Release 取、钉 6.4.299。
- 导出 JSON 必须保持旧格式：`{version:1, rootName, files:{path:{status,page,totalPages,starred,notes:[{id,text,page,ts}],updatedAt}}}`。
- 每个任务结束必须 `git commit`；Go 测试命令 `go test ./...`，前端纯逻辑测试命令 `node --test tests/frontend/*.test.mjs`。
- 参考实现：`deepseek_html_20261013_7c2d5a.html`（下称"参考 HTML"）。参考工程：github.com/yetone/magpie（MIT）。

## Review Focus

规格里暗示、但没有哪个任务的测试直接覆盖，且最可能坑到真实使用者的五种情况：

1. **资料根目录被移动/拔盘后启动**：不崩溃、已保存的进度/笔记仍在、UI 提示重新选择目录。→ Task 4 `TestScanMissingRoot` + Task 7 `TestStateWithoutRoot`。
2. **恶意或意外的路径穿越**：`GET /files/../../secret` 与编码变体必须 404，不能读出根目录之外的文件。→ Task 6 `TestFilesRejectsTraversal`。
3. **大 PDF 的 HTTP Range 请求**：pdf.js 对大文件发 `Range` 头，必须返回 206 与正确切片，否则部分文档打不开。→ Task 6 `TestFilesRangeRequest`。
4. **页码边界**：`page > totalPages` 时按参考实现的规则钳制（进度页钳到总页数），百分比不超 100%。→ Task 2 `TestSetProgressClampsToTotalPages`。
5. **脏 JSON 导入**：非法/缺字段的导入必须整体拒绝且**不破坏现有数据**。→ Task 5 `TestImportRejectsInvalidWithoutTouchingStore`。

---

### Task 1: 项目骨架与基线提交

**Files:**
- Create: `go.mod`, `main.go`, `embed.go`, `internal/gui/app.go`
- Create: `frontend/index.html`（占位页）
- Test: 无独立测试文件；验收为 `go build` 成功 + 窗口手工打开

**Interfaces:**
- Produces: `main.go` 中 `main()` 启动 Wails v3 窗口，加载 `url`（Task 7 注入真实服务地址，本任务先用 `about:blank` 或占位页）。后续任务都往这个骨架里挂模块。

- [ ] **Step 1: 提交现有仓库基线**

```bash
git add AGENTS.md docs/ deepseek_html_20261013_7c2d5a.html
git commit -m "chore: baseline — agent docs, spec, reference HTML"
```

- [ ] **Step 2: `go mod init github.com/skyarea/recorder`，安装 Wails v3**

Run: `go get github.com/wailsapp/wails/v3@latest`
Expected: `go.mod` 出现 `wails/v3`（当前为 v3.0.0-beta 系，允许）。

- [ ] **Step 3: 写 `main.go` + `embed.go` + `internal/gui/app.go`**

按 magpie 的 `internal/gui/app.go` 与 `mainwindow.go` 模式：`application.New(...)` 创建 app、注册一个主窗口、加载 URL、`app.Run()`。`embed.go` 用 `//go:embed frontend` 嵌入占位页。窗口标题 `PDF 阅读进度 · 笔记`，尺寸约 1280×860。打开根目录对话框等 runtime API 的具体写法在实现时对照 magpie 源码确定。

- [ ] **Step 4: 构建验证**

Run: `go build -o recorder.exe .`
Expected: 生成 `recorder.exe`，无编译错误。

- [ ] **Step 5: 手工冒烟**

Run: `./recorder.exe`
Expected: 弹出窗口、显示占位页、正常关闭。把 `recorder.exe` 加入 `.gitignore`。

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat: wails v3 skeleton with embedded frontend"
```

---

### Task 2: 存储层 — files 与 settings（SQLite）

**Files:**
- Create: `internal/storage/store.go`, `internal/storage/models.go`
- Test: `internal/storage/store_test.go`

**Interfaces:**
- Consumes: 无。
- Produces（Task 3/5/7 依赖，签名必须一致）:

```go
type FileRec struct {
    Path, Status string // status ∈ todo|reading|done|skip
    Page, TotalPages int
    Starred bool
    UpdatedAt string // RFC3339
}
func Open(dbPath string) (*Store, error)      // 自动建表（幂等）
func (s *Store) Close() error
func (s *Store) File(path string) (FileRec, error)   // 无记录时返回 todo 默认值
func (s *Store) SetStatus(path, status string) error // 非法 status 返回 error
func (s *Store) SetStar(path string, starred bool) error
func (s *Store) SetProgress(path string, page int) error    // 见钳制规则
func (s *Store) SetTotalPages(path string, total int) error // 联动钳 page
func (s *Store) ListFiles() ([]FileRec, error)
func (s *Store) SetSetting(k, v string) error
func (s *Store) Setting(k string) (string, bool, error)
```

- [ ] **Step 1: 写失败测试（表结构 + CRUD + 钳制）**

```go
func TestFileDefaultsAndSetters(t *testing.T)     // File() 默认值；SetStatus/SetStar 生效
func TestSetProgressClampsToTotalPages(t *testing.T) // totalPages=10, page=15 → 10
func TestSetTotalPagesClampsPage(t *testing.T)    // page=8, SetTotalPages(5) → page=5
func TestSetStatusRejectsInvalid(t *testing.T)    // "bogus" → error, 数据不变
func TestSettingsRoundTrip(t *testing.T)
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/storage/ -v`
Expected: FAIL（包不存在或符号未定义）

- [ ] **Step 3: 实现 `storage`（modernc.org/sqlite，`file:` DSN；表 `files(path PK, status, page, total_pages, starred, updated_at)`、`settings(key PK, value)`）**

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/storage/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/ go.mod go.sum && git commit -m "feat(storage): files and settings tables"
```

---

### Task 3: 存储层 — notes、annotations 与 M:N 关联

**Files:**
- Modify: `internal/storage/models.go`, `internal/storage/store.go`
- Test: `internal/storage/store_test.go`

**Interfaces:**
- Consumes: Task 2 的 `Open/Store`。
- Produces:

```go
type Note struct { ID, FilePath, Text string; Page *int; TS string; AnnIDs []string }
type Annotation struct { ID, FilePath string; Page int; Kind, Data, CreatedAt string } // kind ∈ highlight|line|box; Data 为归一化 JSON
func (s *Store) AddNote(filePath, text string, page *int) (Note, error) // id/ts 后端生成
func (s *Store) UpdateNote(id, text string) error
func (s *Store) DeleteNote(id string) error          // 级联删其 links
func (s *Store) ListNotes(filePath string) ([]Note, error) // 按 ts 升序，附 AnnIDs
func (s *Store) AddAnnotation(filePath string, page int, kind, data string) (Annotation, error)
func (s *Store) DeleteAnnotation(id string) error    // 级联删其 links
func (s *Store) ListAnnotations(filePath string) ([]Annotation, error)
func (s *Store) LinkNote(noteID, annID string) error      // 幂等
func (s *Store) UnlinkNote(noteID, annID string) error
```

- [ ] **Step 1: 写失败测试**

```go
func TestNoteCRUD(t *testing.T)          // 增查改删；Page nil 与数值均可
func TestAnnotationCRUD(t *testing.T)    // 三种 kind
func TestLinksAreManyToManyAndCascade(t *testing.T) // 一笔记挂两标注、一标注被两笔记挂；DeleteNote 后 links 清空
func TestListNotesReturnsAnnIDs(t *testing.T)
```

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/storage/ -v` → FAIL

- [ ] **Step 3: 实现 `notes(id PK, file_path, text, page, ts)`、`annotations(id PK, file_path, page, kind, data, created_at)`、`note_annotations(note_id, annotation_id, PK(两者))` 及上述方法**

- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/storage/ -v` → PASS

- [ ] **Step 5: Commit** — `git add internal/storage && git commit -m "feat(storage): notes, annotations, M:N links"`

---

### Task 4: 目录扫描与页数嗅探（Go 移植）

**Files:**
- Create: `internal/files/scan.go`, `internal/files/sniff.go`
- Test: `internal/files/files_test.go`

**Interfaces:**
- Consumes: 无。
- Produces（Task 7 依赖）:

```go
type FileMeta struct{ Path, Name string }
func Scan(root string) ([]FileMeta, error) // 递归收集 *.pdf（大小写不敏感），Path 为相对路径、'/' 分隔，按自然序（数字感知、中文 locale 近似 zh）排序
func SniffPageCount(path string) (int, error) // 读头 2MB + 尾 1MB，正则 `/Count\s+(\d+)` 取 <200000 的最大值；失败返回 0
```

- [ ] **Step 1: 写失败测试**

```go
func TestScanCollectsPdfsWithSlashPaths(t *testing.T) // 建 temp 目录含子目录 a/b.PDF、x.txt → 只收 PDF、路径 `sub/a/b.PDF`
func TestScanSortsNaturally(t *testing.T)             // p1,p2,p10 → p1,p2,p10
func TestScanMissingRoot(t *testing.T)                // 不存在的目录 → 返回 error（不 panic）[Review Focus 1]
func TestSniffPageCount(t *testing.T)                 // 造含 `/Count 42` 的假 PDF 字节 → 42；空文件 → 0
```

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/files/ -v` → FAIL

- [ ] **Step 3: 实现**（自然序用 `golang.org/x/text` 或自写数字感知比较；嗅逻逻辑对照参考 HTML 的 `sniffPageCount`）

- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/files/ -v` → PASS

- [ ] **Step 5: Commit** — `git add internal/files && git commit -m "feat: directory scan and page-count sniff"`

---

### Task 5: JSON 导出/导入（兼容旧格式）

**Files:**
- Create: `internal/export/export.go`
- Test: `internal/export/export_test.go`

**Interfaces:**
- Consumes: Task 2/3 的 `*storage.Store`。
- Produces（Task 7 依赖）:

```go
func ExportJSON(s *storage.Store, rootName string) ([]byte, error) // 输出 Global Constraints 规定的旧格式
func ImportJSON(s *storage.Store, data []byte, replace bool) (imported int, err error) // replace=false 时按 updatedAt 较新者胜出合并
```

- [ ] **Step 1: 写失败测试**

```go
func TestExportMatchesLegacyShape(t *testing.T)   // 键名 version/rootName/files、notes 的 id/text/page/ts、updatedAt
func TestImportRoundTrip(t *testing.T)            // export→import→ListFiles/ListNotes 相等
func TestImportMergeNewerWins(t *testing.T)       // replace=false，较新 updatedAt 覆盖旧值，较旧保留
func TestImportRejectsInvalidWithoutTouchingStore(t *testing.T) // 非法 JSON、缺 files 字段 → error 且原数据逐条不变 [Review Focus 5]
```

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/export/ -v` → FAIL

- [ ] **Step 3: 实现**（格式逐字段对照参考 HTML 的 `exportJSON`/`importJSON`）

- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/export/ -v` → PASS

- [ ] **Step 5: Commit** — `git add internal/export && git commit -m "feat: legacy-compatible JSON export/import"`

---

### Task 6: `/files/*` PDF 文件服务（含防穿越与 Range）

**Files:**
- Create: `internal/httpfiles/server.go`
- Test: `internal/httpfiles/server_test.go`

**Interfaces:**
- Consumes: Task 4 的 `FileMeta.Path`（根内相对路径）。
- Produces（Task 7 依赖）:

```go
func NewHandler(getRoot func() string) http.Handler
// GET /files/<relpath>；root 为空或路径不在根内 → 404；http.ServeContent 提供 Range/ETag/Last-Modified
```

- [ ] **Step 1: 写失败测试**

```go
func TestServesFileUnderRoot(t *testing.T)      // 200，内容一致
func TestFilesRejectsTraversal(t *testing.T)    // /files/../../etc/passwd 及 %2e%2e 编码变体 → 404 [Review Focus 2]
func TestFilesMissingRootOrFile404(t *testing.T)// getRoot 返回 ""、文件不存在 → 404
func TestFilesRangeRequest(t *testing.T)        // Range: bytes=2-5 → 206 + 正确 4 字节 [Review Focus 3]
```

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/httpfiles/ -v` → FAIL

- [ ] **Step 3: 实现**（`filepath.Clean` + `filepath.Rel` 判定包含关系；`url.PathUnquote` 后再判；用 `http.ServeContent` 对接 `os.Open` 的 `io.ReadSeeker`）

- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/httpfiles/ -v` → PASS

- [ ] **Step 5: Commit** — `git add internal/httpfiles && git commit -m "feat: loopback PDF file server with range support"`

---

### Task 7: 应用服务层 — `/api/*` 路由与启动编排

**Files:**
- Create: `internal/app/server.go`, `internal/app/state.go`
- Modify: `main.go`（启动顺序：开 DB → 读 root → 重扫 → 起 `127.0.0.1:0` HTTP → 窗口加载该地址）
- Test: `internal/app/server_test.go`

**Interfaces:**
- Consumes: Task 2/3/4/5/6 的全部导出符号。
- Produces（前端 Task 8+ 依赖的 HTTP 契约）:

```
GET  /api/state -> {hasRoot,rootPath,rootName,scanned:[{path,name}],
    records:{path:{status,page,totalPages,starred,updatedAt,notes:[{id,text,page,ts,annIds}]}},
    ui:{collapsed,filter,notesOpen}, orphanCount}
POST /api/root          {path}        -> rescan, 返回 state；path 不存在 → 400
POST /api/status        {path,status}
POST /api/star          {path,starred}
POST /api/progress      {path,page}
POST /api/totalpages    {path,totalPages}
POST /api/notes         {path,text,page}        -> note
POST /api/notes/update  {id,text}
POST /api/notes/delete  {id}
GET  /api/annotations?path=<rel> -> [annotation]
POST /api/annotations        {path,page,kind,data} -> annotation
POST /api/annotations/delete {id}
POST /api/links         {noteId,annotationId}
POST /api/links/delete  {noteId,annotationId}
POST /api/ui            {collapsed,filter,notesOpen}
POST /api/export         -> {path,count}（Go 侧弹保存对话框；对话框不可用时落 `%APPDATA%\recorder\exports\阅读进度-YYYYMMDD.json` 并返回该路径）
POST /api/import?replace=true|false  body=JSON 文本 -> {imported}
GET  /files/<rel>       -> Task 6
GET  /*                 -> embed 的 frontend 静态资源（index.html 为回退）
```

- 失败一律 `{"error":"..."}` + 4xx/5xx。
- **Orphan 定义**：`records` 中不在 `scanned` 且有实质内容（状态非 todo / 有笔记 / page>0 / starred）的路径，`orphanCount` 为数量。
- 根目录设置存 settings 键 `root_path`；UI 状态存 `ui_*` 三个键（Task 8 消费）。

- [ ] **Step 1: 写失败测试（httptest，临时 DB + 临时资料目录）**

```go
func TestStateShapeAfterScan(t *testing.T)   // 扫描后 scanned 与 records/ui/hasRoot 字段齐全
func TestStateWithoutRoot(t *testing.T)      // 未设 root → hasRoot=false、scanned 空、不报错 [Review Focus 1]
func TestRootChangeRescans(t *testing.T)
func TestMutationsRoundTrip(t *testing.T)    // status/star/progress/notes/annotations/links/ui 逐个 POST 后 state 反映
func TestOrphanCount(t *testing.T)
func TestImportExportEndpoints(t *testing.T) // export 造文件→import(replace/merge) 返回 imported
func TestUnknownRoute404(t *testing.T)
```

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/app/ -v` → FAIL

- [ ] **Step 3: 实现 `Routes() http.Handler`（`http.ServeMux`）与 state 组装；`main.go` 编排启动，窗口 URL 换成真实地址；根目录对话框调用点留为 `chooseRoot()`，实现时对照 magpie**

- [ ] **Step 4: 运行确认通过** — Run: `go test ./... -v` → 全 PASS

- [ ] **Step 5: 手工冒烟** — Run: `./recorder.exe` → 选目录 → 打印 `/api/state` 响应肉眼核对

- [ ] **Step 6: Commit** — `git add -A && git commit -m "feat: app service layer and startup orchestration"`

---

### Task 8: 前端移植 — 布局/树/统计/详情/笔记面板

**Files:**
- Create: `frontend/index.html`, `frontend/app.css`, `frontend/app.js`, `frontend/lib/tree.js`, `frontend/lib/md.js`, `frontend/package.json`（`"type":"module"`）
- Test: `frontend/test/tree.test.js`, `frontend/test/md.test.js`

**Interfaces:**
- Consumes: Task 7 的 `/api/*` 契约。
- Produces: `lib/tree.js` 导出 `buildTree(paths)`, `hasMatch(node, matchFn)`, `match(path, rec, filter, query)`；`lib/md.js` 导出 `mdToHtml(src)` —— 两者从参考 HTML **原样移植为纯函数**（Task 11/13 复用 tree/match 过滤）。`app.js` 暴露 `window.__recorder = {state, refresh()}` 供后续任务挂接。

- [ ] **Step 1: 写失败测试（node --test）**

```js
test('buildTree nests paths and sorts naturally')   // ['a/b.pdf','a/c.pdf','d.pdf']
test('match filters by status, star and query')     // filter/star/大小写不敏感，命中 notes 文本
test('mdToHtml covers heading, list, bold, code, link, blockquote') // 逐条断言输出 HTML 片段
```

- [ ] **Step 2: 运行确认失败** — Run: `node --test frontend/test/` → FAIL

- [ ] **Step 3: 实现 lib 纯函数**（逐行对照参考 HTML 的 `buildTree/hasMatch/match/mdInline/mdToHtml`）

- [ ] **Step 4: 运行确认通过** — Run: `node --test frontend/test/` → PASS

- [ ] **Step 5: 实现 `index.html/app.css/app.js`**：从参考 HTML 拆出结构与样式；数据层把 `loadAll/save/getRec` 换成 fetch `/api/state` 与各 POST；功能全集：目录树、状态/星标（含 `1~4`/`S` 快捷键）、统计 chips、搜索（`/`）、`J/K` 切换、笔记 CRUD + Markdown + `Ctrl+Enter`、页码/总页数输入与跳页按钮、导出（`POST /api/export`）与导入（`<input type=file>` 读文本 POST）、孤儿提示 + 清理、深浅色主题（CSS 自带）、UI 状态 POST `/api/ui`。阅读器区域先留占位（Task 9 接管）。删除参考 HTML 的"新标签打开/下载"两按钮，换一个"在资源管理器中显示"（Go 侧 `POST /api/reveal {path}`，`explorer /select`；这是桌面端等价物，属本计划的自主决定）。

- [ ] **Step 6: 手工冒烟** — Run: `./recorder.exe` → 选目录 → 逐项点一遍（状态/星标/筛选/搜索/笔记增改删/导入导出/孤儿清理/快捷键）

- [ ] **Step 7: Commit** — `git add frontend && git commit -m "feat: ported UI backed by local API"`

---

### Task 9: 集成 pdf.js 官方 viewer

> **已由 issue #7 交付**（提交 `2772771` / `156b1f6` / `fbaeda5`），本节按交付事实改正原文。原文的「`npm pack pdfjs-dist` 解出 `web/`」是走不通的路：npm 任何版本（已核实 3.11 / 4.10 / 5.4 / 6.4）都没有 `viewer.html`，只有 `PDFViewer` 组件库；官方 viewer 只存在于 GitHub Release 的 dist zip。Task 10–14 同理已被 #2 的 issue 索引取代，不再逐条改写。

**Vendored 清单（6.4.299）：**

保留：`viewer.html`、`viewer.mjs`、`viewer.css`、`images/`、`locale/{en-US,zh-CN}/viewer.ftl`、`locale/locale.json`、`build/pdf.mjs`、`build/pdf.worker.mjs`、`web/cmaps/`、`web/standard_fonts/`、`web/wasm/`、`web/iccs/`、`LICENSE`。
剔除：所有 `.map`、除中英文外的 111 种语言包、`debugger*`、示例 PDF。
版本号记录在 `frontend/vendor/pdfjs/VERSION`，由 `embed_test.go` 钉住（树内不得出现 `.map` / `debugger*` / 多余语言包）。

**Files:**
- Create: `frontend/vendor/pdfjs/{web,build,LICENSE,VERSION}`（从 GitHub Release 的 pdf.js **6.4.299** dist zip 解出，**保持官方原样布局、不改第三方源码**）, `frontend/viewer-bridge.mjs`
- Modify: `frontend/index.html`（阅读器区改为 iframe）, `frontend/vendor/pdfjs/web/viewer.html`（唯一一处本地改动，见 Step 1）, `main.go` embed 范围确认
- Test: `internal/httpfiles/server_test.go`（`TestFilesRejectsTraversal` / `TestFilesRangeRequest`）、`internal/app/contenttypes.go` 的固定类型注册、`embed_test.go`（vendored 树规模与剔除项）、`tests/frontend/bridge.test.mjs`（postMessage 协议）

**Interfaces:**
- Consumes: `/files/<rel>` 可访问；Task 7 的 `totalpages/progress` 接口。
- Produces（Task 10~13 依赖的 postMessage 协议，双方都用 `frontend/lib/bridge.mjs` 导出的常量）:

```
viewer → host: {src:'recorder', type:'ready'|'pages'(total)|'page'(page)}
host → viewer: {src:'host', type:'goto'(page)}   // PDFViewerApplication.page = n
```

- [ ] **Step 1: vendor pdf.js** — Run: 从 `https://github.com/mozilla/pdf.js/releases` 取 `pdfjs-6.4.299-dist.zip`，解出 `build/` 与 `web/` 到 `frontend/vendor/pdfjs/`，按上面的清单剔除，把 `6.4.299` 写进 `VERSION`。在 `viewer.html` 的脚本列表末尾加 `<script src="../../../viewer-bridge.mjs" type="module"></script>` —— 这是全树**唯一**一处对第三方源码的改动（postMessage 桥在 viewer 外部没有入口）。升级流程：整目录替换 + 补回这一行，写进 README 的工程约定。

- [ ] **Step 2: 写 `viewer-bridge.mjs` 的 ready/pages/page 与 goto 处理**（`PDFViewerApplication.eventBus.on('pagesinit'/'pagechanging')`）。两个必须处理的时序坑：① `AppOptions.eventBus` 在 `initialize()` 的异步段中途才赋值，要等 `app.initializedPromise` 再订阅；② `pagesinit` 之前到达的 `goto` 会被 `_setCurrentPageNumber` 静默吞掉，要按 `havePages` 暂存、`pagesinit` 补发。

- [ ] **Step 3: `app.js` 把阅读器区换成 iframe**：`src = /vendor/pdfjs/web/viewer.html?file=` + `encodeURIComponent('/files/'+currentPath)`；监听 host 消息，"跳转到此页"/页码框回车 → `goto`。关闭时把 iframe 导航到 `about:blank`，否则 Chromium 不会卸载上一个文档。

- [ ] **Step 4: 手工验收清单**：打开含书签的 PDF → 缩略图侧栏、`Ctrl+F` 文内查找、打印、大纲、缩放、翻页全部可用；跳页按钮生效；换文件不残留。

- [ ] **Step 5: Commit** — `git add frontend && git commit -m "feat: pdfjs official viewer with bridge"`

---

### Task 10: 阅读进度自动跟踪

**Files:**
- Modify: `frontend/viewer-bridge.js`, `frontend/app.js`, `frontend/lib/throttle.js`（新）
- Test: `frontend/test/throttle.test.js`

**Interfaces:**
- Consumes: 协议 `{type:'pages'|'page'}`；`POST /api/progress`、`POST /api/totalpages`。
- Produces: `lib/throttle.js` 导出 `throttle(fn, ms)`（trailing 调用、立即首调）。

- [ ] **Step 1: 写失败测试** — `test('throttle fires leading then trailing within window')`：假计时器或真实 30ms 窗口断言调用次数与末次参数。Run: `node --test frontend/test/` → FAIL

- [ ] **Step 2: 实现 `throttle`** → Run 测试 → PASS

- [ ] **Step 3: `app.js` 接线**：`page` 消息（throttle 800ms）→ `POST /api/progress`；`pagesinit` 的 `pages` 消息 → `POST /api/totalpages`（仅当与当前值不同）；本地刷新百分比条（沿用参考 HTML 的 `refreshPageUI`）。**页码输入框、跳页按钮行为不变**（手动改动仍然保存）。

- [ ] **Step 4: 手工验收**：翻 5 页 → 关窗重开 → 进度保留；手输页码 → 仍生效；`page>total` 被钳制。

- [ ] **Step 5: Commit** — `git add frontend && git commit -m "feat: auto progress tracking"`

---

### Task 11: 标注层 — 三种工具与绘制

**Files:**
- Create: `frontend/lib/geometry.js`, `frontend/annotate.js`（viewer 内覆盖层逻辑，由 viewer-bridge 加载）
- Modify: `frontend/index.html`（工具栏：划词/画线/框选/删除标注，中文文案）, `frontend/app.js`（工具状态 → postMessage）
- Test: `frontend/test/geometry.test.js`

**Interfaces:**
- Consumes: viewer 内 pdf.js 每页 `.page` 容器（`data-page-number`）。
- Produces（Task 12/13 依赖）:

```js
// geometry.js（纯函数）
clientRectToNorm(pageRect, rect)   // {x,y,w,h} 客户端坐标 → 0..1 归一化
normRectToClient(pageRect, n)      // 逆变换
pointsToNorm(pageRect, points)     // [[x,y]...] → 归一化
hitTest(ann, nx, ny)               // 点(0..1)是否命中标注，eps=0.01
// 归一化数据形状（即 Annotation.Data）
highlight: {"rects":[{x,y,w,h}...]}
line:      {"points":[{x,y}...]}
box:       {"rect":{x,y,w,h}}
// 协议新增
viewer → host: {type:'ann-created', page, kind, data}
host → viewer: {type:'tool', tool:'highlight'|'line'|'box'|'erase'|null}
```

- [ ] **Step 1: 写失败测试**

```js
test('clientRectToNorm and normRectToClient round-trip at different zoom scales') // 同一标注在 pageRect 1000px 与 500px 宽下还原一致 [防缩放漂移]
test('pointsToNorm normalizes polyline')
test('hitTest hits box/line/highlight within eps and misses outside')
```

- [ ] **Step 2: 运行确认失败** — Run: `node --test frontend/test/` → FAIL

- [ ] **Step 3: 实现 `geometry.js`** → PASS

- [ ] **Step 4: 实现 `annotate.js`**：每个 `.page` 内注入覆盖层；划词=鼠标 up 后 `getSelection()` 的多 rect → 归一化；画线=pointer 轨迹采样（每 4px 取点）；框选=两点矩形；erase=点击命中删除（先本地删，Task 12 接持久化）。绘制样式：划词半透明黄、线 2px 红/蓝、框描边。

- [ ] **Step 5: 工具栏接线**（host `tool` 消息；激活态高亮）

- [ ] **Step 6: 手工验收**：三种工具各画一次 → 缩放后仍贴合原位；切换文件覆盖层清空；erase 能删。

- [ ] **Step 7: Commit** — `git add frontend && git commit -m "feat: annotation drawing tools with normalized geometry"`

---

### Task 12: 标注持久化（保存/加载/删除）

**Files:**
- Modify: `frontend/app.js`, `frontend/viewer-bridge.js`, `frontend/annotate.js`
- Test: Go 侧已由 Task 3 `TestAnnotationCRUD` 覆盖；本任务补 `frontend/test/bridge-protocol.test.js`

**Interfaces:**
- Consumes: `POST /api/annotations`、`GET /api/annotations?path=`、`POST /api/annotations/delete`；协议 `ann-created`。
- Produces: 协议补齐 —— `{type:'ann-deleted', id}`（viewer→host）；`{type:'render', annotations:[{id,page,kind,data}]}`（host→viewer，打开文件后全量渲染）。

- [ ] **Step 1: 写失败测试** — `test('bridge message shapes match contract')`：对 `lib/bridge.js` 的构造函数断言 `src/type` 字段与必需字段。Run → FAIL

- [ ] **Step 2: 实现 bridge 构造/校验函数** → PASS

- [ ] **Step 3: 接线**：`ann-created` → `POST /api/annotations`（拿 id）→ 记录到当前文件集；打开文件后 `GET` → `render`；erase 删除 → `POST .../delete` → 从本地集移除。

- [ ] **Step 4: 手工验收**：划 3 处标注 → 关窗重开 → 原样显示；erase 删除 → 重开不复活；不同 PDF 标注互不串。

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: annotation persistence"`

---

### Task 13: 笔记 ↔ 标注双向关联

**Files:**
- Modify: `frontend/app.js`, `frontend/viewer-bridge.js`, `frontend/annotate.js`, `frontend/app.css`
- Test: `frontend/test/note-ann.test.js`（关联选择器的纯逻辑）

**Interfaces:**
- Consumes: `/api/notes`、`/api/links`(+delete)、`GET /api/annotations`；`Note.AnnIDs`（Task 3）。
- Produces 协议：

```
host → viewer: {type:'show-note-popup', annId, page}   // 标注完成且用户未跳过时
viewer → host: {type:'save-note', annId, page, text}   // text 空 = 取消（跳过）
host → viewer: {type:'flash', id}                      // 点笔记跳转后闪烁定位
```

- [ ] **Step 1: 写失败测试** — 纯函数：`annLabel(ann)`（`p.3 · 划词` 等展示文案）、`linkedNotes(notes, annId)`（反查）。Run → FAIL

- [ ] **Step 2: 实现并通过** — Run: `node --test frontend/test/` → PASS

- [ ] **Step 3: 就地弹框**：`ann-created` 持久化成功后 host 回发 `show-note-popup` → viewer 在标注旁出小 textarea → 保存/空文本取消；保存 = `POST /api/notes` + `POST /api/links`。

- [ ] **Step 4: 面板补挂**：笔记行显示标注徽标（`🔗N`）；"挂标注"按钮弹出该文件标注选择列表（`annLabel` 文案，纯函数产出）→ `POST /api/links`；已挂的可"取消关联"（`POST /api/links/delete`，标注本身不删）。

- [ ] **Step 5: 点笔记跳转**：笔记行或徽标点击 → 有 `annIds` → 取第一条标注 → `goto` 其页 → `flash` 闪烁 1.2s；无标注但有页码 → 仅跳页。

- [ ] **Step 6: 手工验收**：划词→弹框写笔记→徽标出现→点徽标跳回原位闪烁；面板里给旧笔记挂/摘标注；取消弹框不产生笔记。

- [ ] **Step 7: Commit** — `git add -A && git commit -m "feat: bidirectional notes-annotations linking"`

---

### Task 14: 打包发布（单文件 exe）

**Files:**
- Create: `build/windows/icon.ico`, `build/windows/info.json`（go-winres 输入）, `Makefile`, `README.md`
- Modify: `main.go`（版本 ldflags、WebView2 缺失时的提示）

**Interfaces:**
- Consumes: 前 13 个任务的完整实现。
- Produces: `dist/recorder.exe`（带图标与版本信息）。

- [ ] **Step 1: 图标与版本** — `go install github.com/tc-hib/go-winres@latest`；`go-winres make` 生成 `rsrc_windows_$GOARCH.syso`；`Makefile`：`go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/recorder.exe .`

- [ ] **Step 2: WebView2 检测** — 启动时读注册表 `HKLM\SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`（及 WOW6432Node 视图）判断 evergreen 是否存在；缺失 → 弹 MessageBox 给出 WebView2 Runtime 下载链接（https://developer.microsoft.com/microsoft-edge/webview2/），参考 magpie 的 `internal/gui/webview2.go`。

- [ ] **Step 3: 全量回归** — Run: `go test ./...` && `node --test frontend/test/` → 全 PASS；跑一次 Task 8~13 的手工验收清单。

- [ ] **Step 4: 干净环境验证** — 从 `dist/recorder.exe` 双击启动（非开发目录），完成一次"选目录→阅读→标注→笔记→关开重进"全流程；确认 `%APPDATA%\recorder\recorder.db` 生成。

- [ ] **Step 5: README** — 构建方法（`make` / 命令行）、数据位置、功能与快捷键说明。

- [ ] **Step 6: Commit** — `git add -A && git commit -m "build: packaged single-file windows executable"`

---

## Self-Review 记录

- **Spec 覆盖**：目标/非目标逐条对应 Task 1~14（目录扫描 T4/T8、状态进度星标 T2/T8、统计搜索 T8、笔记 T8、导入导出 T5、深浅色 T8（CSS 自带）、孤儿清理 T8、三工具 T11、就地弹框+面板补挂+跳转 T13、自动进度 T10、SQLite/%APPDATA% T2、单根自动重扫 T7、官方 viewer T9、单 exe/图标/WebView2 T14、OCR/迁移/安装包在非目标中明确不做）。
- **类型一致性**：`FileRec/Note/Annotation` 在 T2/T3 定义、T5/T7 消费；bridge 协议字段在 T9 定义、T10~13 消费；`lib` 纯函数导出名与测试名一致。
- **Review Focus** 五条各有归属任务与测试（见各条行内标注）；缩放漂移另在 T11 有 round-trip 测试。
- **比例**：本计划只给签名、测试名与断言要点；唯一给出数据形状的地方是跨任务契约（SQLite 字段、HTTP JSON、postMessage 协议、导出格式），实现体留给执行者。
