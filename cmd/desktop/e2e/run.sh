#!/bin/sh
# 桌面端端到端验证：把桌面端编译成 Wails server 模式(HTTP 服务，不开窗口),
# 在临时 HOME 里放好测试数据，用本机 Chrome 按界面操作走遍技能库的全部功能并核对磁盘。
# 用法(在 cmd/desktop/e2e 下):pnpm install && pnpm test [步骤名过滤]
# 需要联网的步骤(技能市场、GitHub)默认跳过，E2E_NETWORK=1 时运行。
set -eu
here=$(cd "$(dirname "$0")" && pwd)
desktop=$(dirname "$here")
work=$(mktemp -d /tmp/skills-e2e.XXXXXX)
server_pid=""
zip_pid=""
cleanup() {
	[ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null || true
	[ -n "$zip_pid" ] && kill "$zip_pid" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT INT TERM

echo "构建前端与 server 模式二进制…"
(cd "$desktop/frontend" && pnpm build >/dev/null)
(cd "$desktop" && go build -tags server,production -o "$work/server" . 2>/dev/null)

# 测试数据：一个目录里的三个本地技能(alpha 带一个可执行脚本)、两个项目目录、供 URL 入库的两个 zip。
home="$work/home"
mkdir -p "$home/code/proj-a" "$home/code/proj-b"
# 内置 Agent 的配置目录：有它们才算检测到，默认显示在选择安装位置的地方。
mkdir -p "$home/.claude" "$home/.codex"
for name in alpha beta gamma; do
	mkdir -p "$home/code/my-skills/$name"
	printf -- '---\nname: %s\ndescription: "%s skill: does %s things."\n---\n\n# %s\n\n- step one\n- step `two`\n' \
		"$name" "$name" "$name" "$name" >"$home/code/my-skills/$name/SKILL.md"
done
mkdir -p "$home/code/my-skills/alpha/scripts"
printf '#!/bin/sh\necho hi\n' >"$home/code/my-skills/alpha/scripts/run.sh"
chmod +x "$home/code/my-skills/alpha/scripts/run.sh"
mkdir -p "$work/zips/delta"
printf -- '---\nname: delta\ndescription: "delta skill from a zip"\n---\n\n# delta\n' >"$work/zips/delta/SKILL.md"
(cd "$work/zips" && zip -qr delta.zip delta)
# 两个技能的 zip:测从技能发起更新(只勾这一个)与集合重新对齐到同一个来源版本。
for name in eta theta; do
	mkdir -p "$work/zips/pair/$name"
	printf -- '---\nname: %s\ndescription: "%s v1"\n---\n\n# %s\n' "$name" "$name" "$name" >"$work/zips/pair/$name/SKILL.md"
done
(cd "$work/zips/pair" && zip -qr ../pair.zip eta theta)
# 三个技能的 zip:测更新计划里的风险、跳过、改名、来源里已删除与新技能。
for name in kappa lambda mu; do
	mkdir -p "$work/zips/trio/$name"
	printf -- '---\nname: %s\ndescription: "%s v1"\n---\n\n# %s\n\nUse it well.\n' "$name" "$name" "$name" >"$work/zips/trio/$name/SKILL.md"
done
(cd "$work/zips/trio" && zip -qr ../trio.zip kappa lambda mu)
# well-known 站点:/site/ 下放旧格式的索引，测站点链接的识别。
mkdir -p "$work/zips/site/.well-known/skills/omega"
printf -- '---\nname: omega\ndescription: "omega from a well-known index"\n---\n\n# omega\n' >"$work/zips/site/.well-known/skills/omega/SKILL.md"
printf '{"skills":[{"name":"omega","description":"omega from a well-known index","files":["SKILL.md"]}]}\n' >"$work/zips/site/.well-known/skills/index.json"

# Git 仓库：tag v1.0 只有 git-tagged;main(默认分支)再加 skills/git-main 与 .claude/skills/git-other;release/v2 再加 skills/git-v2。
# 临时 HOME 的 .gitconfig 用 insteadOf 把 https://git.e2e.test/org/repo.git 指到本地裸仓库，测分支下拉与入库预览。
repo="$work/git/src"
mkdir -p "$repo"
gitc() { git -C "$repo" -c user.name=e2e -c user.email=e2e@example.com -c commit.gpgsign=false "$@"; }
skill() {
	mkdir -p "$repo/$1/$2"
	printf -- '---\nname: %s\ndescription: "%s from git"\n---\n\n# %s\n' "$2" "$2" "$2" >"$repo/$1/$2/SKILL.md"
}
gitc init -q -b main
skill skills git-tagged
gitc add -A && gitc commit -qm v1 && gitc tag v1.0
skill skills git-main
skill .claude/skills git-other
gitc add -A && gitc commit -qm main
gitc checkout -qb release/v2
skill skills git-v2
gitc add -A && gitc commit -qm v2 && gitc checkout -q main
git clone -q --bare "$repo" "$work/git/repo.git"
git config --file "$home/.gitconfig" "url.file://$work/git/repo.git.insteadOf" https://git.e2e.test/org/repo.git

# 第一次打开：各 Agent 的全局目录里早就有不是库装的技能。first-pdf 是 npx skills 的布局(真实文件夹在 Codex 全局，
# Claude Code 全局里是指过去的符号链接);first-dup 两处同名、内容不一样，Codex 的那份较新。
mkdir -p "$home/.agents/skills/first-pdf" "$home/.agents/skills/first-dup" "$home/.claude/skills/first-notes" "$home/.claude/skills/first-dup"
first() { printf -- '---\nname: %s\ndescription: "%s"\n---\n' "$1" "$2" >"$3/SKILL.md"; }
first first-pdf "read pdfs" "$home/.agents/skills/first-pdf"
ln -s ../../.agents/skills/first-pdf "$home/.claude/skills/first-pdf"
first first-notes "my notes" "$home/.claude/skills/first-notes"
first first-dup "the old copy" "$home/.claude/skills/first-dup"
first first-dup "the new copy" "$home/.agents/skills/first-dup"
touch -t 202401010000 "$home/.claude/skills/first-dup/SKILL.md"

port=${E2E_PORT:-18080}
zip_port=$((port + 1))
(cd "$work/zips" && exec python3 -m http.server "$zip_port" >/dev/null 2>&1) &
zip_pid=$!
HOME="$home" XDG_STATE_HOME="" WAILS_SERVER_PORT="$port" "$work/server" >"$work/server.log" 2>&1 &
server_pid=$!
for _ in 1 2 3 4 5 6 7 8 9 10; do
	curl -sf -o /dev/null "http://localhost:$port/" && break
	sleep 0.5
done

E2E_HOME="$home" E2E_URL="http://localhost:$port/" E2E_ZIP_URL="http://localhost:$zip_port/delta.zip" E2E_ZIP_DIR="$work/zips" \
	E2E_SHOTS="${E2E_SHOTS:-/tmp/skills-e2e-shots}" node "$here/e2e.mjs" "$@"
