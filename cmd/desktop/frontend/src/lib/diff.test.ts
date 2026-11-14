import { describe, expect, it } from "vitest";
import { FileMode, type File } from "@bindings/internal/library";
import { changeTree, changedFiles, changedWords, lineDiff, modeChange, splitRows, stepFile, treeFiles, unfold } from "./diff";

describe("changedFiles", () => {
  it("SKILL.md 排第一，不论它是修改、新增还是删除;其余按修改、新增、删除排", () => {
    expect(
      changedFiles({
        changed: ["scripts/x.sh"],
        added: ["SKILL.md", "a.md"],
        deleted: ["old.md"],
      }),
    ).toEqual([
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
  it("没变的行两边并排(同一个对象),改掉的行删掉的在左、新增的在右，都带行号", () => {
    const rows = splitRows(lineDiff("a\nb\nc\n", "a\nB\nc\n"));
    const a = { kind: "same", text: "a", oldLine: 1, newLine: 1 };
    const c = { kind: "same", text: "c", oldLine: 3, newLine: 3 };
    expect(rows).toEqual([
      { kind: "pair", left: a, right: a },
      {
        kind: "pair",
        left: { kind: "del", text: "b", oldLine: 2 },
        right: { kind: "add", text: "B", newLine: 2 },
      },
      { kind: "pair", left: c, right: c },
    ]);
    expect(rows[0].kind === "pair" && rows[0].left === rows[0].right).toBe(true);
  });

  it("一段改动里删掉和新增的行数不同时，多出来的一侧对面留空", () => {
    expect(splitRows(lineDiff("a\nb\n", "x\ny\nz\n"))).toEqual([
      {
        kind: "pair",
        left: { kind: "del", text: "a", oldLine: 1 },
        right: { kind: "add", text: "x", newLine: 1 },
      },
      {
        kind: "pair",
        left: { kind: "del", text: "b", oldLine: 2 },
        right: { kind: "add", text: "y", newLine: 2 },
      },
      {
        kind: "pair",
        left: undefined,
        right: { kind: "add", text: "z", newLine: 3 },
      },
    ]);
  });

  it("新增的文件只有右边，删掉的文件只有左边", () => {
    expect(splitRows(lineDiff("", "x\n"))).toEqual([
      {
        kind: "pair",
        left: undefined,
        right: { kind: "add", text: "x", newLine: 1 },
      },
    ]);
    expect(splitRows(lineDiff("x\n", ""))).toEqual([
      {
        kind: "pair",
        left: { kind: "del", text: "x", oldLine: 1 },
        right: undefined,
      },
    ]);
  });

  it("折起来的没变段落占整行", () => {
    const before = Array.from({ length: 20 }, (_, i) => `l${i}`).join("\n") + "\n";
    const after = before.replace("l10\n", "L10\n");
    const rows = splitRows(lineDiff(before, after));
    expect(rows[0]).toEqual({ kind: "skip", count: 7, oldLine: 1, newLine: 1 });
    expect(rows.at(-1)).toEqual({
      kind: "skip",
      count: 6,
      oldLine: 15,
      newLine: 15,
    });
  });
});

describe("lineDiff", () => {
  it("没变的行两边都有行号，删掉的只有旧行号，新增的只有新行号，折起来的行也算进去", () => {
    const before = Array.from({ length: 10 }, (_, i) => `l${i}`).join("\n") + "\n";
    const after = before.replace("l8\n", "L8\nextra\n");
    const lines = lineDiff(before, after);
    expect(lines[0]).toEqual({
      kind: "skip",
      count: 5,
      oldLine: 1,
      newLine: 1,
    });
    expect(lines[1]).toEqual({
      kind: "same",
      text: "l5",
      oldLine: 6,
      newLine: 6,
    });
    expect(lines.filter((l) => l.kind === "del")).toEqual([{ kind: "del", text: "l8", oldLine: 9 }]);
    expect(lines.filter((l) => l.kind === "add")).toEqual([
      { kind: "add", text: "L8", newLine: 9 },
      { kind: "add", text: "extra", newLine: 10 },
    ]);
    expect(lines.at(-1)).toEqual({
      kind: "same",
      text: "l9",
      oldLine: 10,
      newLine: 11,
    });
  });

  it("连续没变的行不到 3 行时不折，照常显示", () => {
    // 第 1 行改了、第 9 行改了：中间第 5 行离两处改动都超过 3 行，但只有这 1 行，不折。
    const before = Array.from({ length: 9 }, (_, i) => `l${i}`).join("\n") + "\n";
    const after = before.replace("l0\n", "L0\n").replace("l8\n", "L8\n");
    expect(lineDiff(before, after).some((l) => l.kind === "skip")).toBe(false);
  });
});

describe("changedWords", () => {
  const changes = (before: string, after: string) => {
    const lines = lineDiff(before, after);
    const marks = changedWords(lines);
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

  it("很长的一段空白也很快标完，不卡住界面", () => {
    const start = performance.now();
    const [, add] = changes("keep this common prefix x y\n", `keep this common prefix u${" ".repeat(100_000)}v\n`);
    expect(performance.now() - start).toBeLessThan(1000);
    const changed = add?.filter((s) => s.changed) ?? [];
    expect(changed).toHaveLength(1);
    expect(changed[0].text.startsWith("u") && changed[0].text.endsWith("v")).toBe(true);
  });

  it("两行大半都不一样时不标词，整行已经标着改了", () => {
    expect(changes("alpha beta gamma\n", "one two three\n")).toEqual([undefined, undefined]);
  });
});

describe("unfold", () => {
  it("展开的那段折起来的行换成没变的行，带两边的行号;没展开的照旧", () => {
    const before = Array.from({ length: 20 }, (_, i) => `l${i}`).join("\n") + "\n";
    const after = before.replace("l10\n", "L10\nextra\n");
    const lines = lineDiff(before, after);
    const head = lines[0];
    const out = unfold(lines, before, new Set([head]));
    expect(out.slice(0, 7)).toEqual(
      Array.from({ length: 7 }, (_, k) => ({
        kind: "same",
        text: `l${k}`,
        oldLine: k + 1,
        newLine: k + 1,
      })),
    );
    expect(out.at(-1)).toEqual({
      kind: "skip",
      count: 6,
      oldLine: 15,
      newLine: 16,
    });
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
      children: [
        {
          kind: "file",
          name: "config.yaml",
          path: "agents/openai/config.yaml",
          change: "added",
        },
      ],
    });
    expect(tree[1].kind === "dir" && tree[1].children.map((c) => c.name)).toEqual(["guide.md", "old.md"]);
  });

  it("文件按显示的先后，折起来的目录里的不算", () => {
    const tree = changeTree(files);
    expect(treeFiles(tree)).toEqual([
      "agents/openai/config.yaml",
      "references/guide.md",
      "references/old.md",
      "scripts/run.sh",
      "SKILL.md",
    ]);
    expect(treeFiles(tree, new Set(["references"]))).toEqual(["agents/openai/config.yaml", "scripts/run.sh", "SKILL.md"]);
  });
});

describe("stepFile", () => {
  const tree = changeTree([
    { kind: "changed", path: "alpha/a.txt" },
    { kind: "changed", path: "middle/b.txt" },
    { kind: "changed", path: "middle/c.txt" },
    { kind: "changed", path: "zeta/d.txt" },
  ]);

  it("上下键换到前后看得见的文件，到头了不动", () => {
    expect(stepFile(tree, new Set(), "middle/b.txt", 1)).toBe("middle/c.txt");
    expect(stepFile(tree, new Set(), "middle/b.txt", -1)).toBe("alpha/a.txt");
    expect(stepFile(tree, new Set(), "zeta/d.txt", 1)).toBeUndefined();
  });

  it("跳过折起来的目录里的文件", () => {
    expect(stepFile(tree, new Set(["middle"]), "alpha/a.txt", 1)).toBe("zeta/d.txt");
  });

  it("当前文件所在的目录折起来了，照样从它的位置往前或往后找", () => {
    expect(stepFile(tree, new Set(["middle"]), "middle/b.txt", 1)).toBe("zeta/d.txt");
    expect(stepFile(tree, new Set(["middle"]), "middle/b.txt", -1)).toBe("alpha/a.txt");
  });
});

describe("modeChange", () => {
  const file = (mode: FileMode): File => ({ path: "scripts/run.sh", content: "IyEvYmluL3NoCg==", mode }) as File;

  it("加上、去掉可执行权限分别标出", () => {
    expect(modeChange(file(FileMode.ModeRegular), file(FileMode.ModeExecutable))).toBe("executable-added");
    expect(modeChange(file(FileMode.ModeExecutable), file(FileMode.ModeRegular))).toBe("executable-removed");
  });

  it("没变或只有一侧时没有变化", () => {
    expect(modeChange(file(FileMode.ModeExecutable), file(FileMode.ModeExecutable))).toBeUndefined();
    expect(modeChange(undefined, file(FileMode.ModeExecutable))).toBeUndefined();
    expect(modeChange(file(FileMode.ModeExecutable), undefined)).toBeUndefined();
  });
});
