// 主界面的领域辅助：作用域身份、来源展示、安装状态与筛选，都从只读快照派生。
import {
  SameNameRule,
  ScopeKind,
  SkillScopeStatus,
  SourceType,
  UpdateCheckStatus,
  type Agent,
  type AgentCode,
  type Collection,
  type CollectionID,
  type Group,
  type GroupID,
  type ProjectID,
  type Scope,
  type Skill,
  type SkillID,
  type SkillScopeInspection,
  type Source,
  type UnmanagedSkill,
} from "@bindings/internal/library";
import { messages } from "./i18n";
import type { Snapshot } from "./library";

// ---------- 作用域 ----------

// ScopeKey 是作用域在界面状态里的身份，形如 "claude-code@0";0 表示 Agent 的全局级别。
export type ScopeKey = string;

// Place 是"地方":0 = 全局，否则为已登记项目 ID。
export type Place = number;

export function scopeKey(scope: { agent: AgentCode; project_id?: ProjectID }): ScopeKey {
  return `${scope.agent}@${scope.project_id ?? 0}`;
}

export function scopeOf(agent: AgentCode, place: Place): Scope {
  return place ? { kind: ScopeKind.ScopeProject, agent, project_id: place } : { kind: ScopeKind.ScopeGlobal, agent };
}

export function parseScope(key: ScopeKey): Scope {
  const at = key.lastIndexOf("@");
  return scopeOf(key.slice(0, at) as AgentCode, Number(key.slice(at + 1)));
}

export function placeOf(scope: Scope): Place {
  return scope.project_id ?? 0;
}

export function agentName(s: Snapshot, code: AgentCode): string {
  return s.agents.find((a) => a.code === code)?.name ?? code;
}

export function projectName(s: Snapshot, id: ProjectID): string {
  return s.projects.find((p) => p.id === id)?.name ?? "";
}

// skillName / groupName 是库里技能、组的名字;已被移除的写成 #ID。
export const skillName = (s: Snapshot, id: SkillID) => s.skills.find((x) => x.id === id)?.name ?? `#${id}`;

export const groupName = (s: Snapshot, id: GroupID) => s.groups.find((g) => g.id === id)?.name ?? `#${id}`;

export function scopeLabel(s: Snapshot, scope: Scope): string {
  return `${agentNameIn(s, scope.agent, placeOf(scope))} · ${scope.project_id ? projectName(s, scope.project_id) : messages().model.global}`;
}

// shortPath 把用户主目录缩写成 ~。
export function shortPath(s: Snapshot, path: string): string {
  return homePath(s.info.home_dir, path);
}

// homePath 把主目录下的路径缩写成 ~;Windows 与 WSL 发行版(\\wsl.localhost\...)的路径用反斜杠分隔。
export function homePath(home: string | undefined, path: string): string {
  return home && (path === home || path.startsWith(home + "/") || path.startsWith(home + "\\")) ? "~" + path.slice(home.length) : path;
}

// outsideLinkNote 是指向 Agent 目录以外的符号链接入库时的说明:之后在 app 里更新会换掉链接。
export const outsideLinkNote = (target: string) => messages().model.outsideLinkNote(target);

// linkNote 是库要写一处符号链接时的说明:写成独立的文件夹，不再写进链接指向的地方。
export const linkNote = (s: Snapshot, target: string) => messages().model.linkNote(shortPath(s, target));

// agentShown 是 Agent 的显示勾选：用户勾选过的按勾选，没勾选过的检测到的和自定义的显示。
// 显示只影响选择安装位置的地方(右栏与胶囊弹层的"其他 Agent"、"安装到…");已经装了技能的 Agent 照常列出。
export function agentShown(shown: Partial<Record<string, boolean>> | null | undefined, agent: Agent): boolean {
  return shown?.[agent.code] ?? (agent.detected || agent.custom);
}

