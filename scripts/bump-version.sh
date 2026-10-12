#!/usr/bin/env bash
# ==============================================================================
# TWC Approval Go Bridge - 全局版本同步与升级工具 (Version Bump Script)
# ==============================================================================
# 用法:
#   ./scripts/bump-version.sh <新版本号>
#   或者通过 Makefile: make bump VERSION=0.3.4
# ==============================================================================

set -euo pipefail

NEW_VERSION="${1:-}"

if [[ -z "$NEW_VERSION" ]]; then
  echo "❌ 错误: 请指定目标语义化版本号 (例如: 0.3.4)"
  echo "用法: $0 <version>"
  exit 1
fi

# 去除前缀 'v'（如果用户传入了 v0.3.4）
NEW_VERSION="${NEW_VERSION#v}"

# 验证语义化版本格式 (SemVer: X.Y.Z)
if [[ ! "$NEW_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "❌ 错误: 版本号 '$NEW_VERSION' 不符合 SemVer 2.0.0 规范 (必须为 X.Y.Z 格式，如 0.3.4)"
  exit 1
fi

# 切换到项目根目录
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# 获取当前版本
CURRENT_VERSION=$(grep -E 'Version = "[0-9]+\.[0-9]+\.[0-9]+"' internal/version/version.go | sed -E 's/.*"([0-9]+\.[0-9]+\.[0-9]+)".*/\1/')

echo "=========================================="
echo "🚀 开始同步升级全局版本号"
echo "   当前版本: $CURRENT_VERSION"
echo "   目标版本: $NEW_VERSION"
echo "=========================================="

if [[ "$CURRENT_VERSION" == "$NEW_VERSION" ]]; then
  echo "ℹ️ 当前版本已经是 $NEW_VERSION，无需更新。"
  exit 0
fi

# 1. 更新 internal/version/version.go
sed -i '' -E "s/Version = \"$CURRENT_VERSION\"/Version = \"$NEW_VERSION\"/g" internal/version/version.go
sed -i '' -E "s/release base $CURRENT_VERSION/release base $NEW_VERSION/g" internal/version/version.go
echo "  ✅ 已同步: internal/version/version.go"

# 2. 更新 internal/version/version_test.go
sed -i '' -E "s/\"$CURRENT_VERSION\"/\"$NEW_VERSION\"/g" internal/version/version_test.go
sed -i '' -E "s/v$CURRENT_VERSION/v$NEW_VERSION/g" internal/version/version_test.go
echo "  ✅ 已同步: internal/version/version_test.go"

# 3. 更新 Makefile
sed -i '' -E "s/echo $CURRENT_VERSION/echo $NEW_VERSION/g" Makefile
echo "  ✅ 已同步: Makefile"

# 4. 更新 deploy/Dockerfile
sed -i '' -E "s/ARG VERSION=$CURRENT_VERSION/ARG VERSION=$NEW_VERSION/g" deploy/Dockerfile
echo "  ✅ 已同步: deploy/Dockerfile"

# 5. 更新 deploy/k8s/twc-approval.yaml
if [[ -f deploy/k8s/twc-approval.yaml ]]; then
  sed -i '' -E "s/twc-approval-go-bridge:v$CURRENT_VERSION/twc-approval-go-bridge:v$NEW_VERSION/g" deploy/k8s/twc-approval.yaml
  echo "  ✅ 已同步: deploy/k8s/twc-approval.yaml"
fi

# 6. 更新 AGENTS.md 准上线基线版本定义
if [[ -f AGENTS.md ]]; then
  sed -i '' -E "s/准上线基线版本定义为 \`$CURRENT_VERSION\`/准上线基线版本定义为 \`$NEW_VERSION\`/g" AGENTS.md
  echo "  ✅ 已同步: AGENTS.md"
fi

echo "------------------------------------------"
echo "🔍 运行版本一致性守卫测试..."
go test -v ./internal/version -run TestVersionConsistency

echo "=========================================="
echo "🎉 全局版本同步成功！所有构建与部署文件已对齐至: $NEW_VERSION"
echo "   后续提示:"
echo "   1. 请在 CHANGELOG.md 中登记版本更新内容并将 [Unreleased] 归档为 [${NEW_VERSION}]"
echo "   2. 提交并打标签: git commit -am \"chore(release): bump version to ${NEW_VERSION}\""
echo "   3. 创建版本标签: git tag -a v${NEW_VERSION} -m \"Release v${NEW_VERSION}\""
echo "=========================================="
