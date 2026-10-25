// 集合与组的先后：用户拖出来的顺序优先，没排过的按默认顺序接在后面。
import { describe, expect, it } from "vitest";
import { SourceType, type Collection, type Group } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { moved, sortedCollections, sortedGroups } from "./model";

const collection = (id: number, label: string) => ({ id, source: { type: SourceType.SourceGitHub, label } }) as Collection;
const group = (id: number) => ({ id, name: `g${id}` }) as Group;
const snapshot = (s: Partial<Snapshot>) => ({ collections: [], groups: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;

describe("moved", () => {
  it("挪到目标前面或后面", () => {
    expect(moved([1, 2, 3, 4], 4, 2, false)).toEqual([1, 4, 2, 3]);
    expect(moved([1, 2, 3, 4], 1, 3, true)).toEqual([2, 3, 1, 4]);
    expect(moved([1, 2, 3, 4], 1, 4, true)).toEqual([2, 3, 4, 1]);
  });

  it("挪到自己身上、目标不在时不变", () => {
    expect(moved([1, 2, 3], 2, 2, true)).toEqual([1, 2, 3]);
    expect(moved([1, 2, 3], 2, 9, false)).toEqual([1, 2, 3]);
  });
});

describe("sortedCollections", () => {
  const s = snapshot({ collections: [collection(1, "c/zeta"), collection(2, "a/alpha"), collection(3, "b/beta")] });

  it("没排过时按来源名排", () => {
    expect(sortedCollections(s).map((c) => c.id)).toEqual([2, 3, 1]);
  });

  it("排过的在前，新来的按来源名接在后面", () => {
    expect(sortedCollections(s, [1]).map((c) => c.id)).toEqual([1, 2, 3]);
    expect(sortedCollections(s, [3, 1]).map((c) => c.id)).toEqual([3, 1, 2]);
  });

  it("已经不在的集合不影响顺序", () => {
    expect(sortedCollections(s, [9, 1, 8]).map((c) => c.id)).toEqual([1, 2, 3]);
  });
});

describe("sortedGroups", () => {
  it("排过的在前，其余按技能库给的顺序", () => {
    const s = snapshot({ groups: [group(1), group(2), group(3)] });
    expect(sortedGroups(s).map((g) => g.id)).toEqual([1, 2, 3]);
    expect(sortedGroups(s, [3]).map((g) => g.id)).toEqual([3, 1, 2]);
  });
});
