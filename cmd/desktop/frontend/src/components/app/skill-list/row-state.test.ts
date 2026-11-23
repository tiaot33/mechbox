// 列表一行的状态写什么：技能库页说技能本身和它装出去的几处，作用域页只说这一处;和节标题的计数、更新页、左栏的更新数说同一回事。
import { describe, expect, it } from "vitest";
import {
  AgentCode,
  ScopeKind,
  SkillFileProblemKind,
  SkillScopeStatus,
  SourceType,
  UpdateCheckStatus,
  type Agent,
  type Group,
  type MissingMention,
  type RegisteredProject,
  type Scope,
  type Skill,
  type SkillScopeInspection,
  type UnmanagedSkill,
} from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { scopeKey } from "@/lib/scope";
import { sourceChanges, todoSkills } from "@/lib/source-state";
import { rowInstall, skillRowState, unmanagedRowState, updateAlerts, type RowAt } from "./row-state";

const claude = AgentCode.AgentClaudeCode;
const codex = AgentCode.AgentCodex;
const at = (agent: AgentCode, project_id = 0): Scope => (project_id ? { kind: ScopeKind.ScopeProject, agent, project_id } : { kind: ScopeKind.ScopeGlobal, agent });
const agents = [
  { code: claude, name: "Claude Code", global_directories: ["/home/.claude/skills"], project_directories: [".claude/skills"], detected: true },
  { code: codex, name: "Codex", global_directories: ["/home/.codex/skills"], project_directories: [".agents/skills"], detected: true },
] as Agent[];
const snapshot = (s: Partial<Snapshot>) =>
  ({
    agents,
    agentsShown: {},
    projects: [{ id: 1, name: "web" }] as RegisteredProject[],
    collections: [],
    groups: [],
    skills: [],
    skillScopes: [],
    unmanaged: [],
    sameName: [],
    missingMentions: [],
    info: { home_dir: "/home" },
    ...s,
  }) as unknown as Snapshot;
type Check = NonNullable<Skill["last_update_check"]>;
const skill = (id: number, name: string, check?: Partial<Check>, extra: Partial<Skill> = {}) =>
  ({ id, name, source: { type: SourceType.SourceGit, label: "git.dev.test/a/skills" }, last_update_check: check, ...extra }) as Skill;
const installed = (skill_id: number, scope: Scope, extra: Partial<SkillScopeInspection> = {}) =>
  ({ skill_id, scope, folder: `/f/${scopeKey(scope)}/s${skill_id}`, direct: true, status: SkillScopeStatus.SkillScopeCurrent, ...extra }) as SkillScopeInspection;
const unmanaged = (name: string, scope: Scope, extra: Partial<UnmanagedSkill> = {}) => ({ name, folder: `/f/${scopeKey(scope)}/${name}`, scope, ...extra }) as UnmanagedSkill;

const available = { status: UpdateCheckStatus.UpdateAvailable, pending: true };
// libraryRow 是技能库页的一行，scopeRow 是作用域页、项目页的一行。
const libraryRow = (s: Snapshot, x: Skill) => skillRowState(s, x, rowInstall(s, x));
const scopeRow = (s: Snapshot, x: Skill, where: RowAt, elsewhere?: string[]) => skillRowState(s, x, rowInstall(s, x, where), elsewhere);
const labels = (state: { alerts: { label: string }[] }) => state.alerts.map((a) => a.label);

