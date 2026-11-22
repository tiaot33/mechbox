// 各页面列什么、怎样分节：列表里技能的先后，作用域页分装在这里的与没装在这里的上下两块，项目页按 Agent 分节，按住 Shift 勾选一段。
import { describe, expect, it } from "vitest";
import {
  ScopeKind,
  SourceType,
  type Agent,
  type Collection,
  type Skill,
  type SkillScopeInspection,
  type UnmanagedSkill,
} from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { pageListing, pageSkills, pickRange, projectAgents, projectSkillCount, scopeHereCount, sortSkills, type Arrange } from "./pages";

const collection = (id: number, label: string, skills: number[] = []) => ({ id, skills, source: { type: SourceType.SourceGitHub, label } }) as Collection;
const snapshot = (s: Partial<Snapshot>) =>
  ({ collections: [], groups: [], skills: [], skillScopes: [], unmanaged: [], agents: [], projects: [], info: { home_dir: "/home" }, ...s }) as unknown as Snapshot;
const skill = (id: number, name: string, label: string) => ({ id, name, source: { type: SourceType.SourceGitHub, label } }) as Skill;
const agent = (code: string, projectDir: string) => ({ code, name: code, project_directories: [projectDir], detected: true }) as Agent;
const where = (agent: string, project_id?: number) =>
  project_id ? { kind: ScopeKind.ScopeProject, agent, project_id } : { kind: ScopeKind.ScopeGlobal, agent };
const installed = (skill_id: number, agent: string, project_id?: number) => ({ skill_id, scope: where(agent, project_id) }) as SkillScopeInspection;
const unmanaged = (name: string, agent: string, project_id?: number) => ({ name, folder: name, scope: where(agent, project_id) }) as UnmanagedSkill;

describe("sortSkills", () => {
  it("来源按左栏集合的先后，不按来源名;没有来源的在最后", () => {
    const of = (id: number, name: string, collection_id: number, label: string) =>
      ({ id, name, collection_id, source: collection_id ? { type: SourceType.SourceGitHub, label } : { type: SourceType.$zero, label: "" } }) as Skill;
    const s = snapshot({ collections: [collection(2, "z/zeta"), collection(1, "a/alpha")] });
    const skills = [of(1, "a1", 1, "a/alpha"), of(2, "none", 0, ""), of(3, "z1", 2, "z/zeta")];
    expect(sortSkills(s, skills).map((x) => x.name)).toEqual(["z1", "a1", "none"]);
  });
});

// scopeListing 是作用域页要列的：经 pageListing 取。
function scopeListing(s: Snapshot, scope: string, arrange: Arrange, search: string) {
  const page = pageListing(s, { kind: "scope", scope }, arrange, search);
  if (page.kind !== "scope") throw new Error("不是作用域页");
  return page.listing;
}

