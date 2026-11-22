// 界面上说的「一处」:一个 Agent 的全局，或项目里读同一个目录的那几个 Agent。
// 项目里几个 Agent 的技能目录相同(如都读 .agents/skills)时，装在其中一个，其余几个也算已装，卸载也一起卸载。
// 界面上它们合成一处：右栏、作用域页、技能详情的安装表、"安装到…"和各处逐处列出的地方都只列一次，
// 名字是这台机器上显示的那几个 Agent 的名字;点选、拖放、安装都落在其中第一个(代表)的作用域上。
// 全局不合：全局目录大多各是各的，少数共用的照旧一个 Agent 一行，卸载前提示还被谁共用。
// 手里是一串安装(快照里的、技能库返回的结果、更新计划或历史里的)时，合并在这里做，调用处不用管:
// 逐处列出用 eachPlace,数几处用 placeCount(一个技能在一处算一处),写这几处的名字用 placeNames,
// 只问涉及几个地方、不分技能用 scopeCount。
import type { Agent, AgentCode, Scope, SkillScopeInspection } from "@bindings/internal/library";
import { messages } from "./i18n";
import type { Snapshot } from "./library";
import { nativePath } from "./paths";
import { parseScope, placeOf, projectName, scopeKey, scopeOf, type Place, type ScopeKey } from "./scope";

export type AgentList = Pick<Snapshot, "agents" | "agentsShown">;

// ---------- 一处是谁、叫什么 ----------

// sameDirectories 是两个 Agent 在一个层级的目录列表是否完全相同。
function sameDirectories(a: readonly string[] | null, b: readonly string[] | null): boolean {
  return (a ?? []).length === (b ?? []).length && (a ?? []).every((dir, i) => dir === b?.[i]);
}

// projectSharers 是项目里和这个 Agent 读同一个目录的全部 Agent(含它自己),按 Agent 列表顺序;不和别的 Agent 共用时为空。
function projectSharers(s: AgentList, code: AgentCode): Agent[] {
  const self = s.agents.find((a) => a.code === code);
  if (!self) return [];
  const all = s.agents.filter((a) => sameDirectories(a.project_directories, self.project_directories));
  return all.length > 1 ? all : [];
}

// projectNamed 是合成的一处用来起名、画图标的 Agent:共用的几个里这台机器上显示的;一个都不显示时取第一个。
export function projectNamed(s: AgentList, code: AgentCode): Agent[] {
  const all = projectSharers(s, code);
  const shown = all.filter((a) => agentShown(s.agentsShown, a));
  return shown.length ? shown : all.slice(0, 1);
}

// projectLead 是代表合成的一处的 Agent;不共用时就是它自己。
function projectLead(s: AgentList, code: AgentCode): AgentCode {
  return projectNamed(s, code)[0]?.code ?? code;
}

// mergedScope 是作用域合成后落在哪个作用域上：项目里共用一个目录的几个落在代表的 Agent 上，全局和不共用的就是它自己。
export function mergedScope(s: AgentList, scope: Scope): Scope {
  return scope.project_id ? scopeOf(projectLead(s, scope.agent), scope.project_id) : scope;
}

// mergedScopeKey 是作用域合成后的身份：项目里共用一个目录的几个作用域是同一个。
export function mergedScopeKey(s: AgentList, scope: Scope): ScopeKey {
  return scopeKey(mergedScope(s, scope));
}

// sameMergedScope 是两个作用域合成后是否是同一处。
export function sameMergedScope(s: AgentList, a: Scope, b: Scope): boolean {
  return mergedScopeKey(s, a) === mergedScopeKey(s, b);
}

function sameProjectFolder(s: AgentList, a: AgentCode, b: AgentCode): boolean {
  return a === b || projectSharers(s, a).some((x) => x.code === b);
}

// sameFolderAt 是两个 Agent 在某一处是否读同一个目录。
export function sameFolderAt(s: AgentList, a: AgentCode, b: AgentCode, place: Place): boolean {
  if (place) return sameProjectFolder(s, a, b);
  const dirs = (code: AgentCode) => s.agents.find((x) => x.code === code)?.global_directories;
  const first = dirs(a), second = dirs(b);
  return a === b || (!!first && !!second && sameDirectories(first, second));
}

// scopeReaders 是读这个作用域里技能文件夹的 Agent:全局是它自己，项目里是共用这个目录的几个(含这台机器上不显示的)。
export function scopeReaders(s: AgentList, scope: Scope): AgentCode[] {
  const shared = scope.project_id ? projectSharers(s, scope.agent) : [];
  return shared.length ? shared.map((a) => a.code) : [scope.agent];
}

// globalScopesSeenFrom 是项目作用域里的 Agent 也看得到的全局作用域(合成一处的几个 Agent 各自的全局);全局作用域为空。
export function globalScopesSeenFrom(s: AgentList, key: ScopeKey): ScopeKey[] {
  const sc = parseScope(key);
  if (!placeOf(sc)) return [];
  const named = projectNamed(s, sc.agent);
  return (named.length ? named.map((a) => a.code) : [sc.agent]).map((code) => scopeKey(scopeOf(code, 0)));
}