describe("技能库页一行的状态", () => {
  it("没有要处理的事时空着", () => {
    const x = skill(1, "pdf");
    expect(libraryRow(snapshot({ skills: [x], skillScopes: [installed(1, at(claude))] }), x)).toEqual({ alerts: [], plain: undefined });
  });

  it("有新版本、可能改名时点它打开更新计划，两种图标不同", () => {
    const s = snapshot({});
    expect(libraryRow(s, skill(1, "pdf", available)).alerts).toMatchObject([{ tone: "update", label: "有新版本", plan: true }]);
    expect(libraryRow(s, skill(1, "pdf", { status: UpdateCheckStatus.UpdateDeleted, renamed_to: "pdf-kit", pending: true })).alerts).toMatchObject([
      { tone: "rename", label: "可能改名为 pdf-kit", plan: true },
    ]);
  });

  it("跳过的改动不再提", () => {
    expect(libraryRow(snapshot({}), skill(1, "pdf", { status: UpdateCheckStatus.UpdateAvailable, skipped: true })).alerts).toEqual([]);
  });

  it("有新版本而库里的当前版本带着用户的修改：行上画的是更新会换掉修改，点它不直接打开更新计划", () => {
    const state = libraryRow(snapshot({}), skill(1, "pdf", available, { edited: true }));
    expect(labels(state)).toEqual(["更新会换掉你的修改", "有新版本", "有你的修改"]);
    expect(state.alerts[0].plan).toBeUndefined();
  });

  it("来源里已删除是一条提示;选了继续保留的不提醒，只留一句淡色的说明", () => {
    const s = snapshot({});
    expect(libraryRow(s, skill(1, "pdf", { status: UpdateCheckStatus.UpdateDeleted, pending: true })).alerts).toMatchObject([{ tone: "info", label: "来源里已删除" }]);
    expect(libraryRow(s, skill(1, "pdf", { status: UpdateCheckStatus.UpdateDeleted }, { kept_deleted: true }))).toEqual({
      alerts: [],
      plain: { text: "来源已删除", title: "作者已从来源删掉它，你选了继续保留" },
    });
  });

  it("本地目录来源在目录里改过的标出来，不是点它打开更新计划", () => {
    const x = skill(1, "notes", { status: UpdateCheckStatus.UpdateAvailable }, { source: { type: SourceType.SourceLocal, label: "/home/code/x" } as Skill["source"] });
    expect(libraryRow(snapshot({}), x).alerts).toMatchObject([{ tone: "update", label: "目录里改过" }]);
    expect(libraryRow(snapshot({}), x).alerts[0].plan).toBeUndefined();
  });

  it("装出去的几处和当前版本不一样的按原因各一条;行上画最要紧的：严重、注意在前，接着是更新，提示最后", () => {
    const x = skill(1, "pdf", available);
    const s = snapshot({
      skills: [x],
      skillScopes: [
        installed(1, at(claude), { status: SkillScopeStatus.SkillScopeMissing }),
        installed(1, at(codex), { status: SkillScopeStatus.SkillScopeModified }),
        installed(1, at(codex, 1), { status: SkillScopeStatus.SkillScopeModified, source_latest: true }),
      ],
    });
    const state = libraryRow(s, x);
    expect(labels(state)).toEqual(["文件夹不在了", "作者的新版", "有新版本", "被手动改过"]);
    // 在 app 外更新到了作者新版的那一处是更新：点它也打开更新计划。
    expect(state.alerts.map((a) => !!a.plan)).toEqual([false, true, true, false]);
  });

  it("库里有同名的另一个时带上它们，行组件据此弹出菜单", () => {
    const [a, b] = [skill(1, "tdd"), skill(2, "tdd")];
    const state = libraryRow(snapshot({ skills: [a, b] }), a);
    expect(state.alerts).toMatchObject([{ tone: "info", label: "库里有同名的", twins: [b] }]);
  });

  it("行上的标记和节标题的计数、更新页、左栏的更新数说的是同一回事", () => {
    const skills = [
      skill(1, "a", available),
      skill(2, "b", { status: UpdateCheckStatus.UpdateAvailable, skipped: true }),
      skill(3, "c", { status: UpdateCheckStatus.UpdateDeleted, renamed_to: "c2", pending: true }),
      skill(4, "d", { status: UpdateCheckStatus.UpdateDeleted, pending: true }),
      skill(5, "e", { status: UpdateCheckStatus.UpdateDeleted }, { kept_deleted: true }),
      skill(6, "f", { status: UpdateCheckStatus.UpdateCurrent }),
    ];
    const s = snapshot({ skills });
    const marked = (tones: string[]) => skills.filter((x) => libraryRow(s, x).alerts.some((a) => tones.includes(a.tone))).map((x) => x.id);
    const found = sourceChanges(skills);
    expect(marked(["update", "rename"])).toEqual(found.changed.map((x) => x.id));
    expect(marked(["info"])).toEqual(found.deleted.map((x) => x.id));
    expect(skills.filter((x) => updateAlerts(x).length > 0).map((x) => x.id)).toEqual(todoSkills(s).map((x) => x.id));
  });
});