describe("作用域页列什么", () => {
  const s = snapshot({
    agents: [agent("claude-code", ".claude/skills")],
    projects: [{ id: 1, name: "proj", path: "/home/proj" }] as Snapshot["projects"],
    collections: [collection(1, "a/a", [10, 11]), collection(2, "b/b", [20]), collection(3, "c/c", [30, 31])],
    skills: [skill(10, "a1", "a/a"), skill(11, "a2", "a/a"), skill(20, "b1", "b/b"), skill(30, "c1", "c/c"), skill(31, "c2", "c/c")],
    skillScopes: [installed(30, "claude-code", 1), installed(20, "claude-code", 1), installed(10, "claude-code")],
  });

  const ids = (x: { id: number }[]) => x.map((k) => k.id);

  it("分成上下两块：装在这里的与没装在这里的各自按集合分节，来自全局的不在两块里", () => {
    const l = scopeListing(s, "claude-code@1", "collection", "");
    expect(ids(l.hereSections)).toEqual([2, 3]);
    expect(l.hereSections.map((x) => ids(x.skills))).toEqual([[20], [30]]);
    expect(ids(l.global)).toEqual([10]);
    expect(ids(l.notHereSections)).toEqual([1, 3]);
    expect(l.notHereSkills.map((x) => x.name)).toEqual(["a2", "c2"]);
    expect(l.hereCount).toBe(2);
  });

  it("同一个集合在两块里各是一节，身份不同", () => {
    const l = scopeListing(s, "claude-code@1", "collection", "");
    const [here, notHere] = [l.hereSections.find((x) => x.id === 3)!, l.notHereSections.find((x) => x.id === 3)!];
    expect(here.key).not.toBe(notHere.key);
  });

  it("全局作用域没有来自全局的", () => {
    const l = scopeListing(s, "claude-code@0", "collection", "");
    expect(ids(l.hereSections)).toEqual([1]);
    expect(ids(l.notHereSections)).toEqual([1, 2, 3]);
    expect(l.global).toEqual([]);
  });

  it("和库里某个技能一样的未纳管文件夹就是那个技能：列在上面库里的技能里，不进不在技能库里", () => {
    const copy = { ...unmanaged("a2", "claude-code", 1), skill_id: 11, version_id: 1 };
    const other = unmanaged("mine", "claude-code", 1);
    const l = scopeListing({ ...s, unmanaged: [copy, other] }, "claude-code@1", "collection", "");
    expect(l.hereSections.map((x) => ids(x.skills))).toEqual([[11], [20], [30]]);
    expect(l.hereUnmanaged.map((u) => u.name)).toEqual(["mine"]);
    expect(l.notHereSkills.map((x) => x.name)).toEqual(["c2"]);
    expect(l.hereCount).toBe(3);
  });

  it("全局装着的技能多出来的那一份不在来自全局里另列一行", () => {
    const extra = { ...unmanaged("a1-copy", "claude-code"), skill_id: 10, version_id: 1 };
    const l = scopeListing({ ...s, unmanaged: [extra, unmanaged("g", "claude-code")] }, "claude-code@1", "collection", "");
    expect(ids(l.global)).toEqual([10]);
    expect(l.globalUnmanaged.map((u) => u.name)).toEqual(["g"]);
  });

  it("右栏和标题的数字：这个目录里的都算(不是 app 装的也算),来自全局的不算", () => {
    const copy = { ...unmanaged("a2", "claude-code", 1), skill_id: 11, version_id: 1 };
    const withFolders = { ...s, unmanaged: [copy, unmanaged("mine", "claude-code", 1), unmanaged("g", "claude-code")] };
    expect(scopeHereCount(withFolders, "claude-code@1")).toBe(4);
    expect(scopeHereCount(withFolders, "claude-code@0")).toBe(2);
    // 右栏收起时顶栏读 pageListing 的 total:和右栏、摘要标题同一个数，来自全局的另算在 global 里。
    const page = pageListing(withFolders, { kind: "scope", scope: "claude-code@1" }, "collection", "");
    expect(page.kind === "scope" && [page.total, page.global]).toEqual([4, 2]);
  });

  it("搜索时两块都只留匹配的", () => {
    const l = scopeListing(s, "claude-code@1", "collection", "2");
    expect(l.hereSections).toEqual([]);
    expect(ids(l.notHereSections)).toEqual([1, 3]);
    expect(l.hereCount).toBe(0);
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

  it("和库里某个技能一样的未纳管文件夹算作那个技能，不另算一个", () => {
    const copy = { ...unmanaged("beta", "claude-code", 1), skill_id: 12, version_id: 1 };
    const agents = projectAgents({ ...s, unmanaged: [...s.unmanaged, copy] }, 1, "");
    expect(agents[0].skills.map((x) => x.name)).toEqual(["beta"]);
    expect(agents[0].unmanaged.map((u) => u.name)).toEqual(["mine"]);
    expect(agents[0].all).toBe(2);
  });

  it("右栏和项目页标题的数字：几个 Agent 都装了的只算一个，不是 app 装的也算", () => {
    expect(projectSkillCount(s, 1)).toBe(3);
    expect(projectSkillCount(s, 2)).toBe(1);
  });

  it("搜索时只留有结果的节，all 不随搜索变", () => {
    const agents = projectAgents(s, 1, "zet");
    expect(agents.map((a) => a.scope)).toEqual(["codex@1"]);
    expect(agents[0].skills.map((x) => x.name)).toEqual(["zeta"]);
    expect(agents[0].all).toBe(2);
  });
});

describe("pickRange", () => {
  const row = (section: string, id: number) => ({ key: `${section}|${id}`, id });
  // 项目页：Claude Code 一节是 1-4,Codex 一节是 1-6。
  const rows = [1, 2, 3, 4].map((id) => row("cc", id)).concat([1, 2, 3, 4, 5, 6].map((id) => row("codex", id)));

  it("是两行之间(含两头)的技能，不分先后", () => {
    expect(pickRange(rows, "cc|2", "cc|4")).toEqual([2, 3, 4]);
    expect(pickRange(rows, "codex|6", "codex|5")).toEqual([5, 6]);
  });

  it("同一个技能在几节里出现时按点的那一行算，不跨到别的节", () => {
    expect(pickRange(rows, "codex|6", "codex|2")).toEqual([2, 3, 4, 5, 6]);
  });

  it("跨节时两节之间的行都算，结果不重复", () => {
    expect(pickRange(rows, "cc|3", "codex|2")).toEqual([3, 4, 1, 2]);
  });

  it("有一行已经不在列表里时为空", () => {
    expect(pickRange(rows, "gone|7", "cc|2")).toEqual([]);
  });
});

describe("pageSkills", () => {
  const s = snapshot({
    agents: [agent("claude-code", ".claude/skills")],
    projects: [{ id: 1, name: "proj", path: "/home/proj" }] as Snapshot["projects"],
    skills: [skill(10, "a", "a/a"), skill(11, "b", "a/a")],
    skillScopes: [installed(10, "claude-code", 1), installed(11, "claude-code")],
  });

  it("项目页只列装在这个项目里的，其余页是库里的全部", () => {
    expect([...pageSkills(s, { kind: "project", project: 1 })]).toEqual([10]);
    expect([...pageSkills(s, { kind: "scope", scope: "claude-code@1" })].sort()).toEqual([10, 11]);
  });
});
