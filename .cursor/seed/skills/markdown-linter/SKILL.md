---
name: markdown-linter
description: Check Markdown files for common style issues like missing top-level headings, tabs, and trailing whitespace. Use when reviewing docs. Ships a Python checker, a rules reference, and a report template.
---

# Markdown Linter

较完整形态的 skill 固件:含 `scripts/`(可执行 Python)、`references/` 与 `assets/` 三类惯例子目录,
用于验证 `Install` 对多层嵌套目录与多文件的处理。

## When to use

审阅 Markdown 文档、想快速定位常见排版问题时。

## Steps

1. 运行 `scripts/lint.py <file.md>` 得到问题清单。
2. 对照 `references/rules.md` 理解每条规则。
3. 需要汇报时套用 `assets/report-template.md`。
