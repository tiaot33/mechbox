#!/usr/bin/env bash
# 判断 HEAD 该不该自动发一版。该发时在 stdout 输出新版本号(如 0.0.5),不该发时什么都不输出;判断经过写在 stderr。
# 基准是按版本号排最高的正式版 tag(vX.Y.Z,不含预发布);从它往后数 main 上用户能感觉到的 PR,
# 即标题是 feat / fix / perf、末尾带 (#PR 号) 的合并提交，满 3 个就在最后一位加 1。
# 基准里已发布的数据库升级被改动或删除时不自动发(见 check-migrations.sh):用户的数据库已经跑过它们，改了会让结构分叉;
# 只改 schema.sql 并新增升级照常发，升级是否正确由之后的 go test 检查。
# 没有任何正式版 tag 时报错退出。
set -euo pipefail

threshold=3

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

if ! migrations="$("$(dirname "$0")/check-migrations.sh" 2>&1)"; then
  echo "$migrations" >&2
  echo "基准 $latest 之后计数的 PR $count 个，但已发布的数据库升级被改动了，不自动发版;改回去后下一次推送重新判断" >&2
  exit 0
fi

IFS=. read -r major minor patch <<<"${latest#v}"
next="$major.$minor.$((patch + 1))"
echo "基准 $latest 之后计数的 PR $count 个，满 $threshold 个，测试通过后发 v$next" >&2
echo "$next"
