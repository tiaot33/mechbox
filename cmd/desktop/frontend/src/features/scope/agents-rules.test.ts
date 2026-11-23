// Agent:改目录时哪些已装的技能、组安装受影响，移除 Agent 时哪些文件夹归它，候选池的顺序与「已有 Agent 读这个目录」。
import { describe, expect, it } from "vitest";
import {
  AgentCode,
  ErrorCode,
  MigrationOutcome,
  ScopeKind,
  type ResetAgentsPlan,
  SkillScopeStatus,
  type Agent,
  type AgentInput,
  type GroupInstall,
  type Scope,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import { messages } from "@/lib/i18n";
import type { LibraryError, Snapshot } from "@/lib/library";
import { agentDelete, agentEdit, agentFieldError, agentReset, alreadyRead, asksWhere, directoryKey, enterSubmits, detectedPicks, directoryOptions, filterDirectoryOptions, keepsFieldError, migrationReport, opensWhileTyping, overlappingDirectories, poolMatches, sortPool, undetected } from "./agents-rules";

const mine = "my-agent" as AgentCode;
const claude = AgentCode.AgentClaudeCode;
const global = (agent: AgentCode): Scope => ({ kind: ScopeKind.ScopeGlobal, agent });
const project = (agent: AgentCode, project_id: number): Scope => ({ kind: ScopeKind.ScopeProject, agent, project_id });
const place = (skill_id: number, scope: Scope, extra: Partial<SkillScopeInspection> = {}): SkillScopeInspection => ({
  directory: "",
  skill_id,
  scope,
  folder: `/${scope.project_id ?? "home"}/${scope.agent}/s${skill_id}`,
  direct: true,
  status: SkillScopeStatus.SkillScopeCurrent,
  ...extra,
});

const agent = { code: mine, name: "Mine", global_directories: ["/home/.mine/skills"], project_directories: [".mine/skills" ]} as Agent;
const input = (extra: Partial<AgentInput>) => ({ name: "Mine", global_directories: ["/home/.mine/skills"], project_directories: [".mine/skills"], ...extra }) as AgentInput;

const exact = { foldsCase: false, backslashes: false };

const snapshot = (skillScopes: SkillScopeInspection[], groupInstalls: GroupInstall[] = []) => ({ skillScopes, groupInstalls }) as unknown as Snapshot;

describe("改 Agent 的第 1 个目录", () => {
  const s = snapshot(
    [place(1, global(mine), { shared_with: [claude] }), place(2, global(mine), { linked_by: [global(claude)] }), place(3, project(mine, 7)), place(4, global(claude))],
    [{ group_id: 5, scope: project(mine, 7) } as GroupInstall],
  );

  const onWindows = (folds: boolean) => ({ ...s, info: { home_dir: "C:\\Users\\me", home_folds_case: folds } }) as Snapshot;

  it("Windows 上全局目录末尾带反斜杠也是同一个目录，不问", () => {
    const windows = { ...agent, global_directories: ["C:\\Users\\me\\.mine\\skills"] } as Agent;
    expect(agentEdit(onWindows(true), windows, input({ global_directories: ["C:\\Users\\me\\.mine\\skills\\"] })).asks).toBe(false);
  });

  it("macOS、Linux 上反斜杠是文件名的一部分：从 team\\skills 改到 team/skills 是换了目录，要问", () => {
    const mac = { ...s, info: { home_dir: "/home", home_folds_case: true } } as Snapshot;
    const backslash = { ...agent, global_directories: ["/home/team\\skills"] } as Agent;
    expect(agentEdit(mac, backslash, input({ global_directories: ["/home/team/skills"] })).asks).toBe(true);
  });

  it("主目录所在的卷不区分大小写时，全局目录只改大小写不算改了，不问;区分大小写时算改了", () => {
    const windows = { ...agent, global_directories: ["C:\\Users\\me\\.agents\\skills"] } as Agent;
    const caseOnly = input({ global_directories: ["C:\\Users\\me\\.Agents\\skills"] });
    expect(agentEdit(onWindows(true), windows, caseOnly)).toMatchObject({ affected: [], groupInstalls: 0, asks: false });
    expect(agentEdit(onWindows(false), windows, caseOnly).asks).toBe(true);
  });

  it("项目目录只改大小写照常问：项目可以在区分大小写的卷上，由技能库逐个项目判断", () => {
    const windows = { ...agent, project_directories: [".agents/skills"] } as Agent;
    const r = agentEdit(onWindows(true), windows, input({ project_directories: [".Agents/skills"] }));
    expect(r.affected.map((p) => p.skill_id)).toEqual([3]);
    expect(r.asks).toBe(true);
  });

  it("写法不同的同一个目录不算改了，不问", () => {
    expect(agentEdit(s, agent, input({ global_directories: ["/home/.mine/skills/"], project_directories: ["./.mine/skills" ]}))).toMatchObject({ affected: [], groupInstalls: 0, asks: false });
  });

  it("只算改了的那一层：全局改了算全局装的;还有别的 Agent 在读的另说，别处链接指着要搬走的先换成独立的一份", () => {
    const r = agentEdit(s, agent, input({ global_directories: ["/home/.other" ]}));
    expect(r.affected.map((p) => p.skill_id)).toEqual([1, 2]);
    expect(r.groupInstalls).toBe(0);
    expect(r.asks).toBe(true);
    expect(r.shared.map((p) => p.skill_id)).toEqual([1]);
    expect(r.detached.map((d) => d.place.skill_id)).toEqual([2]);
  });

  it("项目目录改了算各项目里装的和组安装;只有组安装时也问", () => {
    const r = agentEdit(s, agent, input({ project_directories: [".other" ]}));
    expect(r.affected.map((p) => p.skill_id)).toEqual([3]);
    expect(r.groupInstalls).toBe(1);
    expect(agentEdit(snapshot([], s.groupInstalls), agent, input({ project_directories: [".other" ]})).asks).toBe(true);
  });
});

describe("移除 Agent", () => {
  it("只有它在读的文件夹归它;还有别的 Agent 在读的保留，别的 Agent 的不算", () => {
    const r = agentDelete(snapshot([place(1, global(mine), { shared_with: [claude] }), place(2, global(mine)), place(3, global(claude))]), mine);
    expect(r.kept.map((p) => p.skill_id)).toEqual([1]);
    expect(r.own.map((p) => p.skill_id)).toEqual([2]);
  });

  it("只因共用目录才算已装的不删，算在保留的里", () => {
    const shared = place(1, global(mine), { shared_with: [claude], shared_only: true });
    expect(agentDelete(snapshot([shared]), mine)).toEqual({ kept: [shared], own: [] });
  });
});

describe("改 Agent 的附加目录", () => {
  // Cursor 全局读 ~/.agents/skills 和 ~/.cursor/skills:技能 1 装在第 1 个目录，技能 2 原地入库在附加目录;项目里有组安装。
  const cursor = "cursor" as AgentCode;
  const agent = {
    code: cursor,
    name: "Cursor",
    global_directories: ["/home/.agents/skills", "/home/.cursor/skills"],
    project_directories: [".agents/skills", ".cursor/skills"],
  } as Agent;
  const s = snapshot(
    [
      place(1, global(cursor), { directory: "/home/.agents/skills" }),
      place(2, global(cursor), { directory: "/home/.cursor/skills" }),
      place(3, project(cursor, 7), { directory: ".cursor/skills" }),
    ],
    [{ group_id: 5, scope: project(cursor, 7) } as GroupInstall],
  );
  const edit = (global_directories: string[], project_directories: string[]) =>
    agentEdit(s, agent, { name: "Cursor", global_directories, project_directories } as AgentInput);

  it("只有所在目录被去掉的安装要迁移：去掉附加目录只算装在那里的", () => {
    const r = edit(["/home/.agents/skills"], [".agents/skills", ".cursor/skills"]);
    expect(r.affected.map((p) => p.skill_id)).toEqual([2]);
    expect(r.groupInstalls).toBe(0);
  });

  it("改了第 1 个目录、装在附加目录里的不动;组安装按原来的第 1 个目录是否被去掉算", () => {
    const r = edit(["/home/.cursor/skills"], [".cursor/skills"]);
    expect(r.affected.map((p) => p.skill_id)).toEqual([1]);
    expect(r.groupInstalls).toBe(1);
  });

  it("只调换目录的顺序不算去掉，不问", () => {
    expect(edit(["/home/.cursor/skills", "/home/.agents/skills"], [".agents/skills", ".cursor/skills"]).asks).toBe(false);
  });
});

describe("候选池", () => {
  const entry = (code: string, name: string, global: string[], project: string[], detected = false) =>
    ({ code: code as AgentCode, name, detected, global_directories: global, project_directories: project }) as Agent;
  const cursor = entry("cursor", "Cursor", ["/home/.agents/skills", "/home/.cursor/skills"], [".agents/skills", ".cursor/skills"]);
  const cline = entry("cline", "Cline", ["/home/.agents/skills"], [".agents/skills"], true);
  const amp = entry("amp", "Amp", ["/home/.config/agents/skills"], [".agents/skills"]);
  const pool = [cursor, cline, amp];

  it("检测到的排在前面，其余按名称", () => {
    expect(sortPool(pool).map((a) => a.name)).toEqual(["Cline", "Amp", "Cursor"]);
  });

  it("按名称、目录搜索", () => {
    expect(pool.filter((a) => poolMatches(a, "CUR")).map((a) => a.name)).toEqual(["Cursor"]);
    expect(pool.filter((a) => poolMatches(a, ".config")).map((a) => a.name)).toEqual(["Amp"]);
    expect(pool.filter((a) => poolMatches(a, " ")).length).toBe(3);
  });

  it("独有的目录已经有已添加的 Agent 在读时标出;许多 Agent 都读的 .agents/skills 不比", () => {
    // 升级前手填过一个读 .cursor/skills 的 Agent;Codex 只读 .agents/skills。
    const old = entry("custom-1", "My Cursor", ["/home/.cursor/skills/"], ["./.cursor/skills"]);
    const codex = entry("codex", "Codex", ["/home/.agents/skills"], [".agents/skills"]);
    expect(alreadyRead(pool, [old, codex], cursor)).toEqual([
      { dir: "/home/.cursor/skills", project: false, by: [old] },
      { dir: ".cursor/skills", project: true, by: [old] },
    ]);
    expect(alreadyRead(pool, [old, codex], cline)).toEqual([]);
  });
});

describe("升级后提示添加 Agent", () => {
  const pooled = (code: string, detected: boolean, global: string[]) =>
    ({ code, name: code, detected, global_directories: global, project_directories: [".agents/skills"], detect_directories: [] }) as unknown as Agent;
  it("检测到的默认勾上;独有的目录已经有已添加的 Agent 在读的不勾，没检测到的不勾", () => {
    const pool = [
      pooled("cursor", true, ["/home/.agents/skills", "/home/.cursor/skills"]),
      pooled("gemini-cli", true, ["/home/.agents/skills", "/home/.gemini/skills"]),
      pooled("cline", false, ["/home/.agents/skills"]),
    ];
    const added = [{ code: "custom-1", name: "cursor", global_directories: ["/home/.cursor/skills"], project_directories: [".cursor/skills"] } as unknown as Agent];
    expect(detectedPicks(pool, added)).toEqual(["gemini-cli"]);
  });
});

describe("设置页的「未检测到」", () => {
  const withDetect = (detected: boolean, detect_directories: string[]) => ({ ...agent, detected, detect_directories }) as Agent;
  it("有检测目录、都不存在时才标;检测目录为空的是不检测，不标", () => {
    expect(undetected(withDetect(false, ["/home/.mine"]))).toBe(true);
    expect(undetected(withDetect(true, ["/home/.mine"]))).toBe(false);
    expect(undetected(withDetect(false, []))).toBe(false);
    expect(undetected({ ...agent, detected: false } as Agent)).toBe(false);
  });
});

describe("改目录并迁移之后", () => {
  const s = { info: { home_dir: "/home" }, skills: [{ id: 1, name: "moved" }, { id: 2, name: "only-here" }, { id: 3, name: "blocked" }], projects: [] } as unknown as Snapshot;
  const migration = (skill_id: number, outcome: MigrationOutcome) => ({
    skill_id,
    scope: global(mine),
    outcome,
    from: `/home/.mine/skills/s${skill_id}`,
    to: `/home/.agents/skills/s${skill_id}`,
  });
  it("都原样搬过去了只提示;新目录里已经装着同一个技能的算迁移了，也列出来说清旧文件夹留在原处", () => {
    expect(migrationReport(s, [migration(1, MigrationOutcome.MigrationMoved)])).toMatchObject({ listed: false, migrated: 1, skipped: 0 });
    const r = migrationReport(s, [migration(1, MigrationOutcome.MigrationMoved), migration(2, MigrationOutcome.MigrationAlreadyInstalled)]);
    expect(r).toMatchObject({ listed: true, migrated: 2, skipped: 0 });
    expect(r.rows[0].reason).toContain("旧文件夹留在原处：~/.mine/skills/s2");
  });
  it("有没迁移的才列出;已经装着的那处写明改认新目录里那一份，不说成没迁移", () => {
    const r = migrationReport(s, [migration(2, MigrationOutcome.MigrationAlreadyInstalled), migration(3, MigrationOutcome.MigrationSkipped)]);
    expect(r).toMatchObject({ listed: true, migrated: 1, skipped: 1 });
    expect(r.rows.map((row) => [row.label, row.outcome, row.tone])).toEqual([
      ["blocked · 全局", "没有迁移", "warn"],
      ["only-here · 全局", "新目录里已装着", "plain"],
    ]);
    expect(r.rows[1].reason).toContain("~/.agents/skills/s2");
    expect(r.rows[1].reason).toContain("照常算已装");
    expect(r.rows[1].reason).not.toContain("不再算已装");
  });
  it("新目录那一份是别的 Agent 装的才说是谁装的;没有安装记录的(如上次迁移留在原处的旧文件夹)只说和库里某一版一致，不说别的 Agent 装的", () => {
    const named = { ...s, agents: [{ code: AgentCode.AgentCodex, name: "Codex" }] } as unknown as Snapshot;
    const [codex] = migrationReport(named, [{ ...migration(2, MigrationOutcome.MigrationAlreadyInstalled), installed_for: [AgentCode.AgentCodex] }]).rows;
    expect(codex.reason).toContain("新目录里已经装着这个技能（Codex 装的）：~/.agents/skills/s2");
    const [left] = migrationReport(named, [migration(2, MigrationOutcome.MigrationAlreadyInstalled)]).rows;
    expect(left.reason).toContain("新目录里已经有这个技能，和技能库里它的某一版一致：~/.agents/skills/s2");
    expect(left.reason).not.toContain("装的");
  });
});

describe("目录输入框的下拉候选", () => {
  const catalog = [
    { code: "cursor", global_directories: ["/home/.agents/skills", "/home/.cursor/skills"], project_directories: [".agents/skills"], detect_directories: ["/home/.cursor"] },
    { code: "codex", global_directories: ["/home/.agents/skills"], project_directories: [".agents/skills"], detect_directories: ["/home/.codex"] },
    { code: "claude-code", global_directories: ["/home/.claude/skills"], project_directories: [".claude/skills"], detect_directories: ["/home/.claude"] },
  ] as unknown as Agent[];
  const short = (d: string) => d.replace(/^\/home/, "~");
  it("先列这个 Agent 的默认目录(内置的值),再列其他 Agent 用到的，去掉重复", () => {
    expect(directoryOptions(catalog, "cursor", "global_directories", short)).toEqual({
      defaults: ["~/.agents/skills", "~/.cursor/skills"],
      others: ["~/.claude/skills"],
    });
    expect(directoryOptions(catalog, "cursor", "detect_directories", short)).toEqual({ defaults: ["~/.cursor"], others: ["~/.claude", "~/.codex"] });
  });
  it("手填添加的 Agent 没有默认目录那一组，照样能从全部已知目录里挑", () => {
    expect(directoryOptions(catalog, undefined, "project_directories", (d) => d)).toEqual({ defaults: [], others: [".agents/skills", ".claude/skills"] });
    expect(directoryOptions(catalog, "custom-1", "global_directories", short).defaults).toEqual([]);
  });
  it("输入时按文字过滤;这组里别的行已经填了的不再列", () => {
    const options = directoryOptions(catalog, "cursor", "global_directories", short);
    expect(filterDirectoryOptions(options, "CLAUDE", [], exact)).toEqual({ defaults: [], others: ["~/.claude/skills"] });
    expect(filterDirectoryOptions(options, "", ["~/.agents/skills/"], exact)).toEqual({ defaults: ["~/.cursor/skills"], others: ["~/.claude/skills"] });
  });
  it("别的行和候选只差大小写、在不区分大小写的卷上是同一个目录，也不再列", () => {
    const options = directoryOptions(catalog, "cursor", "global_directories", short);
    expect(filterDirectoryOptions(options, "", ["~/.Agents/skills"], { foldsCase: true, backslashes: false }).defaults).toEqual(["~/.cursor/skills"]);
    expect(filterDirectoryOptions(options, "", ["~/.Agents/skills"], exact).defaults).toEqual(["~/.agents/skills", "~/.cursor/skills"]);
  });
  it("收着时输入：有别的候选才自己展开;没有匹配的、填的正是唯一的候选时不展开", () => {
    const options = directoryOptions(catalog, "cursor", "global_directories", short);
    expect(opensWhileTyping(options, "~/.c", [], exact)).toBe(true);
    expect(opensWhileTyping(options, "~/.claude/skills", [], exact)).toBe(false);
    expect(opensWhileTyping(options, "/opt/my-agent/skills", [], exact)).toBe(false);
  });
});

describe("恢复默认", () => {
  const custom = "custom-ag" as AgentCode;
  const cursor = "cursor" as AgentCode;
  const claudeDefaults = { code: claude, name: "Claude Code", global_directories: ["/home/.claude/skills"], project_directories: [".claude/skills"], detect_directories: ["/home/.claude"] } as unknown as Agent;
  const claudeNow = { ...claudeDefaults, global_directories: ["/home/.claude-work/skills"], detect_directories: ["/home/.claude/"] } as Agent;
  const s = {
    agents: [claudeNow],
    skillScopes: [
      place(1, global(claude), { folder: "/home/.claude-work/skills/s1", linked_by: [global(cursor)] }),
      place(2, global(custom), { folder: "/home/.shared/skills/s2", shared_with: ["custom-b" as AgentCode] }),
      place(3, project(custom, 7), { shared_with: [AgentCode.AgentCodex, "custom-b" as AgentCode] }),
    ],
    groupInstalls: [],
  } as unknown as Snapshot;
  const plan = {
    reset: [
      { agent: claudeNow, defaults: claudeDefaults, added: true },
      { agent: { code: cursor, name: "My Cursor" }, defaults: { code: cursor, name: "Cursor" }, added: false },
    ],
    removed: [
      { agent: { code: custom, name: "antigravity-cli" }, added: true },
      { agent: { code: "custom-old", name: "Old" }, added: false },
    ],
    migrations: [{ place: { skill_id: 1, scope: global(claude) }, to: "/home/.claude/skills/s1", leaves: true }],
    kept: [{ skill_id: 3, scope: project(custom, 7), shared_with: [AgentCode.AgentCodex] }],
    removable: [{ skill_id: 2, scope: global(custom) }],
  } as unknown as ResetAgentsPlan;

  it("按技能库的计划写，不另算：已添加和候选池里的分开，写出改了哪几项，写法不同的同一个目录不算改了", () => {
    const r = agentReset(s, plan);
    expect(r.changed.map((c) => [c.agent.code, c.fields])).toEqual([[claude, ["global_directories"]]]);
    expect(r.changedPooled.map((a) => a.code)).toEqual([cursor]);
    expect(r.removed.map((a) => a.code)).toEqual([custom]);
    expect(r.removedPooled.map((a) => a.code)).toEqual(["custom-old"]);
    expect(r.nothing).toBe(false);
  });
  it("保留的按计划写谁在读(不算同批移除的手填 Agent);两个手填 Agent 共用的文件夹列在选删除时会删的里，按快照的巡检结果写", () => {
    const r = agentReset(s, plan);
    expect(r.kept.map((p) => [p.skill_id, p.shared_with])).toEqual([[3, [AgentCode.AgentCodex]]]);
    expect(r.own.map((p) => [p.skill_id, p.folder])).toEqual([[2, "/home/.shared/skills/s2"]]);
    expect(r.migrations.map((m) => [m.place.skill_id, m.to])).toEqual([[1, "/home/.claude/skills/s1"]]);
    expect(r.detached.length).toBe(1);
  });
  it("计划是空的时什么都不做", () => {
    expect(agentReset(s, { reset: [], removed: [], migrations: [], kept: [], removable: [] } as unknown as ResetAgentsPlan).nothing).toBe(true);
  });
});

describe("Agent 装在 Windows 本机还是 WSL 发行版", () => {
  it("第一次打开时这台电脑有 WSL 发行版、还没选过才先问，再按那里检测", () => {
    expect(asksWhere(["Ubuntu"], "")).toBe(true);
    expect(asksWhere([], "")).toBe(false);
    expect(asksWhere(["Ubuntu"], "Ubuntu")).toBe(false);
  });
});

describe("表单的按键", () => {
  const key = (k: string, composing = false) => ({ key: k, composing });
  it("中文输入法选词时按 Enter 是确定候选词：不提交表单，也不选下拉项", () => {
    expect(enterSubmits(key("Enter", true))).toBe(false);
    expect(enterSubmits(key("Enter"))).toBe(true);
    expect(directoryKey(key("Enter", true), { open: true, active: 0, count: 3 })).toEqual({ type: "none" });
    expect(directoryKey(key("ArrowDown", true), { open: true, active: 0, count: 3 })).toEqual({ type: "none" });
  });
  it("下拉开着时 ↑↓ 在候选间移动、首尾相接,Enter 填进高亮的那一项，没高亮时提交;收着时 ↑↓ 展开;Esc 只收起", () => {
    expect(directoryKey(key("ArrowDown"), { open: false, active: -1, count: 3 })).toEqual({ type: "open" });
    expect(directoryKey(key("ArrowDown"), { open: true, active: 2, count: 3 })).toEqual({ type: "move", active: 0 });
    expect(directoryKey(key("ArrowUp"), { open: true, active: -1, count: 3 })).toEqual({ type: "move", active: 2 });
    expect(directoryKey(key("Enter"), { open: true, active: 1, count: 3 })).toEqual({ type: "pick" });
    expect(directoryKey(key("Enter"), { open: true, active: -1, count: 3 })).toEqual({ type: "submit" });
    expect(directoryKey(key("Escape"), { open: true, active: 1, count: 3 })).toEqual({ type: "close" });
    expect(directoryKey(key("Escape"), { open: false, active: -1, count: 3 })).toEqual({ type: "none" });
  });
});

describe("技能目录重复或一个在另一个里面", () => {
  const windows = (foldsCase: boolean) => ({ foldsCase, backslashes: true });
  it("重复的、写法不同的同一个、一个在另一个里面的都不行", () => {
    expect(overlappingDirectories(["/home/.a/skills", "/home/.a/skills/"], exact)).toBe(true);
    expect(overlappingDirectories([".agents/skills", "./.agents/skills"], exact)).toBe(true);
    expect(overlappingDirectories(["/home/.a", "/home/.a/skills"], exact)).toBe(true);
    expect(overlappingDirectories(["C:\\Users\\me\\.a\\skills", "C:\\Users\\me\\.a"], windows(false))).toBe(true);
  });

  it("只差大小写的在不区分大小写的卷上算同一个，区分时是两个目录", () => {
    const dirs = ["C:\\Users\\me\\.agents\\skills", "C:\\Users\\me\\.Agents\\skills"];
    expect(overlappingDirectories(dirs, windows(true))).toBe(true);
    expect(overlappingDirectories(dirs, windows(false))).toBe(false);
  });

  it("macOS、Linux 上反斜杠不是分隔符", () => {
    expect(overlappingDirectories(["/home/team\\skills", "/home/team/skills"], { foldsCase: true, backslashes: false })).toBe(false);
  });

  it("技能库报的重复(含经符号链接才算同一个的)写成和界面检查一样的话，其余目录错误照常", () => {
    const overlap = { code: ErrorCode.InvalidArgument, message: "edit agent: directories must not repeat or contain one another", details: { field: "global_directories", paths: ["/home/.a", "/home/.b"] } } as LibraryError;
    expect(agentFieldError(overlap)).toBe(messages().agents.directoriesOverlap);
    const other = { code: ErrorCode.InvalidArgument, message: "edit agent: directory is inside an installed skill folder", details: { field: "global_directories" } } as LibraryError;
    expect(agentFieldError(other)).toContain("directory is inside an installed skill folder");
  });

  it("名字只是前缀相同的不算在里面", () => {
    expect(overlappingDirectories(["/home/.a/skills", "/home/.a/skills-old", "/home/.b/skills"], windows(true))).toBe(false);
  });
});

describe("改了出错的那一项之后报错还显示不显示", () => {
  const whole = { text: "填写全局技能目录" };
  const row = { text: "/opt/bad/skills：要在用户主目录里面", input: "/opt/bad/skills" };

  it("内容没变时保留：添加、去掉空行，重新选回原来的目录，只敲空格", () => {
    expect(keepsFieldError(whole, [""], ["", ""])).toBe(true);
    expect(keepsFieldError(row, ["~/.a/skills", "/opt/bad/skills", ""], ["~/.a/skills", "/opt/bad/skills"])).toBe(true);
    expect(keepsFieldError(row, ["~/.a/skills", "/opt/bad/skills"], ["~/.a/skills", "/opt/bad/skills"])).toBe(true);
    expect(keepsFieldError({ text: "填写名称" }, [""], [" "])).toBe(true);
  });

  it("整项的报错改了就不再显示", () => {
    expect(keepsFieldError(whole, [""], ["~"])).toBe(false);
    expect(keepsFieldError({ text: "填写名称" }, [""], ["C"])).toBe(false);
    expect(keepsFieldError({ text: "有两个目录是同一个，或一个在另一个里面" }, ["~/.a", "~/.a/skills"], ["~/.a"])).toBe(false);
  });

  it("写出了某一行的报错：那一行改掉或去掉才不再显示，改别的行时保留", () => {
    expect(keepsFieldError(row, ["/opt/bad/skills", "~/.b/skills"], ["/opt/bad/skills", "~/.c/skills"])).toBe(true);
    expect(keepsFieldError(row, ["/opt/bad/skills", "~/.b/skills"], ["/opt/bad/skills"])).toBe(true);
    expect(keepsFieldError(row, ["/opt/bad/skills", "~/.b/skills"], ["~/.bad/skills", "~/.b/skills"])).toBe(false);
    expect(keepsFieldError(row, ["/opt/bad/skills", "~/.b/skills"], ["~/.b/skills"])).toBe(false);
  });
});
