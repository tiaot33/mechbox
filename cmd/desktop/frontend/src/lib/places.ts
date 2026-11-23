// 界面上说的「一处」:同一层级(全局，或某个项目里)目录列表完全相同的几个 Agent 合成一处。
// 它们读的是同一组目录，装在其中一个，其余几个也算已装，卸载也一起卸载。界面上它们合成一处：右栏、作用域页、技能详情的安装表、
// "安装到…"和各处逐处列出的地方都只列一次，名字是这台机器上显示的那几个 Agent 的名字;点选、拖放、安装都落在其中第一个(代表)的作用域上。
// 目录列表不同、只共用某个目录的(如 Cursor 读 .agents/skills 和 .cursor/skills,Codex 只读 .agents/skills)各占一行，卸载前提示还被谁共用。
// 合成一处的几个 Agent 也可以各自装在列表里不同的目录(各自的安装记录都保留),那就是不同的文件夹：一个技能逐处列出时按实际的文件夹分开，
// 名字写读那个文件夹的那几个 Agent;作用域页、右栏的状态、重新安装与开关覆盖其中每一处(见 lib/installs 的 skillScopesIn)。
// 手里是一串安装(快照里的、技能库返回的结果、更新计划或历史里的)时，合并在这里做，调用处不用管:
// 逐处列出用 eachPlace,数几处用 placeCount(一个技能在一处算一处),写这几处的名字用 placeNames,
// 只问涉及几个地方、不分技能用 scopeCount。
import type { Agent, AgentCode, Scope, SkillScopeInspection } from "@bindings/internal/library";
import { messages } from "./i18n";
import type { Snapshot } from "./library";
import { homePath, nativePath } from "./paths";
import { parseScope, placeOf, projectName, scopeKey, scopeOf, type Place, type ScopeKey } from "./scope";

// AgentList 是合成一处要用的快照内容。skillScopes 用来按实际的文件夹分开一个技能的几处、看 Agent 自己有没有安装记录，没给时按合成后的一处算;
// info 用来把全局目录写成 ~/...。
export type AgentList = Pick<Snapshot, "agents" | "agentsShown"> & Partial<Pick<Snapshot, "skillScopes" | "info">>;

// ---------- 一处是谁、叫什么 ----------

// directoriesAt 是 Agent 在一个层级的技能目录列表。
export function directoriesAt(agent: Agent, place: Place): string[] {
  return (place ? agent.project_directories : agent.global_directories) ?? [];
}

// sameDirectories 是两个目录列表是否完全相同(顺序也相同)。
function sameDirectories(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((dir, i) => dir === b[i]);
}

// sharers 是同一层级和这个 Agent 目录列表完全相同的全部 Agent(含它自己),按 Agent 列表顺序;不和别的 Agent 合成时为空。
function sharers(s: AgentList, code: AgentCode, place: Place): Agent[] {
  const self = s.agents.find((a) => a.code === code);
  if (!self) return [];
  const all = s.agents.filter((a) => sameDirectories(directoriesAt(a, place), directoriesAt(self, place)));
  return all.length > 1 ? all : [];
}

// mergedAgents 是合成的一处里的全部 Agent(含这台机器上不显示的);不合成时只有它自己。
export function mergedAgents(s: AgentList, code: AgentCode, place: Place): AgentCode[] {
  const all = sharers(s, code, place);
  return all.length ? all.map((a) => a.code) : [code];
}

// namedAt 是合成的一处用来起名、画图标的 Agent:这台机器上显示的那几个;一个都不显示时取第一个。不合成时为空。
export function namedAt(s: AgentList, code: AgentCode, place: Place): Agent[] {
  const all = sharers(s, code, place);
  const shown = all.filter((a) => agentShown(s.agentsShown, a));
  return shown.length ? shown : all.slice(0, 1);
}

// leadAt 是代表合成的一处的 Agent;不合成时就是它自己。
function leadAt(s: AgentList, code: AgentCode, place: Place): AgentCode {
  return namedAt(s, code, place)[0]?.code ?? code;
}

// mergedScope 是作用域合成后落在哪个作用域上：合成一处的几个落在代表的 Agent 上，不合成的就是它自己。
export function mergedScope(s: AgentList, scope: Scope): Scope {
  return scopeOf(leadAt(s, scope.agent, placeOf(scope)), placeOf(scope));
}

// mergedScopeKey 是作用域合成后的身份：合成一处的几个作用域是同一个。
export function mergedScopeKey(s: AgentList, scope: Scope): ScopeKey {
  return scopeKey(mergedScope(s, scope));
}

// sameMergedScope 是两个作用域合成后是否是同一处。
export function sameMergedScope(s: AgentList, a: Scope, b: Scope): boolean {
  return mergedScopeKey(s, a) === mergedScopeKey(s, b);
}

// sameFolderAt 是两个 Agent 在某一层级是否合成一处(目录列表完全相同)。
export function sameFolderAt(s: AgentList, a: AgentCode, b: AgentCode, place: Place): boolean {
  return a === b || sharers(s, a, place).some((x) => x.code === b);
}

