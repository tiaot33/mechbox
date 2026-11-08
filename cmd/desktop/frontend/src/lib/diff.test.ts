import { describe, expect, it } from "vitest";
import { changeTree, changedFiles, inlineChanges, lineDiff, numberLines, splitRows, unfold, visibleFiles } from "./diff";

describe("changedFiles", () => {
  it("SKILL.md 排第一，不论它是修改、新增还是删除;其余按修改、新增、删除排", () => {
    expect(changedFiles({ changed: ["scripts/x.sh"], added: ["SKILL.md", "a.md"], deleted: ["old.md"] })).toEqual([
      { kind: "added", path: "SKILL.md" },
      { kind: "changed", path: "scripts/x.sh" },
      { kind: "added", path: "a.md" },
      { kind: "deleted", path: "old.md" },
    ]);
  });

  it("没有差异时为空", () => {
    expect(changedFiles(null)).toEqual([]);
  });
});

describe("splitRows", () => {
  it("没变的行两边并排，改掉的行删掉的在左、新增的在右", () => {
    expect(splitRows(lineDiff("a\nb\nc\n", "a\nB\nc\n"))).toEqual([
      { kind: "pair", left: { kind: "same", text: "a" }, right: { kind: "same", text: "a" } },
      { kind: "pair", left: { kind: "del", text: "b" }, right: { kind: "add", text: "B" } },
      { kind: "pair", left: { kind: "same", text: "c" }, right: { kind: "same", text: "c" } },
    ]);
  });

  it("一段改动里删掉和新增的行数不同时，多出来的一侧对面留空", () => {
    expect(splitRows(lineDiff("a\nb\n", "x\ny\nz\n"))).toEqual([
      { kind: "pair", left: { kind: "del", text: "a" }, right: { kind: "add", text: "x" } },
      { kind: "pair", left: { kind: "del", text: "b" }, right: { kind: "add", text: "y" } },
      { kind: "pair", left: undefined, right: { kind: "add", text: "z" } },
    ]);
  });

  it("新增的文件只有右边，删掉的文件只有左边", () => {
    expect(splitRows(lineDiff("", "x\n"))).toEqual([{ kind: "pair", left: undefined, right: { kind: "add", text: "x" } }]);
    expect(splitRows(lineDiff("x\n", ""))).toEqual([{ kind: "pair", left: { kind: "del", text: "x" }, right: undefined }]);
  });

  it("折起来的没变段落占整行", () => {
    const before = Array.from({ length: 20 }, (_, i) => `l${i}`).join("\n") + "\n";
    const after = before.replace("l10\n", "L10\n");
    const rows = splitRows(lineDiff(before, after));
    expect(rows[0]).toEqual({ kind: "skip", count: 7 });
    expect(rows.at(-1)).toEqual({ kind: "skip", count: 6 });
  });
});

describe("numberLines", () => {
  it("没变的行两边都有行号，删掉的只有旧行号，新增的只有新行号，折起来的行也算进去", () => {
    const before = Array.from({ length: 10 }, (_, i) => `l${i}`).join("\n") + "\n";
    const after = before.replace("l8\n", "L8\nextra\n");
    const lines = numberLines(lineDiff(before, after));
    expect(lines[0]).toEqual({ kind: "skip", count: 5, oldLine: 1, newLine: 1 });
    expect(lines[1]).toEqual({ kind: "same", text: "l5", oldLine: 6, newLine: 6 });
    expect(lines.filter((l) => l.kind === "del")).toEqual([{ kind: "del", text: "l8", oldLine: 9 }]);
    expect(lines.filter((l) => l.kind === "add")).toEqual([
      { kind: "add", text: "L8", newLine: 9 },
      { kind: "add", text: "extra", newLine: 10 },
    ]);
    expect(lines.at(-1)).toEqual({ kind: "same", text: "l9", oldLine: 10, newLine: 11 });
  });

  it("左右对比照样带着行号", () => {
    const rows = splitRows(numberLines(lineDiff("a\nb\n", "a\nB\n")));
    expect(rows[1]).toEqual({ kind: "pair", left: { kind: "del", text: "b", oldLine: 2 }, right: { kind: "add", text: "B", newLine: 2 } });
  });
});

