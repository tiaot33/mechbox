// 集合与组的先后：用户拖出来的顺序优先，没排过的按默认顺序接在后面;集合加入组时加入哪些技能。
import { describe, expect, it } from "vitest";
import { SourceType, type Collection, type Group } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { missingFromGroup, moved, noOrder, prunedOrder, sortedCollections, sortedGroups } from "./model";

const collection = (id: number, label: string, skills: number[] = []) => ({ id, skills, source: { type: SourceType.SourceGitHub, label } }) as Collection;
const group = (id: number, members: number[] = []) => ({ id, name: `g${id}`, members }) as Group;
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
    expect(sortedCollections(s, []).map((c) => c.id)).toEqual([2, 3, 1]);
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
    expect(sortedGroups(s, []).map((g) => g.id)).toEqual([1, 2, 3]);
    expect(sortedGroups(s, [3]).map((g) => g.id)).toEqual([3, 1, 2]);
  });
});

describe("prunedOrder", () => {
  const s = snapshot({ collections: [collection(1, "a/a")], groups: [group(2), group(3)] });

  it("去掉已经不在库里的集合与组，复用 ID 的新组不会占到旧组的位置", () => {
    expect(prunedOrder(s, { collections: [1, 5], groups: [4, 3, 2] })).toEqual({ collections: [1], groups: [3, 2] });
  });

  it("没有要去掉的时返回同一个对象", () => {
    const order = { collections: [1], groups: [3] };
    expect(prunedOrder(s, order)).toBe(order);
    expect(prunedOrder(s, noOrder)).toBe(noOrder);
  });
});

describe("missingFromGroup", () => {
  const s = snapshot({ collections: [collection(1, "a/a", [10, 11, 12]), collection(2, "b/b")], groups: [group(7, [11])] });

  it("是集合里还不在组里的技能", () => {
    expect(missingFromGroup(s, 1, 7)).toEqual([10, 12]);
  });

  it("集合为空或都已在组里时为空", () => {
    expect(missingFromGroup(s, 2, 7)).toEqual([]);
    expect(missingFromGroup(snapshot({ collections: [collection(1, "a/a", [11])], groups: [group(7, [11])] }), 1, 7)).toEqual([]);
  });
});
