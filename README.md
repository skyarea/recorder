# recorder

本地优先的 Windows PDF 阅读器：**阅读进度 + 笔记 + 标注**，数据全部存在你自己的电脑上。

[![CI](https://github.com/skyarea/recorder/actions/workflows/ci.yml/badge.svg)](https://github.com/skyarea/recorder/actions/workflows/ci.yml)
[![Secret scan](https://github.com/skyarea/recorder/actions/workflows/secret-scan.yml/badge.svg)](https://github.com/skyarea/recorder/actions/workflows/secret-scan.yml)

*这将是一个*单文件 exe，无需安装、无需账号、不联网。**现在还没有能下载的程序。**

## 现在的状态

**读、标注、笔记都齐了，打包命令也已就绪。** `go run .`（或 `make` 出的
`dist/recorder.exe`）开一个窗口：选定目录 → 左边列出 PDF → 点开在右边读（vendored
的 pdf.js 官方 viewer，滚动/缩放/`Ctrl+F`/缩略图/大纲/打印都在）。滚动自动存进度，
划词/画线/框选三种标注落库，标注后就地弹笔记、面板里也能补挂，笔记↔标注双向互链
可点徒回原位闪烁。数据全部写进 `%APPDATA%\recorder\recorder.db`。

已经写好的部分：

| 已就绪 | 说明 | 用的地方 |
| --- | --- | --- |
| 数据层 | SQLite（纯 Go）—— 文件、进度、星标、笔记、标注、笔记↔标注多对多 | 有测试 |
| 目录扫描 | 递归收集 PDF，路径为「根目录相对路径」，页数嗅探 | 有测试 |
| 备份 | JSON 导出 / 导入，格式与旧版网页应用兼容 | 有测试 |
| 本地服务 | 只监听 `127.0.0.1` 随机端口的 HTTP 服务，`/api/*` 给前端取状态 | 有测试 |
| 阅读器 | `/files/*` 供文档（含 Range），iframe + postMessage 桥 | 有测试 |
| 界面 | 目录树、状态/星标、统计、搜索、主题、笔记面板 | #8 |
| 进度自动跟随 | 滚动经节流写 `/api/progress`，页码框可跳转 | #9 |
| 标注 | 划词/画线/框选三工具 + 持久化 + 回访，归一化几何防缩放漂移 | #10~#13 |
| 笔记↔标注互链 | 就地弹框 + 面板补挂徒标 + 跳转闪烁，多对多 | #14 |
| 打包 | `make` / `go-winres`：单文件 exe，带图标与版本信息，WebView2 缺失弹提示 | #15 |

还差的收尾（打包的自动化部分已完成，剩下的需真人/对外动作）：

| # | 事项 |
| --- | --- |
| #15 | 手工验收 10 步（见下清单）+ 打 tag 真实发布一次 |

### 记在案上的待办（还没有对应 issue）

- **JBIG2 扫描件没实测过** —— `web/wasm/jbig2.wasm` 在位、可服务、类型正确，OpenJPEG 那条路已经拿一个「整页只有一张 JPX 图」的 PDF 真跑通（wasm 被请求、解码并上屏）。JBIG2 需要一个编码器（`jbig2enc`）才造得出真件，本机 Ghostscript 10.08 的构建里没有 JBIG2 编码路径。

想跟踪进度看 [Issues](https://github.com/skyarea/recorder/issues)；
想了解设计意图看 [设计文档](docs/superpowers/specs/2026-10-13-pdf-reader-desktop-design.md)；
想了解实施顺序看 [实现计划](docs/superpowers/plans/2026-10-13-pdf-reader-desktop.md)。

## 界面示意

下面是这个应用打算长成的样子 —— 一张矢量示意，不是运行截图（图里的标注界面还没实现）。

![recorder 主界面示意：左侧是标着阅读状态的 PDF 目录树，中间 PDF 页面上有划词高亮、矩形框选和自由画线三种标注，右侧是带标注徽标的笔记面板](docs/pages/app-preview.svg)

图本身在深浅两种模式下都是自洽的（这两套配色都各自满足正文对比度 4.5:1），
所以不会出现「深色页面上贴一张浅底图看不清」的情况。
跟随的是系统主题，不是 GitHub 右上角的主题开关。

## 从源码构建

需要 Go 1.26.4+（见 `go.mod`；CI 用 `go-version-file` 跟随它，低版本会自动拉工具链）。

```bash
go build ./...          # 编译
go test ./... -count=1  # 测试
go vet ./...            # 静态检查

go run .                # 运行（开发）—— 开窗并加载本地服务的前端
```

Node 只在 CI 里需要：跑落地页配色自检（`node docs/landing/check-contrast.mjs`）
和前端测试（`node --test tests/frontend/*.test.mjs`）。构建与运行本项目本身不需要 Node。

数据目录（设计目标）见下面的「数据与备份」一节。

### 打包成单文件 exe（Windows）

产物是 `dist/recorder.exe`：一个文件，带图标与版本信息，双击即用，无需安装包。

前置：`go install github.com/tc-hib/go-winres@latest`（生成 Windows 资源）。

```bash
make            # = make exe：生成资源 + 构建 dist/recorder.exe
make test       # 全量回归（go test ./... + node --test）
make version    # 本次构建用的版本号（取自 git tag，无 tag 则 0.0.0-dev）
```

没有 `make`（比如裸 Windows）就照 `Makefile` 里的两条命令手跑：先
`go-winres simply …` 生成 `rsrc_windows_amd64.syso`，再
`go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o dist/recorder.exe .`。
`go build` 会自动收录同目录的 `.syso`，图标与版本信息就进了 exe。

- **版本号来自 git tag**：`v1.2.3` → exe 属性里显示 `1.2.3`，`-X main.version` 与
  `go-winres` 用同一个值。`.syso` 每次现场生成、不入库（版本必须跟着 tag 走）。
- **换图标**：替换 `build/windows/icon.ico`（源图 `icon-source.png` 一并留着），
  重新打 tag 跑 `make` 即可 —— 图标是最容易替换的一块，占位图标是刻意的。
- **WebView2 缺失**：程序启动先查注册表，没有 WebView2 运行库时弹一个中文对话框
  给出下载链接，而不是无声崩溃（目标机若已装 Edge 则自带该运行库）。
- **发布**：推 `v*` tag（或手动跑 `Release` workflow）即构建并上传 `recorder.exe` 到
  GitHub Release。见「CI」一节。

### 手工验收清单（发布前跑一遍）

自动化测试覆盖状态/进度/搜索/导出与纯逻辑；下面这些「GUI 交互对不对」只有真人
点得出来，从 `dist/recorder.exe`（非开发目录）双击起：

1. 双击 exe → 窗口打开，标题带版本号，任务栏/窗口有图标。
2. 点「选择目录」→ 选一个含 PDF 的根目录 → 左侧列出文件。
3. 点一个 PDF → 阅读区出图；滚动翻页 → 右下角进度条/页码自动跟随。
4. 选「划词」→ 选中一段文字 → 出高亮，并就地弹笔记框；写一句保存。
5. 笔记面板里该笔记出现「标注徽标」；点徽标 → 跳回那一页并闪烁定位。
6. 笔记面板「挂标注」→ 给已有笔记补挂/取消挂某条标注，徽标数量随之变。
7. 页码框输一个页码回车 → 跳到该页，与滚动进度一致。
8. 关闭程序，重开 → 进度、标注、笔记都还在（数据在 `%APPDATA%\recorder\recorder.db`）。
9. 换一个扫描件 PDF 打开 → 能渲染（验证 vendored 的 wasm/cmaps 都在位）。
10. 确认 `%APPDATA%\recorder\recorder.db` 已生成且非空。

### 提交前

启用本地 git 钩子（拦截密钥、敏感文件与大文件）：

```bash
bash scripts/install-hooks.sh
```

## 数据与备份

数据全在本机，只有一个 SQLite 文件：

```
%APPDATA%\recorder\recorder.db
```

不放程序目录（避免 UAC 虚拟化与卸载残留），你可以直接找到它、直接拷走它。
迁移到另一台机器用「导出 JSON」—— 备份格式与旧版网页应用通用，所以
**从旧版导出的 JSON 可以直接导入新版**。反过来不成立：旧版 localStorage 的数据
不会自动搬过来，要先用旧版导出。

**注意：进度与笔记的写入接口已接通**（#8 界面、#9 进度自动跟随、#14 笔记↔标注互链）——
滚动会节流地写 `/api/progress`，打开文档写 `/api/totalpages`，标注与笔记经
`/api/notes`、`/api/annotations`、`/api/links` 落库。程序一跑起来这个文件就会被建出来。

## 功能

以下功能均已实现（目录扫描、pdf.js 阅读器、状态/进度、三工具标注、笔记、
互链、统计搜索、导出导入、主题）：

- **目录树扫描** —— 指定一个根目录，递归列出所有 PDF；路径用相对路径记录，换机器也能对上
- **阅读状态与进度** —— `未读 / 在读 / 已读 / 跳过` 四态，滚动自动记录进度（节流），星标收藏
- **三种标注** —— 划词高亮、自由画线、矩形框选，归一化坐标防缩放漂移，可擦除
- **手写笔记** —— 笔记完全手写（不自动生成），支持 Markdown
- **笔记 ↔ 标注互链** —— 多对多关联；标注后就地弹框写笔记，面板里可补挂/取消挂；从笔记一键跳回 PDF 对应位置并闪烁定位
- **统计与搜索** —— 状态统计、全文搜索笔记
- **导出 / 导入 JSON** —— 备份与迁移，兼容旧版 HTML 应用的格式
- **深色 / 浅色主题** —— 跟随系统，可手动切换并记住

### 快捷键

| 键 | 作用 |
| --- | --- |
| `J` / `↓` `K` / `↑` | 在列表里跳到下一个 / 上一个文件 |
| `1` `2` `3` `4` | 设为 `待读` / `在读` / `已读` / `跳过` |
| `S` | 切换星标 |
| `/` | 光标进搜索框 |
| `Enter` | 页码框里回车 = 跳页 |
| `Ctrl+Enter` | 保存正在写的笔记 |
| `Esc` | 取消编辑中的笔记 |

## 技术选型

| 层 | 选型 | 理由 |
| --- | --- | --- |
| 应用外壳 | Go + [Wails v3](https://v3alpha.wails.io/) | 单进程、单文件 exe，WebView2 渲染 |
| 前端 | 无框架原生 JS/HTML | 由旧版参考实现移植，零构建步骤 |
| PDF 渲染 | pdf.js 官方 viewer（vendored） | 保留查找、打印、缩略图、大纲、缩放 |
| 数据 | SQLite via `modernc.org/sqlite` | 纯 Go，**无 CGO**，交叉编译无痛 |
| 前后端通信 | 进程内 HTTP + `fetch('/api/*')` | 无绑定生成器、无打包器、无代码生成 |

架构借鉴 [magpie](https://github.com/yetone/magpie)（MIT）的「embed 前端资产 + 自有 HTTP handler」模式。

## 工程约定

- **无 CGO**：SQLite 必须用 `modernc.org/sqlite`，测试在 `CGO_ENABLED=0` 下通过
- **无绑定生成器、无打包器**：纯 `go build` + `//go:embed`
- **数据不迁移**：不兼容旧版 localStorage 数据（导出 / 导入 JSON 仅作备份功能）
- **单根目录**：上次扫描的目录记在设置里，启动时自动重扫；文件身份为「根目录相对路径、`/` 分隔」
- **pdf.js 是 vendored 的第三方源码**：`frontend/vendor/pdfjs/` 保持上游目录结构、原样不动，`VERSION` 记下版本号。升级 = 用新 release 的产物整目录替换（同样精简掉 sourcemap、多余语言、debugger、示例 PDF），再把 `viewer.html` 里那一行 `<script src="../../../viewer-bridge.mjs">` 补回去 —— 那是全树唯一一处本地改动
- **响应类型由程序自己定，不信操作系统**：`internal/app/contenttypes.go` 固定了 `.html/.css/.js/.mjs/.json/.wasm` 的 Content-Type。Windows 上 Go 先查注册表、查不到才用内置表，任何装过的软件都能把 `.mjs` 登记成 `text/plain`（CI 镜像就是），而浏览器会拒绝按模块执行它、`WebAssembly.instantiateStreaming` 也会直接失败

设计文档在 [`docs/superpowers/specs/`](docs/superpowers/specs/)，
实现计划在 [`docs/superpowers/plans/`](docs/superpowers/plans/)。

## CI

| Workflow | 触发 | 内容 |
| --- | --- | --- |
| [CI](.github/workflows/ci.yml) | push / PR | `gofmt`、`go vet`、`go build`、`go test`（`CGO_ENABLED=0`）、落地页配色对比度、前端测试（`node --test`） |
| [Secret scan](.github/workflows/secret-scan.yml) | push / PR | gitleaks 全历史密钥扫描 |
| [Release](.github/workflows/release.yml) | tag `v*` / 手动 | 构建并发布 `recorder.exe`（尚无 tag，从未运行） |
| [Deploy Pages](.github/workflows/pages.yml) | `docs/pages/**` 变更 | 发布下载引导页（工作流本身可用，但仓库尚未启用 Pages） |

## License

尚未选定。代码目前**没有** `LICENSE` 文件 —— 在你补上之前，默认保留所有权利。
