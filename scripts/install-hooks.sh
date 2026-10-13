#!/usr/bin/env bash
# 安装本仓库的 git 钩子。可重复执行。
# 用法：bash scripts/install-hooks.sh
set -euo pipefail

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo"

chmod +x scripts/pre-commit 2>/dev/null || true

# hooksPath 让钩子随仓库走：换台机器 clone 后重跑本脚本即可
git config --local core.hooksPath scripts

echo "✓ 已安装 git hooks（core.hooksPath = scripts）"
echo "  提交前会检查：密钥模式 / 敏感文件 / 大文件"
echo "  绕过：git commit --no-verify"
