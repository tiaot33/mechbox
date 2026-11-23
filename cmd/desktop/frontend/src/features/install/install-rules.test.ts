// 安装与卸载流程里的判断：装到哪几处、跳过与覆盖、结果怎么归并，卸载时删哪些文件夹、要不要先确认。
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
  type Group,
  type GroupInstall,
  type GroupMemberResult,
  type InstallPlan,
  type MissingMention,
  type Scope,
  type Skill,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import { messages } from "@/lib/i18n";
import type { LibraryError, Snapshot } from "@/lib/library";
import {
  asksBeforeUninstall,
  groupInstalled,
  groupInstallPlan,
  installResults,
  installSteps,
  mentionedMissing,
  notDirectIn,
  occupied,
  readInstallPlans,
  readsTwiceNotes,
  skippedPicked,
  uninstallGroupPlaces,
  uninstallPicked,
  uninstallPlace,
  type InstallItem,
} from "./install-rules";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const project = (agent: AgentCode, project_id: number): Scope => ({ kind: ScopeKind.ScopeProject, agent, project_id });
const t = messages().actions;

const skill = (id: number, extra: Partial<Skill> = {}) =>
  ({ id, name: `s${id}`, source: { type: SourceType.SourceGitHub, label: "owner/repo" }, ...extra }) as Skill;

function place(skill_id: number, scope: Scope, extra: Partial<SkillScopeInspection> = {}): SkillScopeInspection {
  const where = scope.project_id ? `/p${scope.project_id}/.${scope.agent}` : `/home/.${scope.agent}`;
  return { skill_id, scope, directory: `${where}/skills`, folder: `${where}/skills/s${skill_id}`, direct: true, status: SkillScopeStatus.SkillScopeCurrent, ...extra };
}

// 两个 Agent 的目录各是各的，不合成一处。
const agentsWithOwnDirectories = [
  { code: claude, name: "Claude Code", global_directories: ["/home/.claude/skills"], project_directories: [".claude/skills"] } as Agent,
  { code: codex, name: "Codex", global_directories: ["/home/.codex/skills"], project_directories: [".codex/skills"] } as Agent,
];

const snapshot = (s: Partial<Snapshot>) =>
  ({
    collections: [],
    groups: [],
    groupInstalls: [],
    skills: [],
    skillScopes: [],
    unmanaged: [],
    agents: agentsWithOwnDirectories,
    projects: [{ id: 7, name: "proj" }],
    info: { home_dir: "/home" },
    ...s,
  }) as unknown as Snapshot;

const plan = (status: InstallPlanStatus, extra: Partial<InstallPlan> = {}) =>
  ({ status, name: "s1", folder: "/home/.claude/skills/s1", ...extra }) as InstallPlan;

const err = (code: ErrorCode, details?: LibraryError["details"]): LibraryError => ({ code, message: code, details }) as LibraryError;

describe("装上之后会读到两份", () => {
  it("写出也读这个目录、已经在别的目录装着它的 Agent;装到几处时写出是哪一处", () => {
    const s = snapshot({});
    const twice = plan(InstallPlanStatus.InstallReady, { reads_twice: [global(codex)] });
    expect(readsTwiceNotes(s, [{ plan: twice }])).toEqual([t.readsTwice(["Codex"], "s1")]);
    expect(readsTwiceNotes(s, [{ plan: twice, label: "s1 · Claude Code · 全局" }, { plan: plan(InstallPlanStatus.InstallReady) }])).toEqual([
      `s1 · Claude Code · 全局：${t.readsTwice(["Codex"], "s1")}`,
    ]);
  });
});