// agentsIn 把某个地方的 Agent 分成直接列出的和折进"其他 N 个 Agent"的(右栏与作用域弹层):
// 在这里装有技能的、以及检测到且勾选显示的直接列出;勾选了显示但没检测到的折起来;没勾选显示、也没装技能的不列出。
// 项目里共用一个目录的几个 Agent 合成一处(见 mergeProjectAgents),已经列出的那一处不再折进"其他"。
export function agentsIn(s: Snapshot, place: Place): { open: Agent[]; folded: Agent[] } {
  const used = agentsUsedIn(s, place);
  const open = s.agents.filter((a) => used.includes(a) || (a.detected && agentShown(s.agentsShown, a)));
  const folded = s.agents.filter((a) => !open.includes(a) && agentShown(s.agentsShown, a));
  if (!place) return { open, folded };
  const merged = mergeProjectAgents(s, open);
  return { open: merged, folded: mergeAgentsExcept(s, place, folded, merged) };
}

// agentsUsedIn 是某个地方实际装有技能的 Agent,按 Agent 列表顺序;项目里共用一个目录的几个合成一处。
export function agentsUsedIn(s: Snapshot, place: Place) {
  const used = s.agents.filter((a) => s.skillScopes.some((p) => p.scope.agent === a.code && placeOf(p.scope) === place));
  return place ? mergeProjectAgents(s, used) : used;
}

// ---------- 项目里共用一个目录的 Agent ----------
// 项目里几个 Agent 的技能目录相同(如都读 .agents/skills)时，装在其中一个，其余几个也算已装，卸载也一起卸载。
// 界面上它们合成一处：右栏、作用域弹层、顶栏胶囊、技能详情的安装表、"安装到…"和各处逐处列出的地方都只列一次，
// 名字是这台机器上显示的那几个 Agent 的名字;点选、拖放、安装都落在其中第一个(代表)的作用域上。
// 全局不合：全局目录大多各是各的，少数共用的照旧一个 Agent 一行，卸载前提示还被谁共用。

type AgentList = Pick<Snapshot, "agents" | "agentsShown">;

// projectSharers 是项目里和这个 Agent 读同一个目录的全部 Agent(含它自己),按 Agent 列表顺序;不和别的 Agent 共用时为空。
export function projectSharers(s: AgentList, code: AgentCode): Agent[] {
  const self = s.agents.find((a) => a.code === code);
  if (!self) return [];
  const all = s.agents.filter((a) => a.project_directory === self.project_directory);
  return all.length > 1 ? all : [];
}

// projectNamed 是合成的一处用来起名、画图标的 Agent:共用的几个里这台机器上显示的;一个都不显示时取第一个。
export function projectNamed(s: AgentList, code: AgentCode): Agent[] {
  const all = projectSharers(s, code);
  const shown = all.filter((a) => agentShown(s.agentsShown, a));
  return shown.length ? shown : all.slice(0, 1);
}

// projectLead 是代表合成的一处的 Agent;不共用时就是它自己。
export function projectLead(s: AgentList, code: AgentCode): AgentCode {
  return projectNamed(s, code)[0]?.code ?? code;
}

export function sameProjectFolder(s: AgentList, a: AgentCode, b: AgentCode): boolean {
  return a === b || projectSharers(s, a).some((x) => x.code === b);
}

// mergeProjectAgents 把项目里的一串 Agent 合成一处一个(换成代表的 Agent),保持第一次出现的顺序。
export function mergeProjectAgents(s: AgentList, agents: Agent[]): Agent[] {
  const out: Agent[] = [];
  for (const a of agents) {
    const lead = s.agents.find((x) => x.code === projectLead(s, a.code)) ?? a;
    if (!out.includes(lead)) out.push(lead);
  }
  return out;
}

// mergeAgentsExcept 是某个地方还没列出的 Agent:项目里合成一处一个，和 listed 里某一处共用目录的去掉。
export function mergeAgentsExcept(s: AgentList, place: Place, agents: Agent[], listed: Agent[]): Agent[] {
  if (!place) return agents.filter((a) => !listed.includes(a));
  return mergeProjectAgents(s, agents).filter((a) => !listed.some((o) => sameProjectFolder(s, o.code, a.code)));
}

