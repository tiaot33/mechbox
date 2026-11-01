#!/usr/bin/env bash
# 判断 HEAD 该不该自动发一版。该发时在 stdout 输出新版本号(如 0.0.5),不该发时什么都不输出;判断经过写在 stderr。
# 基准是按版本号排最高的正式版 tag(vX.Y.Z,不含预发布);从它往后数 main 上用户能感觉到的 PR,
# 即标题是 feat / fix / perf、末尾带 (#PR 号) 的合并提交，满 3 个就在最后一位加 1。
# 基准之后数据库结构变了(schema.sql 或 schemaVersion)不自动发：技能库不迁移旧数据库，新版打不开用户的数据库，要人来决定怎么发。
# 没有任何正式版 tag 时报错退出。
set -euo pipefail

threshold=3
store=internal/library/internal/store

latest="$(git tag --list 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n 1 || true)"
if [[ -z "$latest" ]]; then
  echo "没有 vX.Y.Z 形式的正式版 tag,无法确定基准版本" >&2
  exit 1
fi

pattern='^(feat|fix|perf)(\([^)]*\))?!?: .+ \(#[0-9]+\)$'
count="$(git log --first-parent --format=%s "$latest..HEAD" | grep -cE "$pattern" || true)"

if (( count < threshold )); then
  echo "基准 $latest 之后计数的 PR $count 个，还差 $((threshold - count)) 个，这次不发版" >&2
  exit 0
fi

if ! git diff --quiet "$latest" HEAD -- "$store/schema.sql" ||
  git diff "$latest" HEAD -- "$store/schema.go" | grep -qE '^[-+][[:space:]]*schemaVersion[[:space:]]*='; then
  echo "基准 $latest 之后计数的 PR $count 个，但数据库结构变了($store 的 schema.sql 或 schemaVersion),不自动发版;这一版要手动推 tag 发布" >&2
  exit 0
fi

IFS=. read -r major minor patch <<<"${latest#v}"
next="$major.$minor.$((patch + 1))"
echo "基准 $latest 之后计数的 PR $count 个，满 $threshold 个，测试通过后发 v$next" >&2
echo "$next"