describe("作用域页一行的状态", () => {
  const here: RowAt = { scope: scopeKey(at(claude, 1)) };

  it("直接安装是常态，不写;只随组安装的写随哪个组", () => {
    const x = skill(1, "pdf");
    const groups = [{ id: 7, name: "文档", members: [1] }] as Group[];
    expect(scopeRow(snapshot({ skills: [x], skillScopes: [installed(1, at(claude, 1))] }), x, here)).toEqual({ alerts: [], plain: undefined });
    const s = snapshot({ skills: [x], groups, skillScopes: [installed(1, at(claude, 1), { direct: false, groups: [7] })] });
    expect(scopeRow(s, x, here).plain).toEqual({ text: "随「文档」安装" });
    expect(rowInstall(s, x, here).via).toEqual(["文档"]);
  });

  it("只说这一处：别处要重新安装的不算", () => {
    const x = skill(1, "pdf");
    const s = snapshot({
      skills: [x],
      skillScopes: [installed(1, at(claude, 1), { status: SkillScopeStatus.SkillScopeStale }), installed(1, at(codex), { status: SkillScopeStatus.SkillScopeMissing })],
    });
    expect(labels(scopeRow(s, x, here))).toEqual(["旧版本"]);
  });

  it("没装在这里的行不带这一处的提醒，技能本身的更新照样写", () => {
    const x = skill(1, "pdf", available);
    const s = snapshot({ skills: [x], skillScopes: [installed(1, at(codex), { status: SkillScopeStatus.SkillScopeMissing })] });
    expect(labels(scopeRow(s, x, here))).toEqual(["有新版本"]);
    expect(rowInstall(s, x, here)).toMatchObject({ here: undefined, place: undefined, foreign: false });
  });

  it("合成一处的几个 Agent 各自装在不同目录时，每一处都算：哪一处要重新安装都标出来，悬停写出是哪几处", () => {
    // Claude Code 和 Codex 的全局目录都是 [.dir1, .dir2],合成一处;pdf 分别装在 Claude Code 的 .dir1 和 Codex 的 .dir2。
    const merged = agents.map((a) => ({ ...a, global_directories: ["/home/.dir1", "/home/.dir2"] }));
    const x = skill(1, "pdf");
    const mine = installed(1, at(claude), { folder: "/home/.dir1/pdf" });
    const theirs = installed(1, at(codex), { folder: "/home/.dir2/pdf", status: SkillScopeStatus.SkillScopeMissing, direct: false, groups: [7] });
    const groups = [{ id: 7, name: "文档", members: [1] }] as Group[];
    const s = snapshot({ agents: merged, skills: [x], groups, skillScopes: [mine, theirs] });
    const where: RowAt = { scope: scopeKey(at(claude)) };
    expect(rowInstall(s, x, where)).toMatchObject({ here: mine, places: [mine, theirs], direct: true, via: ["文档"] });
    const state = scopeRow(s, x, where);
    expect(labels(state)).toEqual(["文件夹不在了"]);
    expect(state.alerts[0].tip).toBe("Codex · 全局");
    expect(scopeRow(s, x, where).plain).toEqual({ text: "直接安装 + 随「文档」安装" });
  });

  it("合成一处的几个 Agent 各自装在不同目录时，任何一个 Agent 那一份提到的技能找不到都提醒", () => {
    // 项目里 Claude Code、Codex 的目录都是 [d1, d2],合成一处;entry 装在 Claude Code 的 d1 和 Codex 的 d2,只有 Codex 那边找不到 ship。
    const merged = agents.map((a) => ({ ...a, project_directories: ["d1", "d2"] }));
    const x = skill(1, "entry");
    const mine = installed(1, at(claude, 1), { folder: "/p/d1/entry" });
    const theirs = installed(1, at(codex, 1), { folder: "/p/d2/entry" });
    const missingMentions = [{ skill_id: 1, scope: at(codex, 1), name: "ship" }] as MissingMention[];
    const s = snapshot({ agents: merged, skills: [x], skillScopes: [mine, theirs], missingMentions });
    expect(scopeRow(s, x, here).alerts.map((a) => a.className)).toContain("mentions-missing");
  });

  it("项目里合成一处、全局目录不同时，来自全局的一行看几个 Agent 各自全局的那一份", () => {
    const merged = agents.map((a) => ({ ...a, project_directories: [".agents/skills"] }));
    const x = skill(1, "pdf");
    const s = snapshot({
      agents: merged,
      skills: [x],
      skillScopes: [installed(1, at(claude)), installed(1, at(codex), { status: SkillScopeStatus.SkillScopeMissing })],
    });
    expect(labels(scopeRow(s, x, { ...here, fromGlobal: true }))).toEqual(["文件夹不在了"]);
  });

  it("来自全局的写来自全局，说的是全局那一处", () => {
    const x = skill(1, "pdf");
    const global = installed(1, at(claude), { status: SkillScopeStatus.SkillScopeStale });
    const s = snapshot({ skills: [x], skillScopes: [global] });
    const where = { ...here, fromGlobal: true };
    expect(rowInstall(s, x, where)).toMatchObject({ here: undefined, place: global });
    expect(labels(scopeRow(s, x, where))).toEqual(["旧版本"]);
    expect(scopeRow(snapshot({ skills: [x], skillScopes: [installed(1, at(claude))] }), x, where).plain?.text).toBe("来自全局");
  });

  it("装在这里的技能提到的技能在这里找不到时有一条提示", () => {
    const x = skill(1, "pdf");
    const missingMentions = [
      { skill_id: 1, scope: at(claude, 1), name: "docx" },
      { skill_id: 1, scope: at(claude, 1), name: "xlsx" },
      { skill_id: 1, scope: at(codex), name: "other" },
    ] as MissingMention[];
    const s = snapshot({ skills: [x], skillScopes: [installed(1, at(claude, 1))], missingMentions });
    expect(scopeRow(s, x, here).alerts).toMatchObject([{ tone: "info", label: "提到的 2 个找不到" }]);
  });

  it("这里那一份和它一模一样、不是 app 装的：写明这一句，和旧版本一样时标出来", () => {
    const x = skill(1, "pdf", available, { version_id: 3 });
    const copy = unmanaged("pdf", at(claude, 1), { skill_id: 1, version_id: 2 });
    const s = snapshot({ skills: [x], unmanaged: [copy] });
    const state = scopeRow(s, x, here, ["Codex · web"]);
    expect(rowInstall(s, x, here)).toMatchObject({ identical: copy, foreign: false });
    expect(state.plain?.text).toBe("已在库里 · 这一份不是 app 装的");
    expect(labels(state)).toEqual(["旧版本", "别处有同名的，内容不同", "有新版本"]);
  });

  it("装在这里的技能，这里还多一份一样的文件夹时有一条提示，写出它在哪里", () => {
    const x = skill(1, "pdf");
    const copy = unmanaged("pdf-copy", at(claude, 1), { skill_id: 1 });
    const another = unmanaged("pdf-backup", at(claude, 1), { skill_id: 1 });
    const s = snapshot({ skills: [x], skillScopes: [installed(1, at(claude, 1))], unmanaged: [copy, another] });
    expect(rowInstall(s, x, here).identical).toBeUndefined();
    expect(scopeRow(s, x, here).alerts).toMatchObject([{ tone: "info", label: "多一份一样的", className: "extra-copy" }]);
    expect(scopeRow(s, x, here).alerts[0].tip).toContain(copy.folder);
    expect(scopeRow(s, x, here).alerts[0].tip).toContain(another.folder);
    expect(scopeRow(snapshot({ skills: [x], skillScopes: [installed(1, at(claude, 1))] }), x, here).alerts).toEqual([]);
  });

  it("那一份归项目仓库管时不带库里这个技能的提醒，只留这个文件夹自己的同名提醒", () => {
    const x = skill(1, "pdf", available, { version_id: 3 });
    const copy = unmanaged("pdf", at(claude, 1), { skill_id: 1, version_id: 2, in_repository: true });
    const s = snapshot({ skills: [x], unmanaged: [copy] });
    expect(rowInstall(s, x, here).foreign).toBe(true);
    expect(scopeRow(s, x, here).alerts).toEqual([]);
    expect(labels(scopeRow(s, x, here, ["Codex · web"]))).toEqual(["别处有同名的，内容不同"]);
    expect(scopeRow(s, x, here).plain?.text).toBe("在项目仓库里 · 已在库里 · 这一份不是 app 装的");
  });
});

