// 组：勾选的技能里加入、移出哪些，一并卸载会删哪些文件夹，逐个加入组之后怎么汇报，集合加入组时加入哪些技能。
import { describe, expect, it } from "vitest";
import {
  AgentCode,
  ErrorCode,
  GroupMemberOutcome,
  InstallPlanStatus,
  ScopeKind,
  SkillScopeStatus,
  SourceType,
  type Agent,
  type Collection,
  type Group,
  type GroupInstall,
  type GroupMemberResult,
  type InstallPlan,
  type Scope,
  type Skill,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import { messages } from "@/lib/i18n";
import type { LibraryError, Snapshot } from "@/lib/library";
import { addReadsTwice, addedToGroup, groupOnly, inGroup, missingFromGroup, notInGroup } from "./group-rules";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const t = messages().actions;

const place = (skill_id: number, scope: Scope, extra: Partial<SkillScopeInspection> = {}): SkillScopeInspection => ({
  directory: "",
  skill_id,
  scope,
  folder: `/home/.${scope.agent}/skills/s${skill_id}`,
  direct: false,
  status: SkillScopeStatus.SkillScopeCurrent,
  ...extra,
});

// 两个 Agent 的目录各是各的，不合成一处。
const agentsWithOwnDirectories = [
  { code: claude, name: "Claude Code", global_directories: ["/home/.claude/skills"], project_directories: [".claude/skills"] } as Agent,
  { code: codex, name: "Codex", global_directories: ["/home/.codex/skills"], project_directories: [".codex/skills"] } as Agent,
];

const snapshot = (s: Partial<Snapshot> = {}) =>
  ({
    skills: [1, 2, 3].map((id) => ({ id, name: `s${id}` }) as Skill),
    groups: [{ id: 5, name: "g5", members: [1, 2] } as Group],
    groupInstalls: [],
    skillScopes: [],
    agents: agentsWithOwnDirectories,
    projects: [],
    info: { home_dir: "/home" },
    ...s,
  }) as unknown as Snapshot;

describe("勾选的技能里加入、移出哪些", () => {
  it("加入时只加还不在组里的，移出时只移在组里的;组不在了时都不在组里", () => {
    const s = snapshot();
    expect(notInGroup(s, 5, [1, 3])).toEqual([3]);
    expect(inGroup(s, 5, [1, 3])).toEqual([1]);
    expect(notInGroup(s, 9, [1])).toEqual([1]);
    expect(inGroup(s, 9, [1])).toEqual([]);
  });
});

describe("选一并卸载时删哪些文件夹", () => {
  it("只算组装到的作用域里、只靠这个组留着的：直接安装的、还有别的组安装的不删", () => {
    const s = snapshot({
      groupInstalls: [{ group_id: 5, scope: global(claude) } as GroupInstall],
      skillScopes: [
        place(1, global(claude), { groups: [5] }),
        place(2, global(claude), { groups: [5], direct: true }),
        place(3, global(claude), { groups: [5, 6] }),
        place(1, global(codex), { groups: [5] }),
      ],
    });
    expect(groupOnly(s, 5, [1, 2, 3]).map((p) => `${p.skill_id}@${p.scope.agent}`)).toEqual([`1@${claude}`]);
    expect(groupOnly(snapshot({ skillScopes: s.skillScopes }), 5, [1])).toEqual([]);
  });
});

describe("逐个加入组之后", () => {
  const s = snapshot();
  const result = (skill_id: number, outcome: GroupMemberOutcome) => ({ skill_id, scope: global(claude), outcome }) as GroupMemberResult;

  it("都加入了、没有作用域被跳过时只提示一句", () => {
    const r = addedToGroup(s, [1, 3], [
      { value: [result(1, GroupMemberOutcome.MemberInstalled)] },
      { value: [] },
    ]);
    expect(r).toMatchObject({ all: true, skipped: false, added: 2 });
    expect(r.placed.map((x) => x.skill_id)).toEqual([1]);
  });

  it("有失败、有作用域被跳过时逐个列出：失败的写原因，没有组安装的写已加入", () => {
    const r = addedToGroup(s, [1, 2, 3], [
      { error: { code: ErrorCode.Permission, message: "x" } as LibraryError },
      { value: [result(2, GroupMemberOutcome.MemberSkipped)] },
      { value: [] },
    ]);
    expect(r).toMatchObject({ all: false, skipped: true, added: 2, placed: [] });
    expect(r.rows.map((x) => [x.label, x.outcome])).toEqual([
      ["s1", t.failed],
      ["s2 → Claude Code · 全局", t.memberSkipped],
      ["s3", t.memberAdded],
    ]);
  });
});

describe("missingFromGroup", () => {
  const collection = (id: number, label: string, skills: number[] = []) => ({ id, skills, source: { type: SourceType.SourceGitHub, label } }) as Collection;
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

describe("加入组之前的「会读到两份」", () => {
  const agents = [
    { code: claude, name: "Claude Code", global_directories: ["/home/.claude/skills"], project_directories: [".claude/skills"] },
    { code: codex, name: "Codex", global_directories: ["/home/.agents/skills"], project_directories: [".agents/skills"] },
    { code: "cursor", name: "Cursor", global_directories: ["/home/.agents/skills", "/home/.cursor/skills"], project_directories: [".agents/skills"] },
  ] as Agent[];
  const s = { agents, agentsShown: {}, projects: [], skillScopes: [] } as unknown as Snapshot;
  const plan = (status: InstallPlanStatus, reads_twice: Scope[] = []) => ({ name: "foo", status, folder: "/home/.agents/skills/foo", reads_twice }) as InstallPlan;
  it("组安装到的各处里会装上、又有别的 Agent 会读到两份的写出来，写明是哪一处;被占用装不上的不说", () => {
    expect(
      addReadsTwice(s, [
        { scope: global(codex), plan: plan(InstallPlanStatus.InstallReady, [global("cursor" as AgentCode)]) },
        { scope: global(claude), plan: plan(InstallPlanStatus.InstallReady) },
        { scope: global(codex), plan: plan(InstallPlanStatus.InstallOccupied, [global("cursor" as AgentCode)]) },
      ]),
    ).toEqual(["Codex · 全局：Cursor 也读这个目录，已经在自己别的目录装着 foo，装上后会读到两份"]);
  });
});