describe("被占用、装不上的一处怎么说", () => {
  it("占着的是这个技能自己(另一个作用域算出同一个目录)时没有能点开的", () => {
    const s = snapshot({ skills: [skill(1)] });
    const r = occupied(s, 1, plan(InstallPlanStatus.InstallOccupied, { occupier_skill_id: 1 }));
    expect(r).toEqual({ reason: t.occupiedSelf("~/.claude/skills/s1") });
  });

  it("占着的是库里同名的另一个技能时写出它的来源，能点开它", () => {
    const s = snapshot({ skills: [skill(1), skill(2)] });
    const r = occupied(s, 1, plan(InstallPlanStatus.InstallOccupied, { occupier_skill_id: 2 }));
    expect(r.occupier).toBe(2);
    expect(r.reason).toBe(t.occupiedSource("owner/repo", "~/.claude/skills/s1"));
  });

  it("占着的是没有来源的技能时说没有来源", () => {
    const s = snapshot({ skills: [skill(1), skill(2, { source: { type: SourceType.$zero, label: "" } as Skill["source"] })] });
    expect(occupied(s, 1, plan(InstallPlanStatus.InstallOccupied, { occupier_skill_id: 2 })).reason).toBe(t.occupiedNoSource("~/.claude/skills/s1"));
  });

  it("占着的不在库里时只说另一个同名技能，不能点开", () => {
    const s = snapshot({ skills: [skill(1)] });
    expect(occupied(s, 1, plan(InstallPlanStatus.InstallOccupied, { occupier_skill_id: 9 }))).toEqual({
      reason: t.occupiedOther("~/.claude/skills/s1"),
      occupier: undefined,
    });
  });
});

describe("逐个直接安装时装哪几处", () => {
  it("已经直接安装在那里的不再装，只随组安装的照样装", () => {
    const s = snapshot({
      skillScopes: [place(1, global(claude)), place(2, global(claude), { direct: false, groups: [5] })],
    });
    const items = notDirectIn(s, [skill(1), skill(2), skill(3)], [global(claude), global(codex)]);
    expect(items.map((x) => `${x.skill.id}@${x.scope.agent}`)).toEqual([`2@${claude}`, `3@${claude}`, `1@${codex}`, `2@${codex}`, `3@${codex}`]);
  });
});

describe("装好之后提示里说哪些提到的技能找不到", () => {
  const mention = (skill_id: number, scope: Scope, name: string, mentioned_skill_id?: number): MissingMention =>
    ({ skill_id, scope, name, mentioned_skill_id }) as MissingMention;

  it("库里有、那里还没有安装记录的给按钮一起装上;已有安装记录(文件夹不在了)的不在这里装", () => {
    const s = snapshot({ skillScopes: [place(3, global(claude), { status: SkillScopeStatus.SkillScopeMissing })] });
    const r = mentionedMissing(s, [mention(1, global(claude), "s2", 2), mention(1, global(claude), "s3", 3), mention(4, global(claude), "x")]);
    expect(r.skills).toBe(2);
    expect(r.targets.map((p) => p.skill_id)).toEqual([2]);
    expect(r.count).toBe(1);
  });
});

