// 各页面列什么、怎样分节、数到几，都从只读快照派生。
// 主界面中间有五种页面：技能库(我有什么)、更新(要处理的事)、作用域(Agent 在这里看得到什么)、
// 项目(这个项目里各 Agent 装了什么)，以及一个集合或组的关系图(这些技能之间谁提到谁)。
// 集合和组不是筛选，是技能库页里的分节;左栏点它们只是跳到那一节，关系图从节标题或左栏右键菜单打开。
// 集合与组的先后由技能库给出(ListCollections / ListGroups 按用户拖出来的先后列出),左栏、技能库页与作用域页的分节、
// 更新列表、作用域摘要里的组都直接按快照里的顺序。
import type { Agent, AgentCode, CollectionID, GroupID, ProjectID, Scope, Skill, SkillID, UnmanagedSkill } from "@bindings/internal/library";
import { messages } from "./i18n";
import { placesIn } from "./installs";
import type { Snapshot } from "./library";
import { globalScopesSeenFrom, mergeAgents, scopeLabel } from "./places";
import { parseScope, placeOf, projectName, scopeKey, scopeOf, type Place, type ScopeKey } from "./scope";
import { collectionSkills, noSource, sourceLabel } from "./source";
import { todoSkills, updatableCollections } from "./source-state";
import { extraCopy, splitIdentical, unmanagedIn } from "./unmanaged";
import { collator } from "./utils";

// ---------- 页面 ----------

// GraphSet 是关系图画的那组技能：一个集合或一个组。
export type GraphSet = { kind: "collection"; id: CollectionID } | { kind: "group"; id: GroupID };

export type Page =
  | { kind: "all" }
  | { kind: "updates" }
  | { kind: "scope"; scope: ScopeKey }
  | { kind: "project"; project: ProjectID }
  | { kind: "graph"; set: GraphSet };

// Arrange 是技能库页与作用域页按什么分节。
export type Arrange = "collection" | "group";

export function samePage(a: Page, b: Page): boolean {
  if (a.kind === "scope" && b.kind === "scope") return a.scope === b.scope;
  if (a.kind === "project" && b.kind === "project") return a.project === b.project;
  if (a.kind === "graph" && b.kind === "graph") return a.set.kind === b.set.kind && a.set.id === b.set.id;
  return a.kind === b.kind;
}

// resolvePage 把已没有更新的更新页、项目或 Agent 已被移除的作用域页、项目已被注销的项目页、集合或组已不在的关系图退回技能库。
export function resolvePage(s: Snapshot, page: Page): Page {
  if (page.kind === "updates" && !updatableCollections(s).length) return { kind: "all" };
  if (page.kind === "scope") {
    const sc = parseScope(page.scope);
    if ((sc.project_id && !s.projects.some((p) => p.id === sc.project_id)) || !s.agents.some((a) => a.code === sc.agent)) return { kind: "all" };
  }
  if (page.kind === "project" && !s.projects.some((p) => p.id === page.project)) return { kind: "all" };
  if (page.kind === "graph" && !graphMembers(s, page.set)) return { kind: "all" };
  return page;
}

// graphMembers 是关系图那组技能的 ID;集合或组已不在时为 undefined。
function graphMembers(s: Snapshot, set: GraphSet): SkillID[] | undefined {
  const found = set.kind === "collection" ? s.collections.find((c) => c.id === set.id)?.skills : s.groups.find((g) => g.id === set.id)?.members;
  if (found) return found;
  return set.kind === "group" && s.groups.some((g) => g.id === set.id) ? [] : undefined;
}

// graphSetName 是关系图页的标题：集合写来源，组写组名。
function graphSetName(s: Snapshot, set: GraphSet): string {
  if (set.kind === "group") return s.groups.find((g) => g.id === set.id)?.name ?? "";
  const c = s.collections.find((x) => x.id === set.id);
  return c ? sourceLabel(s, c.source) : "";
}

