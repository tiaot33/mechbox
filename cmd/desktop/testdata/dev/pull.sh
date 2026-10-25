#!/bin/sh
# 从 skills.sh 上的两个热门仓库导出开发数据(devseed)用的技能快照，写到本目录的 git/ 下，提交进仓库;之后生成开发数据不联网。
# 只在要换素材时运行。每个快照是来源仓库在一个提交上的部分技能目录(原样，带各自的 LICENSE.txt,都是 Apache 2.0)。
# 不克隆整个仓库：每个提交只浅拉取这一个提交、不带文件内容，稀疏检出时只下载要的几个目录。
# devseed 按顺序把同一个仓库的快照提交成本地 Git 仓库，先按第一个快照入库，后面的快照造出更新:
#   anthropics/skills:89dcaa3 → dbd4588,frontend-design 改动、claude-academy-guide 改名为 academy-guide、新技能 discernment-nudge。
#   openai/skills:e6afb0d → 30444ae(openai-docs 改动并新增脚本，入库后先更新到这一版，留下更新历史)
#                → b0401f0(openai-docs 再改动、sora 与 doc 在来源里删除、新技能 define-goal)。
set -eu
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d /tmp/mechbox-pull.XXXXXX)
trap 'rm -rf "$work"' EXIT INT TERM

snapshot() { # 仓库 完整提交号 路径...
	repo=$1 commit=$2
	shift 2
	clone="$work/$(basename "$repo")-$commit"
	target="$here/git/$(echo "$repo" | tr / -)/$(echo "$commit" | cut -c1-7)"
	git init -q "$clone"
	git -C "$clone" fetch -q --depth 1 --filter=blob:none "https://github.com/$repo.git" "$commit"
	git -C "$clone" sparse-checkout set --no-cone "$@"
	git -C "$clone" checkout -q FETCH_HEAD
	rm -rf "$target" && mkdir -p "$target"
	for path in "$@"; do
		if [ -e "$clone/$path" ]; then
			mkdir -p "$target/$(dirname "$path")"
			cp -R "$clone/$path" "$target/$path"
		fi
	done
}

anthropics="skills/frontend-design skills/brand-guidelines skills/internal-comms skills/webapp-testing skills/claude-academy-guide skills/academy-guide skills/discernment-nudge"
for commit in 89dcaa3a283f79ed84fd8fe53e2208b9442a6427 dbd4588f9e1033efb41dad4bef2f7947c8993d44; do
	# shellcheck disable=SC2086
	snapshot anthropics/skills $commit $anthropics
done

openai="skills/.curated/openai-docs skills/.curated/gh-fix-ci skills/.curated/playwright skills/.curated/screenshot skills/.curated/sora skills/.curated/doc skills/.curated/define-goal"
for commit in e6afb0d74cc75d220df2faf3dd6c635c2dc6a108 30444aed500c00c85294d12074f6e3ee794f808a b0401f07213a66414d84a65cb50c1d226f99485a; do
	# shellcheck disable=SC2086
	snapshot openai/skills $commit $openai
done