// scopeReaders 是读这个作用域里技能文件夹的 Agent:合成一处的几个(含这台机器上不显示的),不合成时是它自己。
export function scopeReaders(s: AgentList, scope: Scope): AgentCode[] {
  return mergedAgents(s, scope.agent, placeOf(scope));
}

// globalScopesSeenFrom 是项目作用域里的 Agent 也看得到的全局作用域(合成一处的几个 Agent 各自的全局);全局作用域为空。
export function globalScopesSeenFrom(s: AgentList, key: ScopeKey): ScopeKey[] {
  const sc = parseScope(key);
  if (!placeOf(sc)) return [];
  const named = namedAt(s, sc.agent, placeOf(sc));
  return (named.length ? named.map((a) => a.code) : [sc.agent]).map((code) => scopeKey(scopeOf(code, 0)));
}

// agentNameIn 是 Agent 在某个地方的名字：合成的一处是那几个 Agent 的名字。
export function agentNameIn(s: AgentList, code: AgentCode, place: Place): string {
  const named = namedAt(s, code, place);
  if (named.length) return messages().model.agentNames(named.map((a) => a.name));
  return s.agents.find((a) => a.code === code)?.name ?? code;
}

// scopeLabel 是一处的名字：「Agent · 全局」或「Agent · 项目名」,合成的一处写那几个 Agent 的名字。
export function scopeLabel(s: AgentList & Pick<Snapshot, "projects">, scope: Scope): string {
  return `${agentNameIn(s, scope.agent, placeOf(scope))} · ${where(s, scope)}`;
}

const where = (s: Pick<Snapshot, "projects">, scope: Scope) => (scope.project_id ? projectName(s, scope.project_id) : messages().model.global);

// mergedFolderNote 是合成的一处的悬停说明：读这组目录的全部 Agent(含这台机器上不显示的);不合成时为 undefined。
export function mergedFolderNote(s: AgentList, code: AgentCode, place: Place): string | undefined {
  const all = sharers(s, code, place);
  if (!all.length) return undefined;
  const dirs = directoriesAt(all[0], place).map((dir) => (place ? nativePath(dir) : homePath(s.info?.home_dir, dir)));
  return messages().model.mergedFolderNote(dirs, all.map((a) => a.name), !!place);
}

// sharedWithNote 是一处安装要另外说明的共用 Agent:共用这个文件夹、又不在这一处合成的名字里的(目录列表不同、只共用这个目录的)。
export function sharedWithNote(s: AgentList, p: Pick<SkillScopeInspection, "scope" | "shared_with">): AgentCode[] {
  const merged = mergedAgents(s, p.scope.agent, placeOf(p.scope));
  return (p.shared_with ?? []).filter((code) => !merged.includes(code));
}

// ---------- 几处 ----------

// Placed 是装在某个作用域的一条：快照里的安装、技能库返回的逐处结果、更新计划与历史里的地方都是这个样子;
// 只说地方、不分技能时(如"这几处链着它")没有 skill_id。folder 是这一处的文件夹，没给时按快照里这个技能在这个作用域的那一处找。
type Placed = { skill_id?: number; scope: Scope; folder?: string };

// folderOf 是一条安装的实际文件夹;只说地方的、快照里找不到的为空(按合成后的一处算)。
function folderOf(s: AgentList, p: Placed): string {
  if (!p.skill_id) return "";
  if (p.folder) return p.folder;
  const key = scopeKey(p.scope);
  return s.skillScopes?.find((x) => x.skill_id === p.skill_id && scopeKey(x.scope) === key)?.folder ?? "";
}

// mergedPlaceKey 是一条的身份：哪个技能(只说地方时没有)装在合成后的哪一处的哪个文件夹。两条的键相同就是界面上的同一处。
export function mergedPlaceKey(s: AgentList, p: Placed): string {
  return `${p.skill_id ?? ""}|${mergedScopeKey(s, p.scope)}|${folderOf(s, p)}`;
}

// placeAgents 是一条安装在合成的一处里实际读它那个文件夹的 Agent(起名用):合成的几个都读同一个文件夹时同 namedAt;
// 各自装在不同目录时只是读这个文件夹的那几个(显示的优先)。不合成时为空。
function placeAgents(s: AgentList, p: Placed): Agent[] {
  const place = placeOf(p.scope);
  const named = namedAt(s, p.scope.agent, place);
  const folder = folderOf(s, p);
  if (!named.length || !folder || !s.skillScopes) return named;
  const all = sharers(s, p.scope.agent, place);
  const readers = all.filter((a) =>
    s.skillScopes!.some((x) => x.skill_id === p.skill_id && x.scope.agent === a.code && placeOf(x.scope) === place && x.folder === folder),
  );
  if (!readers.length || readers.length === all.length) return named;
  const shown = readers.filter((a) => agentShown(s.agentsShown, a));
  return shown.length ? shown : readers.slice(0, 1);
}

