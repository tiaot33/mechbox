#!/usr/bin/env bash
# 检查已发布的数据库升级没被改动：按版本号排最高的正式版 tag 里已有的升级(store/migrations/ 下 NNNNN_ 开头的文件)
# 与升级起点结构(store/testdata/NNNNN_schema.sql),在 HEAD 里必须原样还在。用户的数据库已经跑过这些升级，改了也不会再跑，
# 只会让新建的库与升级上来的库结构不同。新增升级不受限制。改动了时报错退出，并列出改动的文件。
set -euo pipefail

store=internal/library/internal/store

latest="$(git tag --list 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n 1 || true)"
if [[ -z "$latest" ]]; then
  echo "没有 vX.Y.Z 形式的正式版 tag,没有已发布的升级要检查"
  exit 0
fi

changed="$(git diff --name-status --no-renames "$latest" HEAD -- \
  ":(glob)$store/migrations/[0-9]*" ":(glob)$store/testdata/[0-9]*_schema.sql" | grep -v '^A' || true)"
if [[ -n "$changed" ]]; then
  echo "$changed"
  echo "$latest 里已发布的数据库升级被改动或删除了。要改结构就改 schema.sql,再运行 go run ./$store/migrationgen <这一步做了什么> 新增一步升级" >&2
  exit 1
fi
echo "$latest 里已发布的数据库升级都原样还在"