// agentNameIn 是 Agent 在某个地方的名字：项目里合成的一处是那几个 Agent 的名字。
export function agentNameIn(s: AgentList, code: AgentCode, place: Place): string {
  const named = place ? projectNamed(s, code) : [];
  if (named.length) return messages().model.agentNames(named.map((a) => a.name));
  return s.agents.find((a) => a.code === code)?.name ?? code;
}

// projectFolderNote 是合成的一处的悬停说明：读项目里这个目录的全部 Agent(含这台机器上不显示的);不合成时为 undefined。
export function projectFolderNote(s: AgentList, code: AgentCode, place: Place): string | undefined {
  const all = place ? projectSharers(s, code) : [];
  if (!all.length) return undefined;
  return messages().model.projectFolderNote(all[0].project_directory, all.map((a) => a.name));
}

// sharedWithNote 是一处安装要另外说明的共用 Agent:全局照旧是共用这个文件夹的其他 Agent;
// 项目里共用一个目录的几个 Agent 已合成一处、名字里写着它们，不再另说。
export function sharedWithNote(p: Pick<SkillScopeInspection, "scope" | "shared_with">): AgentCode[] {
  return p.scope.project_id ? [] : (p.shared_with ?? []);
}

// sameMergedScope 是两个作用域合成后是否是同一处。
export function sameMergedScope(s: AgentList, a: Scope, b: Scope): boolean {
  return mergedScopeKey(s, a) === mergedScopeKey(s, b);
}

// mergedScopeKey 是作用域合成后的身份：项目里共用一个目录的几个作用域是同一个。
export function mergedScopeKey(s: AgentList, scope: Scope): ScopeKey {
  return scopeKey({ agent: scope.project_id ? projectLead(s, scope.agent) : scope.agent, project_id: scope.project_id });
}