describe("逐处安装：看过安装计划之后", () => {
  const items: InstallItem[] = [
    { skill: skill(1), scope: global(claude) },
    { skill: skill(2), scope: global(claude) },
    { skill: skill(3), scope: global(claude) },
    { skill: skill(4), scope: global(claude) },
  ];
  const s = snapshot({ skills: items.map((x) => x.skill) });
  const reads = [
    { plan: plan(InstallPlanStatus.InstallReady) },
    { plan: plan(InstallPlanStatus.InstallUntracked, { name: "s2", folder: "/home/.claude/skills/s2" }) },
    { plan: plan(InstallPlanStatus.InstallOccupied, { occupier_skill_id: 9 }) },
    { error: err(ErrorCode.Permission) },
  ];

  it("看计划就出错的算失败、写进结果;有同名文件夹的合成一次询问", () => {
    const r = readInstallPlans(s, items, reads);
    expect(r.several).toBe(false);
    expect(r.planned.map((x) => x.skill.id)).toEqual([1, 2, 3]);
    expect(r.untracked.map((x) => x.skill.id)).toEqual([2]);
    expect(r.unread).toMatchObject([{ label: "s4", outcome: t.failed, tone: "critical" }]);
  });

  it("装到几个 Agent 时每一处写出是哪个 Agent", () => {
    const r = readInstallPlans(s, [items[0], { skill: skill(1), scope: global(codex) }], [reads[0], reads[0]]);
    expect(r.several).toBe(true);
    expect(r.planned.map(r.label)).toEqual(["s1 · Claude Code · 全局", "s1 · Codex · 全局"]);
  });

  it("被占用的跳过并说明原因;同名文件夹选了跳过时跳过、选了覆盖时带 overwrite 装", () => {
    const plans = readInstallPlans(s, items, reads);
    const skip = installSteps(s, plans, false);
    expect(skip.map((x) => ("skip" in x ? "skip" : x.overwrite ? "overwrite" : "install"))).toEqual(["install", "skip", "skip"]);
    expect("skip" in skip[1] && skip[1].skip.reason).toBe(t.untrackedBody("~/.claude/skills/s2", "s2"));
    expect("skip" in skip[2] && skip[2].skip).toMatchObject({ outcome: t.memberSkipped, tone: "warn", reason: t.occupiedOther("~/.claude/skills/s1") });
    const overwrite = installSteps(s, plans, true);
    expect(overwrite.map((x) => ("skip" in x ? "skip" : x.overwrite ? "overwrite" : "install"))).toEqual(["install", "overwrite", "skip"]);
  });

  it("结果按顺序归并：看计划就出错的在前，装上的、跳过的、失败的照 steps 的先后;执行时才被占上的能点开占着的那个", () => {
    const plans = readInstallPlans(s, items, reads);
    const steps = installSteps(s, plans, true);
    const r = installResults(s, plans, steps, [{ value: undefined }, { error: err(ErrorCode.FailedPrecondition, { skill_id: 3 }) }]);
    expect(r.rows.map((x) => [x.label, x.outcome])).toEqual([
      ["s4", t.failed],
      ["s1", t.memberInstalled],
      ["s2", t.failed],
      ["s3", t.memberSkipped],
    ]);
    expect(r.rows[2].occupier).toBe(3);
    expect(r.placed).toEqual([{ skill_id: 1, scope: global(claude) }]);
    expect(r.installed).toBe(1);
    expect(r.failed).toBe(true);
  });

  it("都装上时不算失败;只有看计划出错时也算失败", () => {
    const plans = readInstallPlans(s, items.slice(0, 1), reads.slice(0, 1));
    const steps = installSteps(s, plans, false);
    expect(installResults(s, plans, steps, [{ value: undefined }])).toMatchObject({ installed: 1, failed: false });
    const unread = readInstallPlans(s, items.slice(3), reads.slice(3));
    expect(installResults(s, unread, [], []).failed).toBe(true);
  });

  it("还没执行到的不写进结果", () => {
    const plans = readInstallPlans(s, items.slice(0, 1), reads.slice(0, 1));
    expect(installResults(s, plans, installSteps(s, plans, false), []).rows).toEqual([]);
  });
});

describe("组安装", () => {
  it("有同名文件夹的成员先问，被占用、组里同名的装不上", () => {
    const r = groupInstallPlan([
      plan(InstallPlanStatus.InstallReady),
      plan(InstallPlanStatus.InstallUntracked),
      plan(InstallPlanStatus.InstallOccupied),
      plan(InstallPlanStatus.InstallDuplicate),
      plan(InstallPlanStatus.InstallAlreadyInstalled),
    ]);
    expect(r.untracked.map((m) => m.status)).toEqual([InstallPlanStatus.InstallUntracked]);
    expect(r.blocked.map((m) => m.status)).toEqual([InstallPlanStatus.InstallOccupied, InstallPlanStatus.InstallDuplicate]);
  });

  const result = (skill_id: number, outcome: GroupMemberOutcome) => ({ skill_id, scope: global(claude), outcome }) as GroupMemberResult;

  it("有被跳过的成员、或计划里就有装不上的才逐个列出结果", () => {
    const all = [result(1, GroupMemberOutcome.MemberInstalled), result(2, GroupMemberOutcome.MemberAlreadyInstalled)];
    expect(groupInstalled(all, []).listed).toBe(false);
    expect(groupInstalled(all, [plan(InstallPlanStatus.InstallOccupied)]).listed).toBe(true);
    const skipped = groupInstalled([...all, result(3, GroupMemberOutcome.MemberSkipped)], []);
    expect(skipped.listed).toBe(true);
    expect(skipped.placed.map((r) => r.skill_id)).toEqual([1, 2]);
  });
});