export function pageTitle(s: Snapshot, page: Page): string {
  const m = messages().model;
  if (page.kind === "updates") return m.updates;
  if (page.kind === "scope") return scopeLabel(s, parseScope(page.scope));
  if (page.kind === "project") return projectName(s, page.project);
  if (page.kind === "graph") return graphSetName(s, page.set);
  return m.library;
}

// ---------- 搜索与行的先后 ----------

// matches 是搜索：名字或描述里含有输入的文字，不分大小写;没输入时都算。
export function matches(search: string, item: { name: string; description?: string }): boolean {
  const text = search.trim().toLowerCase();
  return !text || (item.name + " " + (item.description ?? "")).toLowerCase().includes(text);
}

// changedAt 是技能最近一次变化：更新带来新版本的时间，从没更新过时是入库时间。Go 的零值时间当作没有。
function changedAt(skill: Skill): Date | undefined {
  const valid = (v: string | undefined) => (v && new Date(v).getFullYear() > 1 ? new Date(v) : undefined);
  return valid(skill.updated_at) ?? valid(skill.fetched_at);
}

// sortSkills 是列表里技能的顺序：先按来源，和左栏集合的先后一致(没有来源的在最后),同一来源里按名字，同名的最近变化的在前。
export function sortSkills(s: Snapshot, skills: Skill[]): Skill[] {
  const at = new Map(s.collections.map((c, i) => [c.id, i]));
  const source = (x: Skill) => (noSource(x.source) ? Infinity : (at.get(x.collection_id) ?? s.collections.length));
  const time = (x: Skill) => changedAt(x)?.getTime() ?? 0;
  return [...skills].sort(
    (a, b) =>
      source(a) - source(b) ||
      collator.compare(a.name, b.name) ||
      time(b) - time(a),
  );
}

// ---------- 技能库页的分节 ----------

// sectionKey 是一节的身份：收起、展开"没装的"、左栏跳转都按它。作用域页的节带上作用域，收起、展开各作用域分开记，
// 也不和技能库页的同一节连在一起;作用域页下面那块(没装在这里的)的节再带上 not-here,和上面那块的同一节分开记。
export const sectionKey = (kind: "collection" | "group" | "rest", id: number | string, scope?: ScopeKey, notHere = false) =>
  `${scope ? `${scope}:` : ""}${notHere ? "not-here:" : ""}${kind}-${id}`;

// SkillSection 是技能库页、作用域页的一节：一个集合、一个组，或最后的"没有来源" / "没有归组"(kind 为 rest,id 为 0)。
// key 是节的身份(见 sectionKey),最后一节是 "rest-collection" / "rest-group"。skills 是要列的(搜索时只留匹配的),
// all 是不搜索时这一节的全部技能，节标题按它数个数。
export interface SkillSection {
  key: string;
  kind: "collection" | "group" | "rest";
  id: number;
  skills: Skill[];
  all: Skill[];
}

// arrangeSkills 把技能分节(skills 默认是库里的全部;scope 是作用域页，节的身份带上作用域,notHere 是作用域页下面那块)。按集合时每个来源一节，按左栏集合的先后，没有来源的合进最后的"没有来源";
// 按组时每个组一节，按左栏组的先后，一个技能在几个组里就出现几次，不在任何组里的合进"没有归组"。节里的技能按 sortSkills 排。
// 搜索时只留有结果的节;emptyGroups 时不搜索也列出空组，好往里拖技能。
function arrangeSkills(
  s: Snapshot,
  arrange: Arrange,
  search: string,
  {
    skills: all = s.skills,
    emptyGroups = true,
    scope,
    notHere = false,
  }: { skills?: Skill[]; emptyGroups?: boolean; scope?: ScopeKey; notHere?: boolean } = {},
): SkillSection[] {
  const hit = (x: Skill) => matches(search, x);
  const ids = new Set(all.map((x) => x.id));
  const sections: SkillSection[] = [];
  const placed = new Set<SkillID>();
  const push = (kind: SkillSection["kind"], id: number, skills: Skill[]) => {
    const sorted = sortSkills(s, skills);
    sections.push({ key: sectionKey(kind, kind === "rest" ? arrange : id, scope, notHere), kind, id, skills: sorted.filter(hit), all: sorted });
  };
  if (arrange === "collection") {
    for (const c of s.collections) {
      const skills = collectionSkills(s, c).filter((x) => ids.has(x.id));
      skills.forEach((x) => placed.add(x.id));
      push("collection", c.id, skills);
    }
  } else {
    for (const g of s.groups) {
      const members = new Set(g.members ?? []);
      const skills = all.filter((x) => members.has(x.id));
      skills.forEach((x) => placed.add(x.id));
      push("group", g.id, skills);
    }
  }
  push("rest", 0, all.filter((x) => !placed.has(x.id)));
  return sections.filter((x) => x.skills.length > 0 || (emptyGroups && !search.trim() && x.kind === "group"));
}

