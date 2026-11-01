#!/usr/bin/env bash
# 判断 HEAD 该不该自动发一版。该发时输出新版本号(如 0.0.5),不该发时什么都不输出。
# 基准是按版本号排最高的正式版 tag(vX.Y.Z,不含预发布);从它往后数 main 上用户能感觉到的 PR,
# 即标题是 feat / fix / perf、末尾带 (#PR 号) 的合并提交，满 THRESHOLD 个(默认 3)就在最后一位加 1。
# 用法:next-version.sh [THRESHOLD]。没有任何正式版 tag 时报错退出。
set -euo pipefail

threshold="${1:-3}"

latest="$(git tag --list 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n 1 || true)"
if [[ -z "$latest" ]]; then
  echo "没有 vX.Y.Z 形式的正式版 tag,无法确定基准版本" >&2
  exit 1
fi

pattern='^(feat|fix|perf)(\([^)]*\))?!?: .+ \(#[0-9]+\)$'
count="$(git log --first-parent --format=%s "$latest..HEAD" | grep -cE "$pattern" || true)"
echo "基准 $latest,之后计数的 PR $count 个，门槛 $threshold 个" >&2

if (( count < threshold )); then
  exit 0
fi

IFS=. read -r major minor patch <<<"${latest#v}"
echo "$major.$minor.$((patch + 1))"
