// 集合与组的先后：拖到同类的前后，恢复默认顺序，以及把旧版拖出来的先后换成交给技能库的完整先后。
import { describe, expect, it } from "vitest";
import { SourceType, type Collection, type Group } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { isDefaultOrder, legacyOrder, moved } from "./order-rules";

const collection = (id: number, label: string, skills: number[] = []) => ({ id, skills, source: { type: SourceType.SourceGitHub, label } }) as Collection;
const group = (id: number, members: number[] = []) => ({ id, name: `g${id}`, members }) as Group;
const snapshot = (s: Partial<Snapshot>) =>
  ({ collections: [], groups: [], skills: [], skillScopes: [], unmanaged: [], agents: [], projects: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;

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

describe("isDefaultOrder", () => {
  it("ID 升序(首次入库、新建的先后)是默认顺序", () => {
    expect(isDefaultOrder([])).toBe(true);
    expect(isDefaultOrder([1, 2, 5])).toBe(true);
  });

  it("拖过之后不是", () => {
    expect(isDefaultOrder([2, 1, 5])).toBe(false);
  });
});

describe("legacyOrder", () => {
  // 集合 1、2、3 按首次入库排，来源名是 zeta、alpha、beta。
  const s = snapshot({ collections: [collection(1, "c/zeta"), collection(2, "a/alpha"), collection(3, "b/beta")], groups: [group(1), group(2), group(3)] });

  it("拖过的在前，其余集合按来源名、组按 ID 接在后面", () => {
    expect(legacyOrder(s, { collections: [3], groups: [3] })).toEqual({ collections: [3, 2, 1], groups: [3, 1, 2] });
  });

  it("没拖过、拖过的都已不在库里时那一类不写", () => {
    expect(legacyOrder(s, { collections: [], groups: [9] })).toEqual({ collections: undefined, groups: undefined });
  });

  it("库里已经不是默认顺序(升级后又拖过)时不覆盖", () => {
    const moved = snapshot({ collections: [collection(2, "a/alpha"), collection(1, "c/zeta")], groups: [group(2), group(1)] });
    expect(legacyOrder(moved, { collections: [1], groups: [1] })).toEqual({ collections: undefined, groups: undefined });
  });
});
