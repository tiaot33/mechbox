#!/usr/bin/env bash
# Cloud Agent 的仓库引导脚本：幂等地准备 Go 1.26 工具链并预热依赖/构建缓存。
# 该库为 CGO 模块（go-sqlite3），需要本机 C 编译器与 Go 1.26（见 go.mod / README）。
set -euo pipefail

GO_VERSION=1.26.0
GO_TARBALL="go${GO_VERSION}.linux-amd64.tar.gz"
GO_ROOT=/usr/local/go

# 默认镜像自带 Go 1.22，而 go.mod 要求 Go 1.26；缺失时才下载安装，保证幂等。
if ! /usr/local/bin/go version 2>/dev/null | grep -q "go${GO_VERSION} "; then
  echo "Installing Go ${GO_VERSION}..."
  curl -fsSL -o "/tmp/${GO_TARBALL}" "https://go.dev/dl/${GO_TARBALL}"
  sudo rm -rf "${GO_ROOT}"
  sudo tar -C /usr/local -xzf "/tmp/${GO_TARBALL}"
  sudo ln -sf "${GO_ROOT}/bin/go" /usr/local/bin/go
  sudo ln -sf "${GO_ROOT}/bin/gofmt" /usr/local/bin/gofmt
  rm -f "/tmp/${GO_TARBALL}"
fi

go version

# CGO 必开：go-sqlite3 依赖本机 C 编译器。
export CGO_ENABLED=1

# 拉取并校验模块依赖，随后预热构建缓存，减少后续 test/vet 的首次耗时。
go mod download
go mod verify
go build ./...

# 部署测试用种子固件(本地 skills + 项目文件夹)到可写沙盒，方便后续手动测试。
"$(dirname "${BASH_SOURCE[0]}")/seed-sandbox.sh"

echo "Cloud Agent environment ready."
