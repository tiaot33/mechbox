#!/usr/bin/env bash
# 把 .cursor/seed 下的测试固件部署到一个可写沙盒(默认 ~/skills-sandbox)。
# 幂等:重复运行只刷新固件本身,保留沙盒里新增的文件(如激活产生的投影副本)。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SEED_SRC="${REPO_ROOT}/.cursor/seed"
SANDBOX="${SKILLS_SANDBOX_DIR:-${HOME}/skills-sandbox}"

if [[ ! -d "${SEED_SRC}" ]]; then
  echo "seed source not found at ${SEED_SRC}, skipping" >&2
  exit 0
fi

mkdir -p "${SANDBOX}/skills" "${SANDBOX}/projects"

# cp -a 保留可执行位与目录结构;不加 --delete,避免清除沙盒里的测试产物。
cp -a "${SEED_SRC}/skills/." "${SANDBOX}/skills/"
cp -a "${SEED_SRC}/projects/." "${SANDBOX}/projects/"
cp -a "${SEED_SRC}/README.md" "${SANDBOX}/README.md"

echo "Seeded test sandbox at ${SANDBOX}"
echo "  skills:   $(find "${SANDBOX}/skills" -maxdepth 1 -mindepth 1 -type d -printf '%f ' 2>/dev/null)"
echo "  projects: $(find "${SANDBOX}/projects" -maxdepth 1 -mindepth 1 -type d -printf '%f ' 2>/dev/null)"