// ---------- 作用域页 ----------

// ScopeListing 是作用域页要列的，分上下两块。上面是 Agent 在这里看得到的：装在这里的库里的技能按集合或组分节(hereSections)、
// 装在这里的未纳管技能，以及项目里来自同一个 Agent 全局的(Agent 在项目里也看得到，不再出现在别处)。
// 目录列表相同、合成一处的几个 Agent 合成一处时，来自全局的是这几个 Agent 全局的合起来。
// 下面是库里没装在这里的技能，同样按集合或组分节(notHereSections);notHereSkills 是其中的技能，按节的先后、一个技能只算一次。
// 搜索时都只留匹配的;hereCount 是上面那块里库里的技能个数，含和库里一样、不是库装的那几个(identicalIn)。
export interface ScopeListing {
  hereSections: SkillSection[];
  hereUnmanaged: UnmanagedSkill[];
  global: Skill[];
  globalUnmanaged: UnmanagedSkill[];
  notHereSections: SkillSection[];
  notHereSkills: Skill[];
  hereCount: number;
}

function scopeListing(s: Snapshot, key: ScopeKey, arrange: Arrange, search: string): ScopeListing {
  const hit = (x: { name: string; description?: string }) => matches(search, x);
  // 和库里某个技能一样的未纳管文件夹(identicalIn)就是那个技能：列在上面库里的技能里，不进"不在技能库里"。
  const split = splitIdentical(unmanagedIn(s, key));
  const hereIDs = new Set([...placesIn(s, key).map((p) => p.skill_id), ...split.identical.map((u) => u.skill_id!)]);
  const globalKeys = globalScopesSeenFrom(s, key);
  const globalIDs = new Set(
    s.skillScopes.filter((p) => globalKeys.includes(scopeKey(p.scope)) && !hereIDs.has(p.skill_id)).map((p) => p.skill_id),
  );
  const folders = new Set<string>();
  // 全局装着的技能多出来的那一份(extraCopy)已经随那个技能列在来自全局里，不另列一行。
  const globalUnmanaged = s.unmanaged.filter((u) => {
    if (!globalKeys.includes(scopeKey(u.scope)) || folders.has(u.folder) || extraCopy(s, u)) return false;
    folders.add(u.folder);
    return true;
  });
  const byName = (a: { name: string }, b: { name: string }) => collator.compare(a.name, b.name);
  const here = s.skills.filter((x) => hereIDs.has(x.id));
  const notHereSections = arrangeSkills(s, arrange, search, {
    skills: s.skills.filter((x) => !hereIDs.has(x.id) && !globalIDs.has(x.id)),
    emptyGroups: false,
    scope: key,
    notHere: true,
  });
  return {
    hereSections: arrangeSkills(s, arrange, search, { skills: here, emptyGroups: false, scope: key }),
    hereUnmanaged: split.unmanaged.filter(hit).sort(byName),
    global: sortSkills(s, s.skills.filter((x) => globalIDs.has(x.id) && hit(x))),
    globalUnmanaged: globalUnmanaged.filter(hit).sort(byName),
    notHereSections,
    notHereSkills: [...new Set(notHereSections.flatMap((x) => x.skills))],
    hereCount: here.filter(hit).length,
  };
}

