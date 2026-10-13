# recorder

本地优先的 Windows PDF 阅读器：**阅读进度 + 笔记 + 标注**，数据全部存在你自己的电脑上。

单文件 exe，无需安装、无需账号、不联网。

> **状态：开发中**（实现计划 14 个任务，已完成 5 个）。当前构建可以启动窗口，但 PDF 阅读与笔记功能尚在开发。见 [Issues](https://github.com/skyarea/recorder/issues)。

## 功能

- **目录树扫描** —— 指定一个根目录，递归列出所有 PDF；路径用相对路径记录，换机器也能对上
- **阅读状态与进度** —— `待读 / 在读 / 已完成 / 跳过` 四态，滚动自动记录进度，星标收藏
- **三种标注** —— 划词高亮、自由画线、矩形框选
- **手写笔记** —— 笔记完全手写（不自动生成），支持 Markdown
- **笔记 ↔ 标注互链** —— 多对多关联；从笔记一键跳回 PDF 对应位置，从标注也能反向找到笔记
- **统计与搜索** —— 状态统计、全文搜索笔记
- **导出 / 导入 JSON** —— 备份与迁移，兼容旧版 HTML 应用的格式
- **深色 / 浅色主题** —— 快捷键 `J` `K` `1-4` `S` `/`

## 下载

前往 [Releases](https://github.com/skyarea/recorder/releases) 下载 `recorder.exe`，双击运行。

数据目录：`%APPDATA%\recorder\recorder.db`（SQLite）。

## 技术栈

| 层 | 选型 | 理由 |
| --- | --- | --- |
| 应用外壳 | Go + [Wails v3](https://v3alpha.wails.io/) | 单进程、单文件 exe，WebView2 渲染 |
| 前端 | 无框架原生 JS/HTML | 由旧版参考实现移植，零构建步骤 |
| PDF 渲染 | pdf.js 官方 viewer（vendored） | 保留查找、打印、缩略图、大纲、缩放 |
| 数据 | SQLite via `modernc.org/sqlite` | 纯 Go，**无 CGO**，交叉编译无痛 |
| 前后端通信 | 进程内 HTTP + `fetch('/api/*')` | 无绑定生成器、无打包器、无代码生成 |

架构借鉴 [magpie](https://github.com/yetone/magpie)（MIT）的「embed 前端资源 + 自有 HTTP handler」模式。

## 开发

需要 Go 1.26+、Node（仅用于 vendor pdf.js）。

```bash
go build ./...          # 编译
go test ./... -count=1  # 测试
go vet ./...            # 静态检查

# 运行（开发）
go run .

# 打包发布（Task 14 完成后）
go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/recorder.exe .
```

### 提交前

启用本地 git 钩子（拦截密钥、敏感文件与大文件）：

```bash
bash scripts/install-hooks.sh
```

## 工程约定

- **无 CGO**：SQLite 必须用 `modernc.org/sqlite`，测试在 `CGO_ENABLED=0` 下通过
- **无绑定生成器、无打包器**：纯 `go build` + `//go:embed`
- **数据不迁移**：不兼容旧版 localStorage 数据
- 设计文档：[`docs/superpowers/specs/`](docs/superpowers/specs/)
- 实现计划：[`docs/superpowers/plans/`](docs/superpowers/plans/)

## CI

| Workflow | 触发 | 内容 |
| --- | --- | --- |
| [CI](.github/workflows/ci.yml) | push / PR | `gofmt`、`go vet`、`go build`、`go test`（`CGO_ENABLED=0`） |
| [Secret scan](.github/workflows/secret-scan.yml) | push / PR | gitleaks 全历史密钥扫描 |
| [Release](.github/workflows/release.yml) | tag `v*` / 手动 | 构建并发布 `recorder.exe` 到 Releases |
| [Deploy Pages](.github/workflows/pages.yml) | `docs/pages/**` 变更 | 发布下载引导页 |

## License

MIT
