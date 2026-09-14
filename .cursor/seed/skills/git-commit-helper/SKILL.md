---
name: git-commit-helper
description: Draft Conventional Commits messages from a staged diff. Use when the user wants a well-formed commit subject and body. Ships an executable helper script plus a reference cheatsheet.
---

# Git Commit Helper

多文件 skill 固件:含一个**可执行脚本**与一个 `references/` 参考文档,
用于验证 `Install` 能保留可执行权限位与嵌套相对路径。

## When to use

需要按 Conventional Commits 规范生成提交信息时。

## Steps

1. 阅读 `references/conventional-commits.md` 了解类型前缀。
2. 运行 `scripts/prepare-commit.sh` 生成信息骨架。
3. 补全正文后提交。
