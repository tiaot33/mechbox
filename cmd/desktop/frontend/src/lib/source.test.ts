// 来源与集合：左栏折叠技能少的来源、恢复默认顺序，集合加入组时加入哪些技能，技能行里的来源短名，更新页与更新计划里集合叫什么。
import { describe, expect, it } from "vitest";
import { SourceType, type Collection, type Group, type Scope, type Skill, type SkillScopeInspection, type UnmanagedSkill } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { collectionName, foldCollections, isDefaultOrder, missingFromGroup, recordedSources, sourceShort, sourceWithRef } from "./source";

const collection = (id: number, label: string, skills: number[] = []) => ({ id, skills, source: { type: SourceType.SourceGitHub, label } }) as Collection;
const snapshot = (s: Partial<Snapshot>) =>
  ({ collections: [], groups: [], skills: [], skillScopes: [], unmanaged: [], agents: [], projects: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;

describe("isDefaultOrder", () => {
  it("ID 升序(首次入库、新建的先后)是默认顺序", () => {
    expect(isDefaultOrder([])).toBe(true);
    expect(isDefaultOrder([1, 2, 5])).toBe(true);
  });

  it("拖过之后不是", () => {
    expect(isDefaultOrder([2, 1, 5])).toBe(false);
  });
});

describe("missingFromGroup", () => {
  const group = (id: number, members: number[] = []) => ({ id, name: `g${id}`, members }) as Group;
  const s = snapshot({ collections: [collection(1, "a/a", [10, 11, 12]), collection(2, "b/b")], groups: [group(7, [11])] });

  it("是集合里还不在组里的技能", () => {
    expect(missingFromGroup(s, 1, 7)).toEqual([10, 12]);
  });

  it("集合为空或都已在组里时为空", () => {
    expect(missingFromGroup(s, 2, 7)).toEqual([]);
    expect(missingFromGroup(snapshot({ collections: [collection(1, "a/a", [11])], groups: [group(7, [11])] }), 1, 7)).toEqual([]);
  });
});

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

describe("collectionName", () => {
  const skill = (id: number, name: string) => ({ id, name }) as Skill;
  const s = snapshot({
    collections: [collection(1, "matt/skills", [10, 11]), collection(2, "team/kit", [20])],
    skills: [skill(10, "pdf"), skill(11, "docx"), skill(20, "review")],
  });

  it("只有一个技能的集合用技能名称呼，否则是来源名", () => {
    expect(collectionName(s, 1)).toBe("matt/skills");
    expect(collectionName(s, 2)).toBe("review");
  });
});

describe("recordedSources", () => {
  const claude = { kind: "global", agent: "claude-code" } as Scope;
  const codex = { kind: "global", agent: "codex" } as Scope;
  const project = { kind: "project", agent: "claude-code", project_id: 1 } as Scope;
  const sourceless = { id: 7, name: "pdf", source: { type: SourceType.$zero, label: "" } } as Skill;
  const installed = (scope: Scope, input?: string, ref?: string) => ({ skill_id: 7, scope, source_input: input, source_ref: ref }) as SkillScopeInspection;
  const shown = (scope: Scope, input?: string, ref?: string) => ({ skill_id: 7, scope, name: "pdf", source_input: input, source_ref: ref }) as UnmanagedSkill;

  it("装着的和显示在技能下面的都算，同一个来源连同分支合成一条", () => {
    const s = {
      skillScopes: [installed(claude), installed(codex, "owner/repo", "main"), { ...installed(claude, "other/repo"), skill_id: 8 }],
      unmanaged: [shown(project, "owner/repo", "main"), shown(project, "owner/repo")],
    };
    expect(recordedSources(s, sourceless)).toEqual([
      { input: "owner/repo", ref: "main", places: [codex, project] },
      { input: "owner/repo", ref: undefined, places: [project] },
    ]);
  });

  it("技能有来源时没有", () => {
    const s = { skillScopes: [installed(codex, "owner/repo")], unmanaged: [] };
    expect(recordedSources(s, { ...sourceless, source: { type: SourceType.SourceGitHub, label: "owner/repo" } } as Skill)).toEqual([]);
  });
});

describe("sourceWithRef", () => {
  it("有分支时接在链接后面，没有时只写链接", () => {
    expect(sourceWithRef("owner/repo", "main")).toBe("owner/repo · main");
    expect(sourceWithRef("owner/repo")).toBe("owner/repo");
  });
});
