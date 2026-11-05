import { describe, expect, it } from "vitest";
import { changedFiles, lineDiff, splitRows } from "./diff";

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