// scopeHereCount 是作用域目录里有几个技能：库装的、和库里一样但不是 app 装的、不在技能库里的都算(Agent 在这里都用得上),
// 来自全局的不算。右栏这个作用域的数字和作用域页标题里的数字都是它。
export function scopeHereCount(s: Snapshot, key: ScopeKey): number {
  return hereIn(scopeListing(s, key, "collection", ""));
}

const hereIn = (l: ScopeListing) => l.hereCount + l.hereUnmanaged.length;

// ---------- 项目页 ----------

// ProjectAgent 是项目页的一节：项目里一个 Agent(共用一个目录的几个合成一处，scope 是代表的作用域)装在这里的
// 库里的技能与未纳管技能(搜索时只留匹配的);all 是不搜索时一共几个。
export interface ProjectAgent {
  scope: ScopeKey;
  agent: Agent;
  skills: Skill[];
  unmanaged: UnmanagedSkill[];
  all: number;
}

// projectAgentsIn 是项目里装有技能(库装的或未纳管的)的 Agent,按 Agent 列表顺序;共用一个目录的几个合成一处(代表的 Agent)。
// 来自全局的不算。
function projectAgentsIn(s: Snapshot, project: ProjectID): Agent[] {
  const inProject = (p: { scope: Scope }, a: Agent) => p.scope.agent === a.code && placeOf(p.scope) === project;
  const used = s.agents.filter((a) => s.skillScopes.some((p) => inProject(p, a)) || s.unmanaged.some((u) => inProject(u, a)));
  return mergeAgents(s, project, used).filter((a) => {
    const key = scopeKey(scopeOf(a.code, project));
    return placesIn(s, key).length > 0 || unmanagedIn(s, key).length > 0;
  });
}

// projectScopes 是项目里装了技能(库装的或未纳管的)的作用域，和项目页的各节一一对应(合成一处的是代表的作用域);
// 勾选后的操作栏「从这个项目卸载」就在这几处卸载。
export function projectScopes(s: Snapshot, project: ProjectID): Scope[] {
  return projectAgentsIn(s, project).map((a) => scopeOf(a.code, project));
}

// projectAgents 是项目页按 Agent 分的节：项目里装有技能的 Agent(见 projectAgentsIn)各一节;搜索时只留有结果的节。
export function projectAgents(s: Snapshot, project: ProjectID, search: string): ProjectAgent[] {
  const hit = (x: { name: string; description?: string }) => matches(search, x);
  const byName = (a: { name: string }, b: { name: string }) => collator.compare(a.name, b.name);
  return projectAgentsIn(s, project)
    .map((agent) => {
      const key = scopeKey(scopeOf(agent.code, project));
      const split = splitIdentical(unmanagedIn(s, key));
      const ids = new Set([...placesIn(s, key).map((p) => p.skill_id), ...split.identical.map((u) => u.skill_id!)]);
      const skills = sortSkills(s, s.skills.filter((x) => ids.has(x.id)));
      const unmanaged = split.unmanaged.sort(byName);
      return { scope: key, agent, skills: skills.filter(hit), unmanaged: unmanaged.filter(hit), all: skills.length + unmanaged.length };
    })
    .filter((x) => x.skills.length + x.unmanaged.length > 0);
}

// ---------- 每一页列什么、数到几 ----------

// PageListing 是当前页要列的东西与个数，列表、顶栏和搜索行都读这一份。total 是顶栏标题旁的个数，不随搜索变:
// 技能库页是库里的技能数，更新页是要处理的技能数，作用域页是目录里有几个技能(scopeHereCount,和摘要标题、右栏一致;
// global 是另外列出的来自全局的个数，不算在 total 里),
// 项目页是项目里装了几个技能(几个 Agent 都装了的只算一个;unmanaged 是其中不在技能库里的，同样不随搜索变);
// results 是搜索结果数：当前页里名字或描述匹配的行，一个技能在几个组里只算一次。
// 关系图页的 total 是集合或组里的技能数;它的搜索结果数要算上图里虚线圆的技能，由关系图自己算(lib/graph 的 searchHits),这里的 results 不用。
export type PageListing = { total: number; results: number } & (
  | { kind: "all"; sections: SkillSection[] }
  | { kind: "updates"; skills: Skill[] }
  | { kind: "scope"; scope: ScopeKey; listing: ScopeListing; global: number }
  | { kind: "project"; project: ProjectID; agents: ProjectAgent[]; unmanaged: number }
  | { kind: "graph"; set: GraphSet; members: SkillID[] }
);