describe("卸载一处", () => {
  it("直接安装、没有组安装留着时删文件夹，共用这个文件夹的各处一起算", () => {
    const shared = place(1, global(claude), { shared_with: [codex] });
    const s = snapshot({ skillScopes: [shared, { ...place(1, global(codex), { shared_with: [claude] }), folder: shared.folder }] });
    const r = uninstallPlace(s, 1, global(claude));
    expect(r.shared).toEqual([{ place: shared, agents: [codex] }]);
    expect(r.deleted.map((p) => p.scope.agent)).toEqual([claude, codex]);
    expect(asksBeforeUninstall(r, [])).toBe(true);
  });

  it("会一并删掉旧链接、Agent 在别的目录里还有一份一样的时先确认", () => {
    const stale = snapshot({ skillScopes: [place(1, global(claude), { stale_links: ["/home/.cursor/skills/s1"] })] });
    expect(asksBeforeUninstall(uninstallPlace(stale, 1, global(claude)), [])).toBe(true);
    const copy = snapshot({ skillScopes: [place(1, global(claude), { copies: ["/home/.cursor/skills/s1"] })] });
    expect(asksBeforeUninstall(uninstallPlace(copy, 1, global(claude)), [])).toBe(true);
  });

  it("还有组安装留着时文件夹不删", () => {
    const s = snapshot({ skillScopes: [place(1, global(claude), { groups: [5] })] });
    expect(uninstallPlace(s, 1, global(claude)).deleted).toEqual([]);
  });

  it("平时一点就卸载;被手动改过、别处链接指着、别的技能会找不到它时先确认", () => {
    const plain = snapshot({ skillScopes: [place(1, global(claude))] });
    expect(asksBeforeUninstall(uninstallPlace(plain, 1, global(claude)), [])).toBe(false);
    expect(asksBeforeUninstall(uninstallPlace(plain, 1, global(claude)), [{ skill_id: 2, scope: global(claude), name: "s1" } as MissingMention])).toBe(true);
    const edited = snapshot({ skillScopes: [place(1, global(claude), { status: SkillScopeStatus.SkillScopeModified })] });
    expect(asksBeforeUninstall(uninstallPlace(edited, 1, global(claude)), [])).toBe(true);
    const linked = snapshot({ skillScopes: [place(1, global(claude), { linked_by: [global(codex)] })] });
    expect(asksBeforeUninstall(uninstallPlace(linked, 1, global(claude)), [])).toBe(true);
  });

  it("目录列表相同、合成一处的几个 Agent 不另说共用;只共用某个目录的照旧说;这一处没装时不问", () => {
    const agents = [
      { code: claude, name: "Claude Code", global_directories: ["/home/.claude/skills"], project_directories: [".agents/skills"] } as Agent,
      { code: codex, name: "Codex", global_directories: ["/home/.codex/skills"], project_directories: [".agents/skills"] } as Agent,
    ];
    const s = snapshot({ agents, skillScopes: [place(1, project(claude, 7), { shared_with: [codex] })] });
    expect(uninstallPlace(s, 1, project(claude, 7)).shared).toEqual([]);
    const partly = snapshot({ agents: [agents[0], { ...agents[1], project_directories: [".agents/skills", ".codex/skills"] }], skillScopes: s.skillScopes });
    expect(uninstallPlace(partly, 1, project(claude, 7)).shared.map((x) => x.agents)).toEqual([[codex]]);
    expect(asksBeforeUninstall(uninstallPlace(s, 2, project(claude, 7)), [{ skill_id: 2, scope: global(claude), name: "x" } as MissingMention])).toBe(false);
  });
});

