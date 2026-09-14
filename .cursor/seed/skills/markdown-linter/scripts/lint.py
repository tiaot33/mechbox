#!/usr/bin/env python3
"""对单个 Markdown 文件做最小化的排版检查,打印发现的问题。"""
import sys


def lint(path: str) -> int:
    problems = []
    with open(path, encoding="utf-8") as handle:
        lines = handle.readlines()

    if not any(line.startswith("# ") for line in lines):
        problems.append("缺少一级标题 (# )")

    for number, line in enumerate(lines, start=1):
        if "\t" in line:
            problems.append(f"第 {number} 行含制表符,请改用空格")
        if line.rstrip("\n") != line.rstrip():
            problems.append(f"第 {number} 行有行尾空白")

    for problem in problems:
        print(problem)
    return 1 if problems else 0


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("用法: lint.py <file.md>", file=sys.stderr)
        raise SystemExit(2)
    raise SystemExit(lint(sys.argv[1]))