// placeLabel 是一条安装所在那一处的名字(见 scopeLabel):合成的几个 Agent 各自装在不同目录时，写读这个文件夹的那几个。
export function placeLabel(s: AgentList & Pick<Snapshot, "projects">, p: Placed): string {
  return `${placeAgentNames(s, p)} · ${where(s, p.scope)}`;
}

// placeAgentNames 是一条安装所在那一处的 Agent 名字(placeLabel 去掉「· 全局」「· 项目名」的部分)。
export function placeAgentNames(s: AgentList, p: Placed): string {
  const named = placeAgents(s, p);
  if (!named.length) return agentNameIn(s, p.scope.agent, placeOf(p.scope));
  return messages().model.agentNames(named.map((a) => a.name));
}

// eachPlace 是这几条逐处列出时的每一处：一个技能在合成的一处里、同一个文件夹只算一处(留第一条),保持原来的先后。
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
export function placeCount(s: AgentList, items: readonly { skill_id: number; scope: Scope; folder?: string }[]): number {
  return eachPlace(s, items).length;
}

// 只问地方、不分技能时，一条安装和一个作用域是一回事。
const scopesOf = (s: AgentList, items: readonly ({ scope: Scope } | Scope)[]): Scope[] =>
  eachPlace(s, items.map((x) => ({ scope: "scope" in x ? x.scope : x }))).map((p) => p.scope);

// scopeCount 是这几条涉及几个地方：不分技能，几个技能装在同一处只算一个。
export function scopeCount(s: AgentList, items: readonly ({ scope: Scope } | Scope)[]): number {
  return scopesOf(s, items).length;
}

// placeNames 是这几条所在各处的名字(见 placeLabel):不分技能，一处只写一次，保持第一次出现的先后;也可以直接给一串作用域。
export function placeNames(s: AgentList & Pick<Snapshot, "projects">, items: readonly (Placed | Scope)[]): string[] {
  const labels = items.map((x) => ("scope" in x ? placeLabel(s, x) : scopeLabel(s, x)));
  return [...new Set(labels)];
}

// ---------- 一个地方列哪些 Agent ----------

// agentShown 是 Agent 的显示勾选：用户勾选过的按勾选，没勾选过的都显示(已添加就是用户选的)。
// 显示只影响选择安装位置的地方(右栏的"其他 Agent"、"安装到…");自己装了技能的 Agent 照常列出。
export function agentShown(shown: Partial<Record<string, boolean>> | null | undefined, agent: Agent): boolean {
  return shown?.[agent.code] ?? true;
}

// mergeAgents 把某个地方的一串 Agent 合成一处一个(换成代表的 Agent),保持第一次出现的顺序。
export function mergeAgents(s: AgentList, place: Place, agents: Agent[]): Agent[] {
  const out: Agent[] = [];
  for (const a of agents) {
    const lead = s.agents.find((x) => x.code === leadAt(s, a.code, place)) ?? a;
    if (!out.includes(lead)) out.push(lead);
  }
  return out;
}

// mergeAgentsExcept 是某个地方还没列出的 Agent:合成一处一个，和 listed 里某一处合成一处的去掉。
export function mergeAgentsExcept(s: AgentList, place: Place, agents: Agent[], listed: Agent[]): Agent[] {
  return mergeAgents(s, place, agents).filter((a) => !listed.some((o) => sameFolderAt(s, o.code, a.code, place)));
}

// agentsUsedIn 是某个地方实际装有技能的 Agent,按 Agent 列表顺序，合成一处的几个合成一处。
// 只因共用目录被动算出已装(自己没有直接安装和组安装)、又被关掉显示的 Agent 不算：用户不想看到它。
export function agentsUsedIn(s: Snapshot, place: Place) {
  const used = s.agents.filter((a) =>
    s.skillScopes.some((p) => p.scope.agent === a.code && placeOf(p.scope) === place && (!p.shared_only || agentShown(s.agentsShown, a))),
  );
  return mergeAgents(s, place, used);
}

// agentsIn 把某个地方的 Agent 分成直接列出的和折进"其他 N 个 Agent"的(右栏):
// 在这里装有技能的(见 agentsUsedIn)、以及检测到且勾选显示的直接列出;勾选了显示但没检测到的折起来;没勾选显示、也没装技能的不列出。
// 合成一处的几个 Agent 合成一行(见 mergeAgents),已经列出的那一处不再折进"其他"。
export function agentsIn(s: Snapshot, place: Place): { open: Agent[]; folded: Agent[] } {
  const used = agentsUsedIn(s, place);
  const open = mergeAgents(s, place, s.agents.filter((a) => used.includes(a) || (a.detected && agentShown(s.agentsShown, a))));
  const folded = s.agents.filter((a) => !open.includes(a) && agentShown(s.agentsShown, a));
  return { open, folded: mergeAgentsExcept(s, place, folded, open) };
}
