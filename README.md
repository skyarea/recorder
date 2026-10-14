# recorder

本地优先的 Windows PDF 阅读器：**阅读进度 + 笔记 + 标注**，数据全部存在你自己的电脑上。

[![CI](https://github.com/skyarea/recorder/actions/workflows/ci.yml/badge.svg)](https://github.com/skyarea/recorder/actions/workflows/ci.yml)
[![Secret scan](https://github.com/skyarea/recorder/actions/workflows/secret-scan.yml/badge.svg)](https://github.com/skyarea/recorder/actions/workflows/secret-scan.yml)

单文件 exe，无需安装、无需账号、不联网。

## 现在的状态

**开发中 —— 后端已经能跑，界面还没有。**

截至今天，底层四件已经落地并有测试（Go，`CGO_ENABLED=0`）：

| 已就绪 | 说明 |
| --- | --- |
| 数据层 | SQLite（`modernc.org/sqlite`，纯 Go）—— 文件、进度、星标、笔记、标注、笔记↔标注多对多关联 |
| 目录扫描 | 递归收集 PDF，路径为「根目录相对路径」，页数嗅探 |
| 备份 | JSON 导出 / 导入，格式与旧版网页应用兼容 |
| 窗口骨架 | Wails v3 窗口 + 嵌入前端资源 |

还没做的（按 [issue](https://github.com/skyarea/recorder/issues) 计，而非按内部计划计）：

- 本地 HTTP 服务与 `/api/*` 接口 —— #6
- PDF 真的能读（`/files/*` + pdf.js 阅读器）—— #7
- 应用界面（目录树、状态、搜索、主题）—— #8
- 阅读进度自动跟随 —— #9
- 三种标注工具（划词 / 画线 / 框选）与回访 —— #10 ~ #13
- 笔记 ↔ 标注双向互链 —— #14
- 打包成单文件 exe —— #15

也就是说：**现在从 Releases 还下不到能用的程序**（也确实还没有任何发布）。
要跟踪进度请看 [Issues](https://github.com/skyarea/recorder/issues)，
要了解设计意图请看 [设计文档](docs/superpowers/specs/2026-10-13-pdf-reader-desktop-design.md)，
要了解实施顺序请看 [实现计划](docs/superpowers/plans/2026-10-13-pdf-reader-desktop.md)。

## 从源码构建

需要 Go 1.26+。Node 仅在未来 vendor pdf.js 时才用得上，现在不需要。

```bash
go build ./...          # 编译
go test ./... -count=1  # 测试
go vet ./...            # 静态检查

go run .                # 运行（开发）
```

数据目录：`%APPDATA%\recorder\recorder.db`（SQLite）。程序目录不放数据，避免 UAC 虚拟化与卸载残留。

### 提交前

启用本地 git 钩子（拦截密钥、敏感文件与大文件）：

```bash
bash scripts/install-hooks.sh
```

## 功能（目标形态）

以下是这个应用做出来之后应该有的样子。目录扫描、数据层、备份这几项的底层已经就绪，
但**界面本身还没开始**，所以现在能用的只有上面表格里的四件。

- **目录树扫描** —— 指定一个根目录，递归列出所有 PDF；路径用相对路径记录，换机器也能对上
- **阅读状态与进度** —— `待读 / 在读 / 已完成 / 跳过` 四态，滚动自动记录进度，星标收藏
- **三种标注** —— 划词高亮、自由画线、矩形框选
- **手写笔记** —— 笔记完全手写（不自动生成），支持 Markdown
- **笔记 ↔ 标注互链** —— 多对多关联；从笔记一键跳回 PDF 对应位置，从标注也能反向找到笔记
- **统计与搜索** —— 状态统计、全文搜索笔记
- **导出 / 导入 JSON** —— 备份与迁移，兼容旧版 HTML 应用的格式
- **深色 / 浅色主题** —— 快捷键 `J` `K` `1-4` `S` `/`

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
- **单根目录**：记住上次扫描目录并启动时自动重扫；文件身份为「根目录相对路径、`/` 分隔」

设计文档在 [`docs/superpowers/specs/`](docs/superpowers/specs/)，
实现计划在 [`docs/superpowers/plans/`](docs/superpowers/plans/)。

## CI

| Workflow | 触发 | 内容 |
| --- | --- | --- |
| [CI](.github/workflows/ci.yml) | push / PR | `gofmt`、`go vet`、`go build`、`go test`（`CGO_ENABLED=0`）、落地页配色对比度 |
| [Secret scan](.github/workflows/secret-scan.yml) | push / PR | gitleaks 全历史密钥扫描 |
| [Release](.github/workflows/release.yml) | tag `v*` / 手动 | 构建并发布 `recorder.exe` 到 Releases（尚无 tag，从未运行） |
| [Deploy Pages](.github/workflows/pages.yml) | `docs/pages/**` 变更 | 发布下载引导页（工作流本身可用，但仓库尚未启用 Pages） |

## License

尚未选定。代码目前**没有** `LICENSE` 文件 —— 在你补上之前，默认保留所有权利。