describe("合成一处的几个 Agent 各自装在不同目录时卸载", () => {
  // Claude Code 和 Codex 的全局目录都是 [.dir1, .dir2],合成一处;s1 分别装在 Claude Code 的 .dir1 和 Codex 的 .dir2。
  const merged = agentsWithOwnDirectories.map((a) => ({ ...a, global_directories: ["/home/.dir1", "/home/.dir2"] }));
  const mine = place(1, global(claude), { folder: "/home/.dir1/s1" });
  const theirs = place(1, global(codex), { folder: "/home/.dir2/s1" });

  it("作用域页一行的开关关掉时两处的直接安装都撤，两个文件夹都删", () => {
    const s = snapshot({ agents: merged, skills: [skill(1)], skillScopes: [mine, theirs] });
    const r = uninstallPlace(s, 1, global(claude), true);
    expect(r.direct).toEqual([mine, theirs]);
    expect(r.deleted).toEqual([mine, theirs]);
    expect(r.shared).toEqual([]);
  });

  it("技能详情里按文件夹分开的一行只撤这一处，另一个 Agent 的文件夹留着", () => {
    const s = snapshot({ agents: merged, skills: [skill(1)], skillScopes: [mine, theirs] });
    expect(uninstallPlace(s, 1, global(claude))).toMatchObject({ direct: [mine], deleted: [mine] });
    expect(uninstallPlace(s, 1, global(codex))).toMatchObject({ direct: [theirs], deleted: [theirs] });
  });

  it("共用提示按每一处各自的文件夹、组安装说明：只读第二个目录的别的 Agent 只出现在那一处", () => {
    // Cursor 只读 .dir2,目录列表和合成一处的 Claude Code、Codex 不同。Claude Code 在 .dir1 还有组安装，Codex 在 .dir2 只有直接安装。
    const cursor = "cursor" as AgentCode;
    const agents = [...merged, { code: cursor, name: "Cursor", global_directories: ["/home/.dir2"], project_directories: [".cursor/skills"] } as Agent];
    const grouped = { ...mine, groups: [5] };
    const read = { ...theirs, shared_with: [cursor] };
    const s = snapshot({ agents, skills: [skill(1)], skillScopes: [grouped, read] });
    const r = uninstallPlace(s, 1, global(claude), true);
    expect(r.shared).toEqual([{ place: read, agents: [cursor] }]);
    expect(r.deleted).toEqual([read]);
  });

  it("另一处只随组安装时只撤有直接安装的那一处，它的文件夹留着", () => {
    const viaGroup = { ...theirs, direct: false, groups: [5] };
    const s = snapshot({ agents: merged, skills: [skill(1)], skillScopes: [mine, viaGroup] });
    expect(uninstallPlace(s, 1, global(claude), true)).toMatchObject({ direct: [mine], deleted: [mine] });
  });

  it("勾选卸载时两处都算", () => {
    const s = snapshot({ agents: merged, skills: [skill(1)], skillScopes: [mine, theirs] });
    const r = uninstallPicked(s, [1], [global(claude)]);
    expect(r.direct).toEqual([mine, theirs]);
    expect(r.folders).toBe(2);
  });
});

describe("从这里卸载勾选的技能", () => {
  const s = snapshot({
    skills: [skill(1), skill(2), skill(3)],
    skillScopes: [
      place(1, global(claude), { shared_with: [codex] }),
      place(2, global(claude), { groups: [5], shared_with: [codex] }),
      place(3, global(claude), { direct: false, groups: [5] }),
    ],
  });

  it("只随组安装的跳过;还有组安装留着的只撤直接安装、文件夹不删，也不说共用", () => {
    const r = uninstallPicked(s, [1, 2, 3], [global(claude)]);
    expect(r.direct.map((p) => p.skill_id)).toEqual([1, 2]);
    expect(r.viaGroup.map((p) => p.skill_id)).toEqual([3]);
    expect(r.deleted.map((p) => p.skill_id)).toEqual([1]);
    expect(r.folders).toBe(1);
    expect(r.shared.map((p) => p.skill_id)).toEqual([1]);
    expect(r.skills).toBe(2);
  });

  it("只随组安装的几处跳过并写明原因", () => {
    const picked = uninstallPicked(s, [1, 2, 3], [global(claude)]);
    expect(skippedPicked(s, picked).map((x) => [x.label, x.outcome, x.reason])).toEqual([["s3 · Claude Code · 全局", t.memberSkipped, t.onlyViaGroup]]);
  });
});