describe("未纳管技能一行的状态", () => {
  const u = unmanaged("draft", at(claude));

  it("最后一列写不在技能库里;在「不在技能库里」一节里时节标题已经写了，不再写", () => {
    const s = snapshot({ unmanaged: [u] });
    expect(unmanagedRowState(s, u).state.map((x) => x.text)).toEqual(["不在技能库里"]);
    expect(unmanagedRowState(s, u, { headed: true }).state).toEqual([]);
    expect(unmanagedRowState(s, u, { fromGlobal: true }).state.map((x) => x.text)).toEqual(["来自全局", "不在技能库里"]);
  });

  it("归项目仓库管的接着写出来", () => {
    const repo = unmanaged("draft", at(claude, 1), { in_repository: true });
    expect(unmanagedRowState(snapshot({ unmanaged: [repo] }), repo, { headed: true }).state.map((x) => x.text)).toEqual(["在项目仓库里"]);
  });

  it("SKILL.md 写错了的不能入库，描述那一格写哪里写错了", () => {
    const broken = unmanaged("broken", at(claude), { invalid: { kind: SkillFileProblemKind.SkillFileNoDescription } });
    const state = unmanagedRowState(snapshot({ unmanaged: [broken] }), broken, { headed: true });
    expect(state.invalid).toBe("SKILL.md 的 frontmatter 里没有 description，或 description 不是文本");
    expect(state.state.map((x) => x.text)).toEqual(["不能入库"]);
    expect(state.notes.map((x) => x.className)).toEqual(["unmanaged-invalid"]);
  });

  it("来源记录认出的来源和在 skills.sh 上找到的来源，各写怎么认出来的", () => {
    const s = snapshot({ unmanaged: [u] });
    const recorded = unmanagedRowState(s, { ...u, source_input: "matt/skills" });
    expect(recorded.notes.map((x) => x.text)).toEqual(["来自 matt/skills", "来自 matt/skills:npx skills 记下的来源，入库后跟着它检查更新"]);
    const found = unmanagedRowState(s, u, { found: "matt/skills" });
    expect(found.notes.filter((x) => x.detail).map((x) => x.text)).toEqual(["来自 matt/skills：在 skills.sh 上找到的同名技能，内容和这里的大部分相同，入库后跟着它检查更新"]);
  });

  it("认出是库里哪个技能的按那个技能说;库里有同名而内容对不上的提醒入库后会多一个同名的", () => {
    const pdf = skill(1, "pdf");
    const s = snapshot({ skills: [pdf] });
    const same = unmanagedRowState(s, unmanaged("pdf", at(claude), { skill_id: 1 }));
    expect(same.library).toBe(pdf);
    expect(same.state.map((x) => x.text)).toEqual(["和库里的「pdf」一样"]);
    expect(same.alerts).toEqual([]);
    // 没有来源的 pdf:来源记录记着的来源不写在这一行，入库只补记安装，不跟着它。
    const recorded = unmanagedRowState(s, unmanaged("pdf", at(claude), { skill_id: 1, source_input: "matt/skills" }));
    expect(recorded.notes.map((x) => x.className)).not.toContain("unmanaged-source-label");
    const newer = unmanagedRowState(s, unmanaged("pdf", at(claude), { source_latest_of: 1 }));
    expect(newer.newer).toBe(pdf);
    expect(newer.state.map((x) => x.text)).toEqual(["库里「pdf」的新版"]);
    expect(unmanagedRowState(s, unmanaged("pdf", at(claude))).alerts).toMatchObject([{ tone: "info", label: "库里有同名的", className: "library-twin" }]);
  });

  it("作者的新版：更新后照常自动记为装在这里;归项目仓库管、指向 Agent 目录以外的不会自动纳入，说更新后要再入库", () => {
    const s = snapshot({ skills: [skill(1, "pdf")] });
    const title = (extra: Partial<UnmanagedSkill>) => unmanagedRowState(s, unmanaged("pdf", at(claude, 1), { source_latest_of: 1, ...extra })).state[0].title;
    expect(title({})).toMatch(/自动记为装在这里$/);
    expect(title({ in_repository: true })).toMatch(/再入库只记下装在这里$/);
    expect(title({ in_project_lock: true })).toMatch(/再入库只记下装在这里$/);
    expect(title({ link_outside: true })).toMatch(/再入库只记下装在这里$/);
  });

  it("同一层的别处同名、内容不同的说出来", () => {
    const state = unmanagedRowState(snapshot({ unmanaged: [u] }), u, { copies: ["Codex · 全局"] });
    expect(state.alerts).toMatchObject([{ tone: "warn", label: "别处有同名的，内容不同" }]);
  });
});