// agentNameIn 是 Agent 在某个地方的名字：项目里合成的一处是那几个 Agent 的名字。
export function agentNameIn(s: AgentList, code: AgentCode, place: Place): string {
  const named = place ? projectNamed(s, code) : [];
  if (named.length) return messages().model.agentNames(named.map((a) => a.name));
  return s.agents.find((a) => a.code === code)?.name ?? code;
}

// scopeLabel 是一处的名字：「Agent · 全局」或「Agent · 项目名」,项目里合成的一处写那几个 Agent 的名字。
export function scopeLabel(s: AgentList & Pick<Snapshot, "projects">, scope: Scope): string {
  return `${agentNameIn(s, scope.agent, placeOf(scope))} · ${scope.project_id ? projectName(s, scope.project_id) : messages().model.global}`;
}

// projectFolderNote 是合成的一处的悬停说明：读项目里这个目录的全部 Agent(含这台机器上不显示的);不合成时为 undefined。
export function projectFolderNote(s: AgentList, code: AgentCode, place: Place): string | undefined {
  const all = place ? projectSharers(s, code) : [];
  if (!all.length) return undefined;
  return messages().model.projectFolderNote(nativePath(all[0].project_directories?.[0] ?? ""), all.map((a) => a.name));
}

// sharedWithNote 是一处安装要另外说明的共用 Agent:全局照旧是共用这个文件夹的其他 Agent;
// 项目里共用一个目录的几个 Agent 已合成一处、名字里写着它们，不再另说。
export function sharedWithNote(p: Pick<SkillScopeInspection, "scope" | "shared_with">): AgentCode[] {
  return p.scope.project_id ? [] : (p.shared_with ?? []);
}

// ---------- 几处 ----------

// Placed 是装在某个作用域的一条：快照里的安装、技能库返回的逐处结果、更新计划与历史里的地方都是这个样子;
// 只说地方、不分技能时(如"这几处链着它")没有 skill_id。
type Placed = { skill_id?: number; scope: Scope };

// mergedPlaceKey 是一条的身份：哪个技能(只说地方时没有)装在合成后的哪一处。两条的键相同就是界面上的同一处。
export function mergedPlaceKey(s: AgentList, p: Placed): string {
  return `${p.skill_id ?? ""}|${mergedScopeKey(s, p.scope)}`;
}

// eachPlace 是这几条逐处列出时的每一处：一个技能在项目里共用一个文件夹的几个 Agent 那里只算一处(留第一条),保持原来的先后。
export function eachPlace<T extends Placed>(s: AgentList, items: readonly T[]): T[] {
  const seen = new Set<string>();
  return items.filter((p) => {
    const key = mergedPlaceKey(s, p);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

// placeCount 是这几条安装一共几处：一个技能在一处算一处，两个技能装在同一处是两处。
export function placeCount(s: AgentList, items: readonly { skill_id: number; scope: Scope }[]): number {
  return eachPlace(s, items).length;
}

// 只问地方、不分技能时，一条安装和一个作用域是一回事。
const scopesOf = (s: AgentList, items: readonly ({ scope: Scope } | Scope)[]): Scope[] =>
  eachPlace(s, items.map((x) => ({ scope: "scope" in x ? x.scope : x }))).map((p) => p.scope);

// scopeCount 是这几条涉及几个地方：不分技能，几个技能装在同一处只算一个。
export function scopeCount(s: AgentList, items: readonly ({ scope: Scope } | Scope)[]): number {
  return scopesOf(s, items).length;
}

// placeNames 是这几条所在各处的名字(见 scopeLabel):不分技能，一处只写一次，保持第一次出现的先后;也可以直接给一串作用域。
export function placeNames(s: AgentList & Pick<Snapshot, "projects">, items: readonly ({ scope: Scope } | Scope)[]): string[] {
  return scopesOf(s, items).map((scope) => scopeLabel(s, scope));
}

// ---------- 一个地方列哪些 Agent ----------

// agentShown 是 Agent 的显示勾选：用户勾选过的按勾选，没勾选过的都显示(已添加就是用户选的)。
// 显示只影响选择安装位置的地方(右栏的"其他 Agent"、"安装到…");已经装了技能的 Agent 照常列出。
export function agentShown(shown: Partial<Record<string, boolean>> | null | undefined, agent: Agent): boolean {
  return shown?.[agent.code] ?? true;
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

// agentsUsedIn 是某个地方实际装有技能的 Agent,按 Agent 列表顺序;项目里共用一个目录的几个合成一处。
export function agentsUsedIn(s: Snapshot, place: Place) {
  const used = s.agents.filter((a) => s.skillScopes.some((p) => p.scope.agent === a.code && placeOf(p.scope) === place));
  return place ? mergeProjectAgents(s, used) : used;
}

// agentsIn 把某个地方的 Agent 分成直接列出的和折进"其他 N 个 Agent"的(右栏):
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
