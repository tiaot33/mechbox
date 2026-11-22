// 来源与集合：左栏折叠技能少的来源，技能行里的来源短名。
import { describe, expect, it } from "vitest";
import { SourceType, type Collection } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { foldCollections, sourceShort } from "./source";

const collection = (id: number, label: string, skills: number[] = []) => ({ id, skills, source: { type: SourceType.SourceGitHub, label } }) as Collection;
const snapshot = (s: Partial<Snapshot>) =>
  ({ collections: [], groups: [], skills: [], skillScopes: [], unmanaged: [], agents: [], projects: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;

describe("foldCollections", () => {
  const collections = [collection(1, "a/a", [10]), collection(2, "b/b", [11, 12, 13]), collection(3, "c/c", []), collection(4, "d/d", [14, 15])];
  const ids = (list: Collection[]) => list.map((c) => c.id);

  it("开着时技能少于 foldBelow 个的折起来，两边都保持原来的先后", () => {
    const { shown, folded } = foldCollections({ collections, foldCollections: true, foldBelow: 2 });
    expect(ids(shown)).toEqual([2, 4]);
    expect(ids(folded)).toEqual([1, 3]);
    expect(ids(foldCollections({ collections, foldCollections: true, foldBelow: 3 }).folded)).toEqual([1, 3, 4]);
  });

  it("关着时全部照常列出", () => {
    const { shown, folded } = foldCollections({ collections, foldCollections: false, foldBelow: 3 });
    expect(ids(shown)).toEqual([1, 2, 3, 4]);
    expect(folded).toEqual([]);
  });

  it("没有一个集合够数时不折，免得只剩一行「另外 K 个」", () => {
    const { shown, folded } = foldCollections({ collections, foldCollections: true, foldBelow: 4 });
    expect(ids(shown)).toEqual([1, 2, 3, 4]);
    expect(folded).toEqual([]);
  });
});

describe("sourceShort", () => {
  it("本地目录来源写目录，和左栏、节标题是同一个名字", () => {
    const s = snapshot({ info: { home_dir: "/home/me" } as Snapshot["info"] });
    expect(sourceShort(s, { type: SourceType.SourceLocal, label: "/home/me/code/my-skills" })).toBe("~/code/my-skills");
  });
});
