#!/bin/sh
# 桌面端端到端验证：把桌面端编译成 Wails server 模式(HTTP 服务，不开窗口),
# 在临时 HOME 里放好测试数据，用本机 Chrome 按界面操作走遍技能库的全部功能并核对磁盘。
# 用法：先在 cmd/desktop 下 pnpm install(frontend 与 e2e 同属一个 pnpm workspace),再在 cmd/desktop/e2e 下 pnpm test [步骤名过滤]
# 需要联网的步骤(技能市场、GitHub)默认跳过，E2E_NETWORK=1 时运行;离线时不访问真实的 skills.sh。
set -eu
here=$(cd "$(dirname "$0")" && pwd)
desktop=$(dirname "$here")
E2E_PLAN_ONLY=1 node "$here/e2e.mjs" "$@"
case " $* " in *" --list "*) exit 0 ;; esac
server=$(node "$here/build.mjs")
work=$(mktemp -d /tmp/mechbox-e2e.XXXXXX)
shots=${E2E_SHOTS:-$(mktemp -d /tmp/mechbox-e2e-shots.XXXXXX)}
mkdir -p "$shots"
server_pid=""
zip_pid=""
hosts_pid=""
cleanup() {
	status=$?
	trap - EXIT INT TERM
	[ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null || true
	[ -n "$zip_pid" ] && kill "$zip_pid" 2>/dev/null || true
	[ -n "$hosts_pid" ] && kill "$hosts_pid" 2>/dev/null || true
	for pid in $server_pid $zip_pid $hosts_pid; do wait "$pid" 2>/dev/null || true; done
	if [ "$status" -ne 0 ]; then
		for log in "$work"/*.log; do [ ! -f "$log" ] || cp "$log" "$shots/"; done
	fi
	rm -rf "$work"
	echo "诊断文件：$shots"
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# 测试数据在 ../testdata/e2e 里，复制到临时目录;Git 不存的部分(空目录、zip、Git 仓库、指向临时目录的 .gitconfig、修改时间)在这里补上。
# home:my-skills 下三个本地技能(alpha 带一个可执行脚本)、两个项目目录，以及第一次打开时各 Agent 全局目录里早就有的不是库装的技能:
# first-pdf 是 npx skills 的布局(真实文件夹在 Codex 全局，Claude Code 全局里是指过去的符号链接);first-dup 两处同名、内容不一样，Codex 的那份较新。
data="$desktop/testdata/e2e"
test_home="$work/home"
cp -R "$data/home" "$data/zips" "$data/hosts" "$work/"
mkdir -p "$test_home/code/proj-a" "$test_home/code/proj-b"
# 内置 Agent 的配置目录：有它们才算检测到，默认显示在选择安装位置的地方。
mkdir -p "$test_home/.claude" "$test_home/.codex"
touch -t 202401010000 "$test_home/.claude/skills/first-dup/SKILL.md"
# 供 URL 入库的 zip:delta 一个技能;pair 两个技能，测从技能发起更新(只勾这一个)与集合重新对齐到同一个来源版本;
# trio 三个技能，测更新计划里的风险、跳过、改名、来源里已删除与新技能。zips/site 是 well-known 站点，/site/ 下放旧格式的索引，测站点链接的识别。
(cd "$work/zips" && zip -qr delta.zip delta)
(cd "$work/zips/pair" && zip -qr ../pair.zip eta theta)
(cd "$work/zips/trio" && zip -qr ../trio.zip kappa lambda mu)

# Git 仓库:git/ 下每个目录是一次提交加的文件。tag v1.0 只有 git-tagged;main(默认分支)再加 skills/git-main 与 .claude/skills/git-other;
# release/v2 再加 skills/git-v2。临时 HOME 的 .gitconfig 用 insteadOf 把 https://git.e2e.test/org/repo.git 指到本地裸仓库，测分支下拉与入库预览。
repo="$work/git/src"
mkdir -p "$repo"
gitc() { git -C "$repo" -c user.name=e2e -c user.email=e2e@example.com -c commit.gpgsign=false "$@"; }
layer() { cp -R "$data/git/$1/." "$repo/"; }
gitc init -q -b main
layer v1.0
gitc add -A && gitc commit -qm v1 && gitc tag v1.0
layer main
gitc add -A && gitc commit -qm main
gitc checkout -qb release/v2
layer release-v2
gitc add -A && gitc commit -qm v2 && gitc checkout -q main
git clone -q --bare "$repo" "$work/git/repo.git"
git config --file "$test_home/.gitconfig" "url.file://$work/git/repo.git.insteadOf" https://git.e2e.test/org/repo.git

# 离线时 server 模式按 E2E_HOSTS 把 skills.sh 与 well-known 站点 skills.e2e.test 的请求改到本地的 hosts 服务，
# 不访问真实的 skills.sh:搜索不论搜什么都返回 found-a / found-b 两个结果(hosts/api/search),都来自 skills.e2e.test;
# 站点的旧格式索引里有这两个技能。e2e 在 Agent 目录里放同名的未纳管技能，测在 skills.sh 上找来源。

# 三个服务都只绑 127.0.0.1、用 127.0.0.1 访问：python 默认绑 [::],Go 的 server 绑 127.0.0.1,两者能同时占同一个端口号，
# 用 localhost 访问可能连到别的进程。没设 E2E_PORT 时一次向系统要三个空闲端口，几个 e2e 可以同时跑;设了就用它和后面两个，先确认都空闲。
free_ports() {
	node -e "const net=require('net');Promise.all([0,1,2].map(()=>new Promise(r=>{const s=net.createServer().listen(0,'127.0.0.1',()=>r(s))}))).then(ss=>{for(const s of ss){console.log(s.address().port);s.close()}})"
}
check_free() {
	node -e "require('net').createServer().on('error',()=>process.exit(1)).listen($1,'127.0.0.1',function(){this.close()})" ||
		{ echo "端口 $1 已被占用，换一个 E2E_PORT 或不设它" >&2; exit 1; }
}
if [ -n "${E2E_PORT:-}" ]; then
	port=$E2E_PORT
	zip_port=$((port + 1))
	hosts_port=$((port + 2))
	for p in "$port" "$zip_port" "$hosts_port"; do check_free "$p"; done
else
	{ read -r port; read -r zip_port; read -r hosts_port; } <<EOF
$(free_ports)
EOF
fi
# 等服务起来：要确认是本次起的进程在监听这个端口，期间它要一直活着。只看端口能不能连上，可能连到别人的服务，接着测下去还会改坏那边的数据。
wait_up() {
	for _ in $(seq 40); do
		kill -0 "$2" 2>/dev/null || break
		lsof -a -p "$2" -iTCP:"$3" -sTCP:LISTEN >/dev/null 2>&1 && return
		sleep 0.25
	done
	echo "$1 没有起来,$4:" >&2
	cat "$4" >&2
	exit 1
}
(cd "$work/zips" && exec python3 -m http.server --bind 127.0.0.1 "$zip_port" >"$work/zip.log" 2>&1) &
zip_pid=$!
wait_up "zip 服务" "$zip_pid" "$zip_port" "$work/zip.log"
hosts=""
if [ "${E2E_NETWORK:-}" != 1 ]; then
	(cd "$work/hosts" && exec python3 -m http.server --bind 127.0.0.1 "$hosts_port" >"$work/hosts.log" 2>&1) &
	hosts_pid=$!
	wait_up "hosts 服务" "$hosts_pid" "$hosts_port" "$work/hosts.log"
	hosts="skills.sh=127.0.0.1:$hosts_port skills.e2e.test=127.0.0.1:$hosts_port"
fi
# 界面语言跟随系统，server 模式按 LC_ALL / LC_MESSAGES / LANG 判断;e2e 的步骤按中文界面写，固定成中文，不随跑测试的机器变。
library="$test_home/Library/Application Support/mechbox/library.db"
HOME="$test_home" MECHBOX_LIBRARY="$library" XDG_CONFIG_HOME="$test_home/.config" XDG_STATE_HOME="$test_home/.local/state" LC_ALL=zh_CN.UTF-8 WAILS_SERVER_HOST=127.0.0.1 WAILS_SERVER_PORT="$port" E2E_HOSTS="$hosts" \
	"$server" >"$work/server.log" 2>&1 &
server_pid=$!
wait_up "server" "$server_pid" "$port" "$work/server.log"

status=0
E2E_PLAN_ONLY=0 E2E_HOME="$test_home" E2E_LIBRARY="$library" E2E_URL="http://127.0.0.1:$port/" E2E_ZIP_URL="http://127.0.0.1:$zip_port/delta.zip" E2E_ZIP_DIR="$work/zips" \
	E2E_SHOTS="$shots" node "$here/e2e.mjs" "$@" || status=$?
exit "$status"