describe("inlineChanges", () => {
  const changes = (before: string, after: string) => {
    const lines = lineDiff(before, after);
    const marks = inlineChanges(lines);
    return lines.map((l) => marks.get(l));
  };

  it("改掉的一行和替换它的一行标出各自改了的词，相邻几个词连成一块", () => {
    const [del, add] = changes("the quick brown fox jumps\n", "the slow red fox jumps\n");
    expect(del).toEqual([
      { text: "the ", changed: false },
      { text: "quick brown", changed: true },
      { text: " fox jumps", changed: false },
    ]);
    expect(add).toEqual([
      { text: "the ", changed: false },
      { text: "slow red", changed: true },
      { text: " fox jumps", changed: false },
    ]);
  });

  it("标出来的块两头不带空格", () => {
    const [, add] = changes("a visual identity that could be\n", "a distinct visual identity that is\n");
    expect(add).toEqual([
      { text: "a ", changed: false },
      { text: "distinct", changed: true },
      { text: " visual identity that ", changed: false },
      { text: "is", changed: true },
    ]);
  });

  it("汉字一个字一段", () => {
    const [, add] = changes("装到全局目录\n", "装到项目目录\n");
    expect(add).toEqual([
      { text: "装到", changed: false },
      { text: "项目", changed: true },
      { text: "目录", changed: false },
    ]);
  });

  it("一段改动里删掉的和新增的按先后配对，多出来的行不标词", () => {
    const marks = changes("x = 1\ny = 2\n", "x = 10\ny = 20\nz = 30\n");
    expect(marks.filter(Boolean)).toHaveLength(4);
    expect(marks.at(-1)).toBeUndefined();
  });

  it("两行大半都不一样时不标词，整行已经标着改了", () => {
    expect(changes("alpha beta gamma\n", "one two three\n")).toEqual([undefined, undefined]);
  });
});

describe("unfold", () => {
  it("展开的那段折起来的行换成没变的行，带两边的行号;没展开的照旧", () => {
    const before = Array.from({ length: 20 }, (_, i) => `l${i}`).join("\n") + "\n";
    const after = before.replace("l10\n", "L10\nextra\n");
    const lines = numberLines(lineDiff(before, after));
    const head = lines[0];
    const out = unfold(lines, before, new Set([head]));
    expect(out.slice(0, 7)).toEqual(Array.from({ length: 7 }, (_, k) => ({ kind: "same", text: `l${k}`, oldLine: k + 1, newLine: k + 1 })));
    expect(out.at(-1)).toEqual({ kind: "skip", count: 6, oldLine: 15, newLine: 16 });
  });
});

describe("changeTree", () => {
  const files = [
    { kind: "changed" as const, path: "SKILL.md" },
    { kind: "added" as const, path: "scripts/run.sh" },
    { kind: "deleted" as const, path: "references/old.md" },
    { kind: "changed" as const, path: "references/guide.md" },
    { kind: "added" as const, path: "agents/openai/config.yaml" },
  ];

  it("目录在前、文件在后，各自按名字排;只有一个子目录的连续几层合成一项", () => {
    const tree = changeTree(files);
    expect(tree.map((n) => n.name)).toEqual(["agents/openai", "references", "scripts", "SKILL.md"]);
    expect(tree[0]).toEqual({
      kind: "dir",
      name: "agents/openai",
      path: "agents/openai",
      children: [{ kind: "file", name: "config.yaml", path: "agents/openai/config.yaml", change: "added" }],
    });
    expect(tree[1].kind === "dir" && tree[1].children.map((c) => c.name)).toEqual(["guide.md", "old.md"]);
  });

  it("看得见的文件按显示的先后，折起来的目录里的不算", () => {
    const tree = changeTree(files);
    expect(visibleFiles(tree, new Set())).toEqual([
      "agents/openai/config.yaml",
      "references/guide.md",
      "references/old.md",
      "scripts/run.sh",
      "SKILL.md",
    ]);
    expect(visibleFiles(tree, new Set(["references"]))).toEqual(["agents/openai/config.yaml", "scripts/run.sh", "SKILL.md"]);
  });
});
