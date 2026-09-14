#!/usr/bin/env bash
# 根据已暂存的改动数量,打印一个 Conventional Commits 风格的提交信息骨架。
set -euo pipefail

type="${1:-chore}"
scope="${2:-}"
staged_count="$(git diff --cached --name-only 2>/dev/null | wc -l | tr -d ' ')"

if [[ -n "${scope}" ]]; then
  header="${type}(${scope}): "
else
  header="${type}: "
fi

echo "${header}<summary>"
echo
echo "# ${staged_count} file(s) staged. 请在上一行填写简述,并在此处补充正文。"
