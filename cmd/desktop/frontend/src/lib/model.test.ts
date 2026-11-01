// 集合与组的先后：用户拖出来的顺序优先，没排过的按默认顺序接在后面;集合加入组时加入哪些技能;照着另一个 Agent、另一个项目装时装哪些;
// 作用域页分装在这里的与没装在这里的上下两块;项目页按 Agent 分节;按住 Shift 勾选一段。
import { describe, expect, it } from "vitest";
import {
  AgentCode,
  ScopeKind,
  SkillScopeStatus,
  SourceType,
  type Agent,
  type Collection,
  type Group,
  type GroupInstall,
  type RegisteredProject,
  type Scope,
  type Skill,
  type SkillScopeInspection,
  type UnmanagedSkill,
} from "@bindings/internal/library";
import type { Snapshot } from "./library";
import {
  copyFromAgent,
  copyFromProject,
  copyProjectSources,
  copySources,
  copyTarget,
  homePath,
  missingFromGroup,
  moved,
  noOrder,
  pageSkills,
  pickRange,
  placeOf,
  projectAgents,
  prunedOrder,
  scopeListing,
  sortedCollections,
  sortedGroups,
  uncopiedProjectUnmanaged,
  uncopiedUnmanaged,
} from "./model";

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

  const ids = (x: { id: number }[]) => x.map((k) => k.id);

  it("分成上下两块：装在这里的与没装在这里的各自按集合分节，来自全局的不在两块里", () => {
    const l = scopeListing(s, "claude-code@1", "collection", "", noOrder);
    expect(ids(l.hereSections)).toEqual([2, 3]);
    expect(l.hereSections.map((x) => ids(x.skills))).toEqual([[20], [30]]);
    expect(ids(l.global)).toEqual([10]);
    expect(ids(l.notHereSections)).toEqual([1, 3]);
    expect(l.notHereSkills.map((x) => x.name)).toEqual(["a2", "c2"]);
    expect(l.hereCount).toBe(2);
  });

  it("同一个集合在两块里各是一节，身份不同", () => {
    const l = scopeListing(s, "claude-code@1", "collection", "", noOrder);
    const [here, notHere] = [l.hereSections.find((x) => x.id === 3)!, l.notHereSections.find((x) => x.id === 3)!];
    expect(here.key).not.toBe(notHere.key);
  });

  it("全局作用域没有来自全局的", () => {
    const l = scopeListing(s, "claude-code@0", "collection", "", noOrder);
    expect(ids(l.hereSections)).toEqual([1]);
    expect(ids(l.notHereSections)).toEqual([1, 2, 3]);
    expect(l.global).toEqual([]);
  });

  it("搜索时两块都只留匹配的", () => {
    const l = scopeListing(s, "claude-code@1", "collection", "2", noOrder);
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

describe("照着装", () => {
  const claude = AgentCode.AgentClaudeCode;
  const codex = AgentCode.AgentCodex;
  const cursor = "cursor" as AgentCode;
  const agents = [
    { code: claude, name: "Claude Code", global_directory: "/home/.claude/skills", project_directory: ".claude/skills" },
    { code: codex, name: "Codex", global_directory: "/home/.agents/skills", project_directory: ".agents/skills" },
    { code: cursor, name: "Cursor", global_directory: "/home/.cursor/skills", project_directory: ".agents/skills" },
  ] as Agent[];
  const at = (agent: AgentCode, project_id = 0): Scope =>
    project_id ? { kind: ScopeKind.ScopeProject, agent, project_id } : { kind: ScopeKind.ScopeGlobal, agent };
  const installed = (skill_id: number, scope: Scope, extra: Partial<SkillScopeInspection> = {}) =>
    ({ skill_id, scope, folder: `f${skill_id}`, direct: true, status: SkillScopeStatus.SkillScopeCurrent, ...extra }) as SkillScopeInspection;
  const skill = (id: number) => ({ id, name: `s${id}`, source: { type: SourceType.SourceGitHub, label: "a/a" } }) as Skill;
  // 项目 1、3 在这台机器上，项目 2 不在。
  const projects = [{ id: 1, name: "web" }, { id: 2, name: "old", missing: true }, { id: 3, name: "new" }] as RegisteredProject[];
  const base = {
    agents,
    projects,
    skills: [1, 2, 3, 4, 5].map(skill),
    groups: [group(7, [4, 5])],
    groupInstalls: [] as GroupInstall[],
    unmanaged: [] as UnmanagedSkill[],
  };

  describe("照着另一个 Agent 装", () => {
    it("全局的装到全局，项目里的装到同一个项目;直接安装照样直接安装，组安装照样组安装", () => {
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(1, at(claude)),
          installed(4, at(claude), { direct: false, groups: [7] }),
          installed(5, at(claude), { direct: false, groups: [7] }),
          installed(2, at(claude, 1)),
        ],
        groupInstalls: [{ group_id: 7, scope: at(claude) }] as GroupInstall[],
      });
      const copy = copyFromAgent(s, claude, codex);
      expect(copy.map((p) => placeOf(p.to))).toEqual([0, 1]);
      expect(copy[0].skills.map((x) => x.id)).toEqual([1]);
      expect(copy[0].groups.map((g) => g.id)).toEqual([7]);
      expect(copy[1].skills.map((x) => x.id)).toEqual([2]);
      expect(copy[1].groups).toEqual([]);
    });

    it("只加不减：这里已经装了的技能、已有的组安装不列出", () => {
      const s = snapshot({
        ...base,
        skillScopes: [installed(1, at(claude)), installed(2, at(claude)), installed(1, at(codex)), installed(3, at(codex))],
        groupInstalls: [{ group_id: 7, scope: at(claude) }, { group_id: 7, scope: at(codex) }] as GroupInstall[],
      });
      const copy = copyFromAgent(s, claude, codex);
      expect(copy).toHaveLength(1);
      expect(copy[0].skills.map((x) => x.id)).toEqual([2]);
      expect(copy[0].groups).toEqual([]);
    });

    it("读同一个目录的那一处本来就一样，不列出", () => {
      const s = snapshot({ ...base, skillScopes: [installed(1, at(codex, 1)), installed(2, at(codex))] });
      const copy = copyFromAgent(s, codex, cursor);
      expect(copy.map((p) => placeOf(p.to))).toEqual([0]);
    });

    it("组安装记在读同一个目录的另一个 Agent 上也算", () => {
      // Codex 与 Cursor 在项目里共用 .agents/skills,组安装记在 Codex 上;照着 Cursor 装到 Claude Code 时照样带上这个组。
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(4, at(codex, 1), { direct: false, groups: [7] }),
          installed(4, at(cursor, 1), { direct: false, groups: [7] }),
        ],
        groupInstalls: [{ group_id: 7, scope: at(codex, 1) }] as GroupInstall[],
      });
      expect(copyFromAgent(s, cursor, claude)).toMatchObject([{ to: { project_id: 1 }, groups: [{ id: 7 }], skills: [] }]);
      // 反过来照着 Claude Code 装到 Cursor:Cursor 那一处已经有这个组安装(记在 Codex 上),不再列出。
      const back = snapshot({
        ...base,
        skillScopes: [installed(4, at(claude, 1), { direct: false, groups: [7] })],
        groupInstalls: [{ group_id: 7, scope: at(claude, 1) }, { group_id: 7, scope: at(codex, 1) }] as GroupInstall[],
      });
      expect(copyFromAgent(back, claude, cursor)).toEqual([]);
    });

    it("项目里装到共用目录的代表 Agent 上，全局装到它自己", () => {
      expect(copyTarget({ agents, agentsShown: {} }, cursor, 1)).toBe(codex);
      expect(copyTarget({ agents, agentsShown: {} }, cursor, 0)).toBe(cursor);
    });

    it("要装过来的技能里，原来那一处被改过的单列出来", () => {
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(1, at(claude), { status: SkillScopeStatus.SkillScopeModified }),
          installed(4, at(claude), { direct: false, groups: [7], status: SkillScopeStatus.SkillScopeModified }),
          installed(2, at(claude), { status: SkillScopeStatus.SkillScopeModified }),
          installed(2, at(codex)),
        ],
        groupInstalls: [{ group_id: 7, scope: at(claude) }] as GroupInstall[],
      });
      expect(copyFromAgent(s, claude, codex)[0].edited.map((x) => x.id)).toEqual([1, 4]);
    });

    it("项目不在这台机器上的那一处标出来", () => {
      const s = snapshot({ ...base, skillScopes: [installed(1, at(claude, 2), { status: SkillScopeStatus.SkillScopeRootMissing })] });
      expect(copyFromAgent(s, claude, codex)).toMatchObject([{ to: { project_id: 2 }, missing: true }]);
    });

    it("可以照着装的是有东西可装的其他 Agent,装得最多的在前", () => {
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(1, at(claude)),
          installed(4, at(claude)),
          installed(2, at(cursor)),
          installed(3, at(cursor)),
          installed(5, at(cursor)),
          installed(1, at(codex)),
        ],
      });
      expect(copySources(s, codex).map((a) => a.code)).toEqual([cursor, claude]);
      expect(copySources(snapshot({ ...base, skillScopes: [installed(1, at(claude)), installed(1, at(codex))] }), codex)).toEqual([]);
      const onlyMissing = snapshot({ ...base, skillScopes: [installed(1, at(claude, 2), { status: SkillScopeStatus.SkillScopeRootMissing })] });
      expect(copySources(onlyMissing, codex)).toEqual([]);
    });

    it("未纳管技能带不过来，数出来;读同一个目录的那几处不算", () => {
      const s = snapshot({
        ...base,
        skillScopes: [],
        unmanaged: [{ scope: at(codex), name: "a" }, { scope: at(codex, 1), name: "b" }, { scope: at(claude), name: "c" }] as UnmanagedSkill[],
      });
      expect(uncopiedUnmanaged(s, codex, claude)).toHaveLength(2);
      expect(uncopiedUnmanaged(s, codex, cursor)).toHaveLength(1);
    });

    it("这一处已经有同名文件夹的未纳管技能不算带不过来", () => {
      const s = snapshot({
        ...base,
        skillScopes: [installed(1, at(claude), { folder: "/home/.claude/skills/b" })],
        unmanaged: [
          { scope: at(codex), name: "a" },
          { scope: at(claude), name: "a" },
          { scope: at(codex), name: "b" },
          { scope: at(codex), name: "c" },
        ] as UnmanagedSkill[],
      });
      expect(uncopiedUnmanaged(s, codex, claude).map((u) => u.name)).toEqual(["c"]);
    });
  });

  describe("照着另一个项目装", () => {
    it("各 Agent 装到这个项目里的同一个 Agent;直接安装照样直接安装，组安装照样组安装，全局不算", () => {
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(1, at(claude, 1)),
          installed(4, at(codex, 1), { direct: false, groups: [7] }),
          installed(5, at(codex, 1), { direct: false, groups: [7] }),
          installed(3, at(claude)),
        ],
        groupInstalls: [{ group_id: 7, scope: at(codex, 1) }] as GroupInstall[],
      });
      const copy = copyFromProject(s, 1, 3);
      expect(copy.map((p) => p.to)).toEqual([at(claude, 3), at(codex, 3)]);
      expect(copy[0].skills.map((x) => x.id)).toEqual([1]);
      expect(copy[0].groups).toEqual([]);
      expect(copy[1].skills).toEqual([]);
      expect(copy[1].groups.map((g) => g.id)).toEqual([7]);
    });

    it("项目里共用目录的几个 Agent 合成一处，组安装记在其中哪一个上都算", () => {
      const s = snapshot({
        ...base,
        skillScopes: [installed(2, at(codex, 1)), installed(2, at(cursor, 1)), installed(4, at(cursor, 1), { direct: false, groups: [7] })],
        groupInstalls: [{ group_id: 7, scope: at(cursor, 1) }] as GroupInstall[],
      });
      expect(copyFromProject(s, 1, 3)).toMatchObject([{ to: at(codex, 3), skills: [{ id: 2 }], groups: [{ id: 7 }] }]);
    });

    it("只加不减：这个项目里已经装了的技能、已有的组安装不列出", () => {
      const s = snapshot({
        ...base,
        skillScopes: [installed(1, at(claude, 1)), installed(2, at(claude, 1)), installed(1, at(claude, 3)), installed(3, at(claude, 3))],
        groupInstalls: [{ group_id: 7, scope: at(codex, 1) }, { group_id: 7, scope: at(cursor, 3) }] as GroupInstall[],
      });
      expect(copyFromProject(s, 1, 3)).toMatchObject([{ to: at(claude, 3), skills: [{ id: 2 }], groups: [] }]);
      expect(copyFromProject(s, 3, 3)).toEqual([]);
    });

    it("要装过来的技能里，原来那个项目里被改过的单列出来", () => {
      const s = snapshot({
        ...base,
        skillScopes: [installed(1, at(claude, 1), { status: SkillScopeStatus.SkillScopeModified }), installed(2, at(claude, 1))],
      });
      expect(copyFromProject(s, 1, 3)[0].edited.map((x) => x.id)).toEqual([1]);
    });

    it("可以照着装的是有东西可装的其他项目，装得最多的在前;不在这台机器上的项目也能照着", () => {
      const s = snapshot({
        ...base,
        skillScopes: [
          installed(1, at(claude, 1)),
          installed(2, at(claude, 2), { status: SkillScopeStatus.SkillScopeRootMissing }),
          installed(3, at(claude, 2), { status: SkillScopeStatus.SkillScopeRootMissing }),
          installed(4, at(claude, 3)),
          installed(5, at(claude)),
        ],
      });
      expect(copyProjectSources(s, 3).map((p) => p.id)).toEqual([2, 1]);
      const done = snapshot({ ...base, skillScopes: [installed(1, at(claude, 1)), installed(1, at(claude, 3))] });
      expect(copyProjectSources(done, 3)).toEqual([]);
    });

    it("装得多少按技能算：共用目录的几个 Agent 的同一个技能只算一次", () => {
      // 项目 1 在 .agents/skills 里装了 1 个(Codex、Cursor 各列一条),项目 2 的 Claude Code 装了 2 个。
      const s = snapshot({
        ...base,
        skillScopes: [installed(1, at(codex, 1)), installed(1, at(cursor, 1)), installed(2, at(claude, 2)), installed(3, at(claude, 2))],
      });
      expect(copyProjectSources(s, 3).map((p) => p.id)).toEqual([2, 1]);
    });

    it("只有空的组安装、没装库里技能的项目不能照着装", () => {
      const s = snapshot({ ...base, groups: [group(8)], skillScopes: [], groupInstalls: [{ group_id: 8, scope: at(claude, 1) }] as GroupInstall[] });
      expect(copyProjectSources(s, 3)).toEqual([]);
    });

    it("未纳管技能按作用域页的口径数：归项目仓库管的单独算，指向 Agent 目录以外的不算，这里已有同名文件夹的不算", () => {
      const s = snapshot({
        ...base,
        skillScopes: [installed(1, at(claude, 3), { folder: "/p/new/.claude/skills/taken" })],
        unmanaged: [
          { scope: at(claude, 1), name: "mine" },
          { scope: at(codex, 1), name: "also-mine" },
          { scope: at(claude, 1), name: "team", in_repository: true },
          { scope: at(claude, 1), name: "locked", in_project_lock: true },
          { scope: at(claude, 1), name: "author", link_outside: true },
          { scope: at(claude, 1), name: "author-locked", link_outside: true, in_project_lock: true },
          { scope: at(claude, 1), name: "taken" },
          { scope: at(codex, 1), name: "there" },
          { scope: at(codex, 3), name: "there" },
          { scope: at(claude), name: "global" },
        ] as UnmanagedSkill[],
      });
      const left = uncopiedProjectUnmanaged(s, 1, 3);
      expect(left.unmanaged.map((u) => u.name)).toEqual(["mine", "also-mine"]);
      expect(left.repo.map((u) => u.name)).toEqual(["team", "locked"]);
    });
  });
});
