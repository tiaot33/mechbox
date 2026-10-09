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

port=${E2E_PORT:-18080}
zip_port=$((port + 1))
(cd "$work/zips" && exec python3 -m http.server "$zip_port" >/dev/null 2>&1) &
zip_pid=$!
HOME="$home" WAILS_SERVER_PORT="$port" "$work/server" >"$work/server.log" 2>&1 &
server_pid=$!
for _ in 1 2 3 4 5 6 7 8 9 10; do
	curl -sf -o /dev/null "http://localhost:$port/" && break
	sleep 0.5
done

E2E_HOME="$home" E2E_URL="http://localhost:$port/" E2E_ZIP_URL="http://localhost:$zip_port/delta.zip" E2E_ZIP_DIR="$work/zips" \
	E2E_SHOTS="${E2E_SHOTS:-/tmp/skills-e2e-shots}" node "$here/e2e.mjs" "$@"