describe("卸载整组时删哪些成员的文件夹", () => {
  const group = (id: number, members: number[]) => ({ id, name: `g${id}`, members }) as Group;
  const installed = (group_id: number, scope: Scope) => ({ group_id, scope }) as GroupInstall;

  it("只靠这个组安装留着的才删;直接安装的、同一处别的组安装也含它的不删", () => {
    const s = snapshot({
      groups: [group(5, [1, 2, 3]), group(6, [3])],
      groupInstalls: [installed(5, global(claude)), installed(6, global(claude))],
      skillScopes: [
        place(1, global(claude), { direct: false, groups: [5] }),
        place(2, global(claude), { groups: [5] }),
        place(3, global(claude), { direct: false, groups: [5, 6] }),
      ],
    });
    expect(uninstallGroupPlaces(s, 5, global(claude)).gone.map((p) => p.skill_id)).toEqual([1]);
  });

  it("全局共用目录里别的 Agent 也装着含它的组时不删;共用的那几处要另外说明", () => {
    const s = snapshot({
      groups: [group(5, [1, 2]), group(6, [2])],
      groupInstalls: [installed(5, global(claude)), installed(6, global(codex))],
      skillScopes: [
        place(1, global(claude), { direct: false, groups: [5], shared_with: [codex], folder: "/home/.agents/skills/s1" }),
        place(1, global(codex), { direct: false, groups: [5], shared_with: [claude], shared_only: true, folder: "/home/.agents/skills/s1" }),
        place(2, global(claude), { direct: false, groups: [5, 6], shared_with: [codex], folder: "/home/.agents/skills/s2" }),
        place(2, global(codex), { direct: false, groups: [5, 6], shared_with: [claude], folder: "/home/.agents/skills/s2" }),
      ],
    });
    const r = uninstallGroupPlaces(s, 5, global(claude));
    expect(r.gone.map((p) => p.skill_id)).toEqual([1]);
    expect(r.shared.map((p) => p.skill_id)).toEqual([1]);
  });

  it("共用目录的别的 Agent 只是读到它、自己没有安装记录时，它含这个技能的组安装留不住文件夹", () => {
    // Codex 的组 6 里也有 s1,但组安装时 s1 因同名被跳过;之后 Codex 加了这个目录才读到 Claude Code 的 s1。
    const s = snapshot({
      groups: [group(5, [1]), group(6, [1])],
      groupInstalls: [installed(5, global(claude)), installed(6, global(codex))],
      skillScopes: [
        place(1, global(claude), { direct: false, groups: [5], shared_with: [codex], folder: "/home/.agents/skills/s1" }),
        place(1, global(codex), { direct: false, groups: [5], shared_with: [claude], shared_only: true, folder: "/home/.agents/skills/s1" }),
      ],
    });
    expect(uninstallGroupPlaces(s, 5, global(claude)).gone.map((p) => p.skill_id)).toEqual([1]);
  });

  it("合成一处里几个 Agent 各有这个组的组安装时一起撤掉：互相不算「别的组安装还留着」,同一个文件夹只算一次", () => {
    const s = snapshot({
      groups: [group(5, [1])],
      groupInstalls: [installed(5, global(claude)), installed(5, global(codex))],
      skillScopes: [
        place(1, global(claude), { direct: false, groups: [5], shared_with: [codex], folder: "/home/shared/s1" }),
        place(1, global(codex), { direct: false, groups: [5], shared_with: [claude], folder: "/home/shared/s1" }),
      ],
    });
    expect(uninstallGroupPlaces(s, 5, global(claude)).gone).toEqual([]);
    expect(uninstallGroupPlaces(s, 5, global(claude), [global(claude), global(codex)]).gone.map((p) => p.skill_id)).toEqual([1]);
  });

  it("别的项目里装着同一个组不算", () => {
    const s = snapshot({
      groups: [group(5, [1])],
      groupInstalls: [installed(5, global(claude)), installed(5, project(claude, 7))],
      skillScopes: [place(1, global(claude), { direct: false, groups: [5] })],
    });
    expect(uninstallGroupPlaces(s, 5, global(claude)).gone.map((p) => p.skill_id)).toEqual([1]);
  });
});
