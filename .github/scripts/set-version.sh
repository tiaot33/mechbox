#!/usr/bin/env bash
# 把发布版本写进桌面端的各处构建配置。用法:set-version.sh 1.2.3[-beta.1]
# config.yml(关于页读它)与 nfpm 用完整版本;Info.plist、Windows 版本资源与 NSIS 只认数字，去掉预发布后缀。
set -euo pipefail

version="${1#v}"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "版本号不是 X.Y.Z 或 X.Y.Z-后缀: $1" >&2
  exit 1
fi
numeric="${version%%-*}"

build="$(cd "$(dirname "$0")/../../cmd/desktop/build" && pwd)"

V="$version" perl -pi -e 's/^(\s+version:\s*)"[^"]*"/$1"$ENV{V}"/' "$build/config.yml"
V="$version" perl -pi -e 's/^(version:\s*)"[^"]*"/$1"$ENV{V}"/' "$build/linux/nfpm/nfpm.yaml"
V="$numeric" perl -0pi -e 's#(<key>CFBundle(?:ShortVersionString|Version)</key>\s*<string>)[^<]*#$1$ENV{V}#g' "$build/darwin/Info.plist"
V="$numeric" perl -pi -e 's/("(?:file_version|ProductVersion)":\s*)"[^"]*"/$1"$ENV{V}"/' "$build/windows/info.json"
V="$numeric" perl -pi -e 's/(!define INFO_PRODUCTVERSION\s+)"[^"]*"/$1"$ENV{V}"/' "$build/windows/nsis/wails_tools.nsh"

git -C "$build" diff --stat -- .