// 项目里有几个技能：几个 Agent 都装了的技能只算一个;不是 app 装的各是各的文件夹。
const projectFolders = (list: ProjectAgent[]) => new Set(list.flatMap((a) => a.unmanaged.map((u) => u.folder))).size;
const projectCount = (list: ProjectAgent[]) => new Set(list.flatMap((a) => a.skills.map((x) => x.id))).size + projectFolders(list);

// projectSkillCount 是项目里有几个技能(不论是不是 app 装的),右栏这个项目的数字和项目页标题里的数字都是它。
export function projectSkillCount(s: Snapshot, project: ProjectID): number {
  return projectCount(projectAgents(s, project, ""));
}

export function pageListing(s: Snapshot, page: Page, arrange: Arrange, search: string): PageListing {
  if (page.kind === "graph") {
    const members = graphMembers(s, page.set) ?? [];
    return { kind: "graph", set: page.set, members, total: members.length, results: members.length };
  }
  if (page.kind === "updates") {
    const todo = sortSkills(s, todoSkills(s));
    const skills = todo.filter((x) => matches(search, x));
    return { kind: "updates", skills, total: todo.length, results: skills.length };
  }
  if (page.kind === "scope") {
    const all = scopeListing(s, page.scope, arrange, "");
    const listing = search.trim() ? scopeListing(s, page.scope, arrange, search) : all;
    const global = (l: ScopeListing) => l.global.length + l.globalUnmanaged.length;
    return {
      kind: "scope",
      scope: page.scope,
      listing,
      global: global(all),
      total: hereIn(all),
      results: hereIn(listing) + global(listing) + listing.notHereSkills.length,
    };
  }
  if (page.kind === "project") {
    const agents = projectAgents(s, page.project, search);
    const all = projectAgents(s, page.project, "");
    return { kind: "project", project: page.project, agents, unmanaged: projectFolders(all), total: projectCount(all), results: projectCount(agents) };
  }
  return {
    kind: "all",
    sections: arrangeSkills(s, arrange, search),
    total: s.skills.length,
    results: s.skills.filter((x) => matches(search, x)).length,
  };
}

// ---------- 勾选 ----------

// PickRow 是列表里能勾选的一行:key 是这一行的身份(所在的节 + 技能),同一个技能在几节里出现时各是一行。
export interface PickRow {
  key: string;
  id: SkillID;
}

// pickRange 是 rows 里从 from 那一行到 to 那一行(含两头，不分先后)的技能，不重复;两行有一行不在 rows 里时为空。
export function pickRange(rows: PickRow[], from: string, to: string): SkillID[] {
  const a = rows.findIndex((r) => r.key === from);
  const b = rows.findIndex((r) => r.key === to);
  if (a < 0 || b < 0) return [];
  return [...new Set(rows.slice(Math.min(a, b), Math.max(a, b) + 1).map((r) => r.id))];
}

// pageSkills 是这一页列出的库里的技能，勾选只留这些：项目页只列装在这个项目里的，其余页列出库里的全部。
export function pageSkills(s: Snapshot, page: Page): Set<SkillID> {
  if (page.kind === "graph") return new Set(graphMembers(s, page.set) ?? []);
  if (page.kind !== "project") return new Set(s.skills.map((x) => x.id));
  return new Set(projectAgents(s, page.project, "").flatMap((a) => a.skills.map((x) => x.id)));
}

// pageInstallTarget 是在这一页上"安装到…"默认选的地方与 Agent:作用域页是这个作用域，项目页是这个项目，其余不预选。
export function pageInstallTarget(page: Page): { place?: Place; agent?: AgentCode } {
  if (page.kind === "scope") return { place: placeOf(parseScope(page.scope)), agent: parseScope(page.scope).agent };
  if (page.kind === "project") return { place: page.project };
  return {};
}
