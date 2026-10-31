// 集合与组的先后：用户拖出来的顺序优先，没排过的按默认顺序接在后面;集合加入组时加入哪些技能;
// 作用域页里有技能装在这里的节排在前面;项目页按 Agent 分节;按住 Shift 勾选一段。
import { describe, expect, it } from "vitest";
import { ScopeKind, SourceType, type Agent, type Collection, type Group, type Skill, type SkillScopeInspection, type UnmanagedSkill } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { homePath, missingFromGroup, moved, noOrder, pickRange, projectAgents, prunedOrder, scopeListing, sortedCollections, sortedGroups } from "./model";

const collection = (id: number, label: string, skills: number[] = []) => ({ id, skills, source: { type: SourceType.SourceGitHub, label } }) as Collection;
const group = (id: number, members: number[] = []) => ({ id, name: `g${id}`, members }) as Group;
const snapshot = (s: Partial<Snapshot>) =>
  ({ collections: [], groups: [], skills: [], skillScopes: [], unmanaged: [], agents: [], projects: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;
const skill = (id: number, name: string, label: string) => ({ id, name, source: { type: SourceType.SourceGitHub, label } }) as Skill;
const agent = (code: string, project_directory: string) => ({ code, name: code, project_directory, detected: true }) as Agent;
const where = (agent: string, project_id?: number) =>
  project_id ? { kind: ScopeKind.ScopeProject, agent, project_id } : { kind: ScopeKind.ScopeGlobal, agent };
const installed = (skill_id: number, agent: string, project_id?: number) => ({ skill_id, scope: where(agent, project_id) }) as SkillScopeInspection;
const unmanaged = (name: string, agent: string, project_id?: number) => ({ name, folder: name, scope: where(agent, project_id) }) as UnmanagedSkill;

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

describe("homePath", () => {
  it("把主目录缩写成 ~", () => {
    expect(homePath("/Users/me", "/Users/me/.agents/skills")).toBe("~/.agents/skills");
    expect(homePath("\\\\wsl.localhost\\Ubuntu\\home\\me", "\\\\wsl.localhost\\Ubuntu\\home\\me\\.agents\\skills")).toBe("~\\.agents\\skills");
  });

  it("Windows 上的主目录不缩写", () => {
    expect(homePath("C:\\Users\\me", "C:\\Users\\me\\.agents\\skills")).toBe("C:\\Users\\me\\.agents\\skills");
  });
});

describe("scopeListing", () => {
  const s = snapshot({
    agents: [agent("claude-code", ".claude/skills")],
    projects: [{ id: 1, name: "proj", path: "/home/proj" }] as Snapshot["projects"],
    collections: [collection(1, "a/a", [10, 11]), collection(2, "b/b", [20]), collection(3, "c/c", [30, 31])],
    skills: [skill(10, "a1", "a/a"), skill(11, "a2", "a/a"), skill(20, "b1", "b/b"), skill(30, "c1", "c/c"), skill(31, "c2", "c/c")],
    skillScopes: [installed(30, "claude-code", 1), installed(20, "claude-code", 1), installed(10, "claude-code")],
  });

  it("有技能装在这里的节排在前面，先后不变", () => {
    expect(scopeListing(s, "claude-code@1", "collection", "", noOrder).sections.map((x) => x.id)).toEqual([2, 3, 1]);
    expect(scopeListing(s, "claude-code@0", "collection", "", noOrder).sections.map((x) => x.id)).toEqual([1, 2, 3]);
  });

  it("搜索时照样按装没装在这里排", () => {
    expect(scopeListing(s, "claude-code@1", "collection", "2", noOrder).sections.map((x) => x.id)).toEqual([3, 1]);
  });
});

describe("projectAgents", () => {
  const s = snapshot({
    agents: [agent("claude-code", ".claude/skills"), agent("codex", ".agents/skills"), agent("amp", ".agents/skills"), agent("cursor", ".cursor/skills")],
    projects: [{ id: 1, name: "proj", path: "/home/proj" }, { id: 2, name: "other", path: "/home/other" }] as Snapshot["projects"],
    skills: [skill(10, "zeta", "a/a"), skill(11, "alpha", "a/a"), skill(12, "beta", "b/b")],
    skillScopes: [
      installed(10, "codex", 1),
      installed(10, "amp", 1),
      installed(11, "codex", 1),
      installed(11, "amp", 1),
      installed(12, "claude-code"),
      installed(12, "cursor", 2),
    ],
    unmanaged: [unmanaged("mine", "claude-code", 1), unmanaged("global-only", "claude-code")],
  });

  it("项目里装了技能的 Agent 各一节，共用目录的合成一处，只算装在这个项目里的", () => {
    const agents = projectAgents(s, 1, "");
    expect(agents.map((a) => a.scope)).toEqual(["claude-code@1", "codex@1"]);
    expect(agents[0].skills).toEqual([]);
    expect(agents[0].unmanaged.map((u) => u.name)).toEqual(["mine"]);
    expect(agents[1].skills.map((x) => x.name)).toEqual(["alpha", "zeta"]);
    expect(agents.map((a) => a.all)).toEqual([1, 2]);
  });

  it("搜索时只留有结果的节，all 不随搜索变", () => {
    const agents = projectAgents(s, 1, "zet");
    expect(agents.map((a) => a.scope)).toEqual(["codex@1"]);
    expect(agents[0].skills.map((x) => x.name)).toEqual(["zeta"]);
    expect(agents[0].all).toBe(2);
  });
});

describe("pickRange", () => {
  it("是两行之间(含两头)的技能，不分先后", () => {
    expect(pickRange([5, 3, 8, 1, 9], 3, 1)).toEqual([3, 8, 1]);
    expect(pickRange([5, 3, 8, 1, 9], 9, 8)).toEqual([8, 1, 9]);
  });

  it("一个技能在几节里出现时按第一次出现的位置，结果不重复", () => {
    expect(pickRange([5, 3, 5, 8], 5, 8)).toEqual([5, 3, 8]);
  });

  it("上一次的那一行不在列表里时只有这一行", () => {
    expect(pickRange([5, 3, 8], 7, 3)).toEqual([3]);
  });
});