// mergeProjectPlaces 把逐处列出的安装里，项目中共用一个文件夹的几条合成一条(留第一条)。
export function mergeProjectPlaces<T extends { skill_id?: number; scope: Scope }>(s: AgentList, items: T[]): T[] {
  const seen = new Set<string>();
  return items.filter((p) => {
    const key = `${p.skill_id ?? ""}|${mergedScopeKey(s, p.scope)}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

// ---------- 安装 ----------

export function skillScopesOf(s: Snapshot, skillID: number): SkillScopeInspection[] {
  return s.skillScopes.filter((p) => p.skill_id === skillID);
}

export function skillScopesIn(s: Snapshot, key: ScopeKey): SkillScopeInspection[] {
  return s.skillScopes.filter((p) => scopeKey(p.scope) === key);
}

// unmanagedIn 是作用域的技能目录里不是库装的技能文件夹(未纳管技能)。
export function unmanagedIn(s: Snapshot, key: ScopeKey): UnmanagedSkill[] {
  return s.unmanaged.filter((u) => scopeKey(u.scope) === key);
}

// sameNameNote 说明作用域里的一个技能文件夹和同一个 Agent 全局 / 某个已登记项目里的文件夹同名时会怎样:
// 按 Agent 已知的规则说出后果(Claude Code 里全局的生效，Codex 里两份都会出现),没有已知规则的只说同名。
// 同名按 SKILL.md 里的 name,两处的文件夹名可以不同。folder 是这个作用域里那个技能文件夹;没有同名时返回 undefined。
// title 列出另一处的文件夹。
export type SameNameKind = "managed" | "unmanaged";

export function sameNameNote(s: Snapshot, key: ScopeKey, folder: string): { text: string; title: string } | undefined {
  const m = messages().model;
  const scope = parseScope(key);
  const agent = s.agents.find((a) => a.code === scope.agent);
  const rule = agent?.same_name ?? SameNameRule.SameNameUnknown;
  const who = agent?.name ?? scope.agent;
  if (scope.project_id) {
    const pairs = s.sameName.filter((p) => p.agent === scope.agent && p.project_id === scope.project_id && p.project.folder === folder);
    if (pairs.length === 0) return undefined;
    const kinds = [...new Set(pairs.map((p): SameNameKind => (p.global.skill_id ? "managed" : "unmanaged")))];
    const name = pairs[0].name;
    const text = {
      [SameNameRule.SameNameGlobalWins]: m.sameNameGlobalWins(name, kinds, who),
      [SameNameRule.SameNameBothShown]: m.sameNameGlobalBoth(name, kinds, who),
      [SameNameRule.SameNameUnknown]: m.sameNameGlobalUnknown(name, kinds),
    }[rule];
    return { text, title: pairs.map((p) => m.sameNameGlobalFolder(shortPath(s, p.global.folder))).join("\n") };
  }
  const pairs = s.sameName.filter((p) => p.agent === scope.agent && p.global.folder === folder);
  if (pairs.length === 0) return undefined;
  const name = pairs[0].name;
  const projects = [...new Set(pairs.map((p) => projectName(s, p.project_id)))];
  const text = {
    [SameNameRule.SameNameGlobalWins]: m.sameNameProjectWins(projects, name, who),
    [SameNameRule.SameNameBothShown]: m.sameNameProjectBoth(projects, name, who),
    [SameNameRule.SameNameUnknown]: m.sameNameProjectUnknown(projects, name),
  }[rule];
  return { text, title: pairs.map((p) => m.sameNameProjectFolder(projectName(s, p.project_id), shortPath(s, p.project.folder))).join("\n") };
}

// sameNameInLibrary 是库里和它同名的其他技能(同名但内容不同的不合并，见入库)。
export function sameNameInLibrary(s: Snapshot, skill: Pick<Skill, "id" | "name">): Skill[] {
  return s.skills.filter((x) => x.name === skill.name && x.id !== skill.id);
}

// sameNameFetchNote 说明入库一个和库里技能同名的技能会怎样：不带来源时和库里所有技能比，带来源时只和这个来源里同名的比。
export function sameNameFetchNote(name: string, sourced: boolean): string {
  return sourced ? messages().model.sameNameFetchSourced(name) : messages().model.sameNameFetchUnsourced(name);
}

// installedWhere 是技能装在哪里，供分辨库里同名的几个技能:places 是前两处("Claude Code · 全局"、"Codex · proj-a"),
// total 是一共几处(超过两处时文案写"等 N 处",为 0 时写"没装在任何地方")。
// folderPlaces 是和 places 同一个文件夹的各处(含共用这个目录的 Agent),删文件夹时它们一起没了。
export function folderPlaces(s: Snapshot, places: SkillScopeInspection[]): SkillScopeInspection[] {
  const folders = new Set(places.map((p) => `${p.skill_id}|${p.folder}`));
  return s.skillScopes.filter((p) => folders.has(`${p.skill_id}|${p.folder}`));
}

export interface InstalledWhere {
  places: string[];
  total: number;
}

export function installedWhere(s: Snapshot, skillID: number): InstalledWhere {
  const places = mergeProjectPlaces(s, skillScopesOf(s, skillID)).map((p) => scopeLabel(s, p.scope));
  return { places: places.slice(0, 2), total: places.length };
}

export function skillScopeIn(s: Snapshot, skillID: number, key: ScopeKey): SkillScopeInspection | undefined {
  return s.skillScopes.find((p) => p.skill_id === skillID && scopeKey(p.scope) === key);
}

// folderKey 是装出去的实际文件夹:几个 Agent 共用一个目录时，它们的这一处是同一个文件夹，只能一起换、一起装回去或卸载。
export const folderKey = (p: SkillScopeInspection) => `${p.skill_id}|${p.folder}`;

export function needsReinstall(p: SkillScopeInspection): boolean {
  return p.status !== SkillScopeStatus.SkillScopeCurrent;
}

export function skillScopeStatus(status: SkillScopeStatus): { label: string; tone: "ok" | "warn" | "bad" } {
  const m = messages();
  return {
    [SkillScopeStatus.SkillScopeCurrent]: { label: m.model.statusCurrent, tone: "ok" as const },
    [SkillScopeStatus.SkillScopeStale]: { label: m.model.statusStale, tone: "warn" as const },
    [SkillScopeStatus.SkillScopeModified]: { label: m.model.statusModified, tone: "bad" as const },
    [SkillScopeStatus.SkillScopeMissing]: { label: m.model.statusMissing, tone: "bad" as const },
    [SkillScopeStatus.SkillScopeRootMissing]: { label: m.model.statusRootMissing, tone: "bad" as const },
    [SkillScopeStatus.$zero]: { label: m.common.unknown, tone: "warn" as const },
  }[status];
}

// groupStateIn 是组在某个作用域的组安装状态：组安装是独立记录(空组也可以有),有记录即为 on。
// 成员各自的直接安装不算组安装。
export function groupStateIn(s: Snapshot, group: Group, key: ScopeKey): "on" | "off" {
  return s.groupInstalls.some((a) => a.group_id === group.id && scopeKey(a.scope) === key) ? "on" : "off";
}

export function viaGroupCount(s: Snapshot, group: Group, key: ScopeKey): number {
  return (group.members ?? []).filter((id) => skillScopeIn(s, id, key)?.groups?.includes(group.id)).length;
}

// ---------- 更新 ----------
// 计入角标的规则由技能库给出(UpdateCheck.pending):有新版本且没跳过、来源里已删除且没选继续保留、推测出改名且没跳过;
// 本地目录来源、跳过的改动、来源里的新技能都不计入。侧边栏的更新计数、更新列表和 Dock 角标都按它算。
// 本地目录来源的作者就是用户自己：不进更新列表，技能上只有"重新读取"。

export function isLocal(source: Source): boolean {
  return source.type === SourceType.SourceLocal;
}

// noSource 是没有来源的技能：从作用域入库的未纳管技能，作者就是用户自己。不在任何集合里，不检查更新，
// 只能用目录里的修改更新(在装出去的地方改了以后，那一处会出现"用这里的修改更新")。
export function noSource(source: Source): boolean {
  return source.type === SourceType.$zero;
}

export function pending(skill: Skill): boolean {
  return !!skill.last_update_check?.pending;
}

// updateAvailable 是来源里这个技能有新版本，跳过了的也算(更新计划里照样能勾上)。
export function updateAvailable(skill: Skill): boolean {
  return !isLocal(skill.source) && skill.last_update_check?.status === UpdateCheckStatus.UpdateAvailable;
}

// renamedTo 是推测这个技能在来源里改成的新名字;没有推测时为空。
export function renamedTo(skill: Skill): string {
  return skill.last_update_check?.renamed_to ?? "";
}

// deletedInSource 是来源里已删除、也没推测出改名。
export function deletedInSource(skill: Skill): boolean {
  return skill.last_update_check?.status === UpdateCheckStatus.UpdateDeleted && !renamedTo(skill);
}

// hasUpdate 是能从这个技能打开更新计划：有新版本或推测出改名，跳过了的也算。
export function hasUpdate(skill: Skill): boolean {
  return updateAvailable(skill) || !!renamedTo(skill);
}

export function collectionSkills(s: Snapshot, c: Collection): Skill[] {
  const ids = new Set(c.skills ?? []);
  return s.skills.filter((x) => ids.has(x.id));
}

// collectionName 是集合在界面上的名字：集合里只有一个技能时就是技能名，不出现集合这一层。
export function collectionName(s: Snapshot, collectionID: CollectionID): string {
  const c = s.collections.find((x) => x.id === collectionID);
  if (!c) return messages().update.collectionFallback(collectionID);
  const skills = collectionSkills(s, c);
  return skills.length === 1 ? skills[0].name : sourceLabel(s, c.source);
}

// updatableCollections 是有计入角标的技能的集合，按集合列表顺序。
export function updatableCollections(s: Snapshot): Collection[] {
  return s.collections.filter((c) => collectionSkills(s, c).some(pending));
}

// checkableCollections 是检查更新的范围：本地目录来源不检查。
export function checkableCollections(s: Snapshot): CollectionID[] {
  return s.collections.filter((c) => !isLocal(c.source)).map((c) => c.id);
}

export function shortRevision(revision: string): string {
  return revision.slice(0, 7);
}

// revisionSummary 说明集合里的技能在哪个来源版本上：都在同一个时是 same("在 a1b2c3");
// 不在同一个时是 mixed,直接写"pdf 在 d4e5f6,其余 4 个在 a1b2c3",不起专门的名字。
// 来源版本未知的(入库时和来源里的不一样)不算进任何一个，单独写"grill-me 的来源版本未知";都未知时是 unknown("来源版本未知")。
// 本地目录来源与没有来源的技能为 undefined。文案见 revisionText。
export type RevisionSummary =
  | { kind: "unknown" }
  | { kind: "same"; revision: string }
  | { kind: "mixed"; others: { names: string[]; revision: string }[]; unknown: string[]; rest: number; revision: string };

export function revisionSummary(skills: Skill[]): RevisionSummary | undefined {
  const tracked = skills.filter((x) => !isLocal(x.source) && !noSource(x.source));
  const unknown = tracked.filter((x) => !x.revision);
  const byRevision = new Map<string, Skill[]>();
  for (const x of tracked) {
    if (!x.revision) continue;
    const key = shortRevision(x.revision);
    byRevision.set(key, [...(byRevision.get(key) ?? []), x]);
  }
  const names = (list: Skill[]) => list.map((x) => x.name);
  if (byRevision.size === 0) return unknown.length ? { kind: "unknown" } : undefined;
  const groups = [...byRevision.entries()].sort((a, b) => b[1].length - a[1].length);
  const [main, ...others] = groups;
  if (!others.length && !unknown.length) return { kind: "same", revision: main[0] };
  return {
    kind: "mixed",
    others: others.map(([revision, list]) => ({ names: names(list), revision })),
    unknown: names(unknown),
    rest: main[1].length,
    revision: main[0],
  };
}

export function revisionText(summary: RevisionSummary | undefined): string {
  const m = messages().model;
  switch (summary?.kind) {
    case "unknown":
      return m.revisionUnknown;
    case "same":
      return m.revisionSame(summary.revision);
    case "mixed":
      return m.revisionMixed(summary);
    default:
      return "";
  }
}

// ---------- 来源 ----------

export function sourceKindLabel(type: SourceType): string {
  return {
    [SourceType.SourceGitHub]: "GitHub",
    [SourceType.SourceGit]: "Git",
    [SourceType.SourceURL]: "URL",
    [SourceType.SourceWellKnown]: "well-known",
    [SourceType.SourceLocal]: messages().model.kindLocal,
    [SourceType.$zero]: messages().model.noSource,
  }[type];
}

function lastSegment(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).pop() ?? path;
}

// sourceLabel 是集合在左栏里的名字：GitHub 取 owner/repo,Git 仓库取主机加仓库路径,well-known 取站点主机加路径,
// URL 取文件名，本地取缩写路径。
export function sourceLabel(s: Snapshot, source: Source): string {
  switch (source.type) {
    case SourceType.SourceGitHub:
    case SourceType.SourceGit:
    case SourceType.SourceWellKnown:
      return source.label;
    case SourceType.SourceURL:
      return lastSegment(source.label);
    case SourceType.SourceLocal:
      return shortPath(s, source.label);
    case SourceType.$zero:
      return messages().model.noSource;
    default:
      return messages().model.unknownSource;
  }
}

// sourceShort 是卡片上的来源短名：GitHub 取 owner,Git 仓库取主机后的第一段(owner 或 group),well-known 取站点主机,
// URL 取文件名，本地一律"自研"。
export function sourceShort(s: Snapshot, source: Source): string {
  switch (source.type) {
    case SourceType.SourceGitHub:
      return sourceLabel(s, source).split("/")[0];
    case SourceType.SourceGit: {
      const parts = source.label.split("/");
      return parts[1] ?? parts[0];
    }
    case SourceType.SourceWellKnown:
      return source.label.split("/")[0];
    case SourceType.SourceURL:
      return sourceLabel(s, source).replace(/\.zip$/, "");
    default:
      return sourceKindLabel(source.type);
  }
}

// ---------- 左栏筛选 ----------

export type SkillFilter =
  | { kind: "all" }
  | { kind: "updates" }
  | { kind: "collection"; collectionID: CollectionID }
  | { kind: "group"; groupID: GroupID };

export function sameFilter(a: SkillFilter, b: SkillFilter): boolean {
  if (a.kind === "collection" && b.kind === "collection") return a.collectionID === b.collectionID;
  if (a.kind === "group" && b.kind === "group") return a.groupID === b.groupID;
  return a.kind === b.kind;
}

// resolveFilter 把指向已不存在的集合或组、或已没有更新的筛选退回"所有技能"。
export function resolveFilter(s: Snapshot, filter: SkillFilter): SkillFilter {
  if (filter.kind === "collection" && !s.collections.some((c) => c.id === filter.collectionID)) return { kind: "all" };
  if (filter.kind === "group" && !s.groups.some((g) => g.id === filter.groupID)) return { kind: "all" };
  if (filter.kind === "updates" && !updatableCollections(s).length) return { kind: "all" };
  return filter;
}

export function filterTitle(s: Snapshot, filter: SkillFilter): string {
  switch (filter.kind) {
    case "collection": {
      const c = s.collections.find((c) => c.id === filter.collectionID);
      return c ? sourceLabel(s, c.source) : messages().model.allSkills;
    }
    case "group":
      return s.groups.find((g) => g.id === filter.groupID)?.name ?? messages().model.allSkills;
    case "updates":
      return messages().model.updates;
    default:
      return messages().model.allSkills;
  }
}

export interface ListQuery {
  filter: SkillFilter;
  search: string;
  installedOnlyIn: ScopeKey | null; // 仅显示已安装在该作用域的技能
}

export function visibleSkills(s: Snapshot, q: ListQuery): Skill[] {
  let skills = s.skills;
  const f = q.filter;
  if (f.kind === "collection") {
    const ids = new Set(s.collections.find((c) => c.id === f.collectionID)?.skills ?? []);
    skills = skills.filter((x) => ids.has(x.id));
  }
  if (f.kind === "group") {
    const ids = new Set(s.groups.find((g) => g.id === f.groupID)?.members ?? []);
    skills = skills.filter((x) => ids.has(x.id));
  }
  if (f.kind === "updates") skills = skills.filter(pending);
  const text = q.search.trim().toLowerCase();
  if (text) skills = skills.filter((x) => (x.name + " " + x.description).toLowerCase().includes(text));
  const key = q.installedOnlyIn;
  if (key) skills = skills.filter((x) => skillScopeIn(s, x.id, key));
  return skills;
}

// visibleUnmanaged 是选中作用域、看"所有技能"时和库里的技能列在一起的未纳管技能;按组、集合、更新筛选时不列。
export function visibleUnmanaged(s: Snapshot, filter: SkillFilter, search: string, scope: ScopeKey | null): UnmanagedSkill[] {
  if (!scope || filter.kind !== "all") return [];
  const text = search.trim().toLowerCase();
  return unmanagedIn(s, scope).filter((u) => (u.name + " " + (u.description ?? "")).toLowerCase().includes(text));
}
