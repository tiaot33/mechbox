// Agent(agents.tsx、agent-settings.tsx)里的判断：改目录时哪些已装的技能、组安装受影响要先问，移除 Agent 时哪些文件夹归它、
// 哪些还有别的 Agent 在读，候选池怎么排、哪几个要标「已有 Agent 读这个目录」。
// 不碰界面、不调技能库，单元测试见 agents-rules.test.ts。
import { MigrationOutcome, type Agent, type AgentCode, type AgentInput, type AgentMigration, type Scope, type SkillScopeInspection } from "@bindings/internal/library";
import { detachedBy } from "@/lib/consent";
import { messages } from "@/lib/i18n";
import type { Snapshot } from "@/lib/library";
import { folderPlaces } from "@/lib/installs";
import { directoriesAt } from "@/lib/places";
import { shortPath } from "@/lib/paths";
import { placeOf, projectName, skillName, type Place } from "@/lib/scope";
import type { ResultRow } from "@/components/app/result-rows";

// cleanPath 是比较目录时去掉开头的 ./ 和末尾的 /:写法不同的同一个目录不算改了。
const cleanPath = (path: string) => path.replace(/^\.\//, "").replace(/\/+$/, "");

const inputDirectories = (input: AgentInput, place: Place) => ((place ? input.project_directories : input.global_directories) ?? []).map(cleanPath);

// agentEdit 是编辑 Agent 时受影响的:affected 是这个 Agent 所在目录被去掉的那几处安装(含因共用目录而算已装的;
// 所在目录还在新列表里的磁盘不动，不算);groupInstalls 是原来的第 1 个目录被去掉的层级里它的组安装数。
// asks 是要不要先问是否迁移;shared 是其中还有别的 Agent 在读的(那份文件夹留在旧目录);detached 是迁移时别处的符号链接
// 指着要搬走的文件夹、库先给那几处换上独立的一份的。
export function agentEdit(s: Snapshot, agent: Agent, input: AgentInput) {
  const removed = (dir: string, place: Place) => !inputDirectories(input, place).includes(cleanPath(dir));
  // 这一处所在的目录：技能库总会给出;没有时按原来的第 1 个目录算。
  const dirOf = (p: SkillScopeInspection) => p.directory || (directoriesAt(agent, placeOf(p.scope))[0] ?? "");
  const firstRemoved = (scope: Scope) => removed(directoriesAt(agent, placeOf(scope))[0] ?? "", placeOf(scope));
  const affected = s.skillScopes.filter((p) => p.scope.agent === agent.code && removed(dirOf(p), placeOf(p.scope)));
  const groupInstalls = s.groupInstalls.filter((g) => g.scope.agent === agent.code && firstRemoved(g.scope)).length;
  return {
    affected,
    groupInstalls,
    asks: affected.length > 0 || groupInstalls > 0,
    shared: affected.filter((p) => p.shared_with?.length),
    detached: detachedBy(folderPlaces(s, affected.filter((p) => !p.shared_with?.length))),
  };
}

// agentDelete 是移除 Agent 时它装着的各处:own 是只有它在读的(选删掉时删),kept 是还有别的 Agent 在读的(含只因共用目录才算已装的),
// 不管怎么选都保留。
export function agentDelete(s: Snapshot, code: AgentCode) {
  const places = s.skillScopes.filter((p) => p.scope.agent === code);
  return { kept: places.filter((p) => p.shared_with?.length), own: places.filter((p) => !p.shared_with?.length) };
}

// migrationReport 是改目录并迁移之后要说的：rows 逐个列出没有原样搬过去的(新目录里已有别的同名文件夹而没迁移的，
// 新目录里已经装着同一个技能、改认那一份的);listed 是要不要弹出结果列表：有没迁移的才弹，否则只提示迁移了几处;
// migrated 是这个 Agent 在新目录照常算已装的处数(搬过去的、复制过去的、改认那一份的)。
export function migrationReport(s: Snapshot, migrations: AgentMigration[]) {
  const t = messages().actions;
  const label = (m: AgentMigration) => `${skillName(s, m.skill_id)} · ${m.scope.project_id ? projectName(s, m.scope.project_id) : messages().model.global}`;
  const skipped = migrations.filter((m) => m.outcome === MigrationOutcome.MigrationSkipped);
  const already = migrations.filter((m) => m.outcome === MigrationOutcome.MigrationAlreadyInstalled);
  const rows: ResultRow[] = [
    ...skipped.map((m) => ({ label: label(m), outcome: t.notMigrated, tone: "warn" as const, reason: t.notMigratedReason(shortPath(s, m.to), shortPath(s, m.from)) })),
    ...already.map((m) => ({ label: label(m), outcome: t.alreadyMigrated, tone: "plain" as const, reason: t.alreadyMigratedReason(shortPath(s, m.to), shortPath(s, m.from)) })),
  ];
  return { rows, listed: skipped.length > 0, migrated: migrations.length - skipped.length, skipped: skipped.length };
}

// ---------- 恢复默认 ----------

// AgentChange 是恢复默认时一个收录的 Agent 要换回的内置值:fields 是和内置值不一样的几项。
export type AgentChange = { agent: Agent; defaults: Agent; fields: ("name" | DirectoryKind)[] };

// handAdded 是用户手填添加的 Agent(代码以 custom- 开头),恢复默认时一律移除。
export const handAdded = (agent: Agent) => agent.code.startsWith("custom-");

const sameDirectories = (a: string[] | null = [], b: string[] | null = []) =>
  (a ?? []).length === (b ?? []).length && (a ?? []).every((d, i) => cleanPath(d) === cleanPath((b ?? [])[i]));
const sameSet = (a: string[] | null = [], b: string[] | null = []) => sameDirectories((a ?? []).map(cleanPath).sort(), (b ?? []).map(cleanPath).sort());

function agentChange(agent: Agent, defaults: Agent | undefined): AgentChange | undefined {
  if (!defaults) return undefined;
  const fields: AgentChange["fields"] = [];
  if (agent.name !== defaults.name) fields.push("name");
  for (const kind of ["global_directories", "project_directories"] as const) if (!sameDirectories(agent[kind], defaults[kind])) fields.push(kind);
  if (!sameSet(agent.detect_directories, defaults.detect_directories)) fields.push("detect_directories");
  return fields.length ? { agent, defaults, fields } : undefined;
}

const inputOf = (a: Agent): AgentInput => ({
  name: a.name,
  global_directories: a.global_directories ?? [],
  project_directories: a.project_directories ?? [],
  detect_directories: a.detect_directories ?? [],
});

// agentReset 是「恢复默认」会做的，和技能库的 ResetAgents 一致:先移除手填添加的 Agent(removed 是已添加的，removedPooled 是候选池里的，
// 都不回候选池;它们的文件夹按移除 Agent 的规则:kept 还有别的 Agent 在读、总是保留,own 只有它读、按用户选的删除或保留),
// 再把收录的 Agent 换回内置值(changed 是已添加的,changedPooled 是候选池里的);migrations 是已添加的收录 Agent 去掉的目录里
// 那几处安装，迁移到内置的第 1 个目录(to 是那个目录);detached 是别处的符号链接指着要搬走的文件夹的。nothing 是已经和内置的一样。
export function agentReset(s: Snapshot, defaults: Agent[], pool: Agent[]) {
  const byCode = new Map(defaults.map((a) => [a.code, a]));
  const removed = s.agents.filter(handAdded);
  const removedPooled = pool.filter(handAdded);
  const changed = s.agents.flatMap((a) => agentChange(a, byCode.get(a.code)) ?? []);
  const changedPooled = pool.flatMap((a) => agentChange(a, byCode.get(a.code)) ?? []);
  const deleted = removed.map((a) => agentDelete(s, a.code));
  const edits = changed.map((c) => ({ change: c, edit: agentEdit(s, c.agent, inputOf(c.defaults)) }));
  const migrations = edits.flatMap(({ change, edit }) =>
    edit.affected.map((place) => ({ place, to: directoriesAt(change.defaults, placeOf(place.scope))[0] ?? "" })),
  );
  return {
    removed,
    removedPooled,
    changed,
    changedPooled,
    kept: deleted.flatMap((d) => d.kept),
    own: deleted.flatMap((d) => d.own),
    migrations,
    detached: edits.flatMap(({ edit }) => edit.detached),
    nothing: !removed.length && !removedPooled.length && !changed.length && !changedPooled.length,
  };
}

// ---------- 候选池 ----------

// poolMatches 是候选池里按名称、目录搜索:不分大小写，名称或任一目录包含搜索的文字。
export function poolMatches(agent: Agent, search: string): boolean {
  const q = search.trim().toLowerCase();
  if (!q) return true;
  return [agent.name, ...directoriesAt(agent, 0), ...directoriesAt(agent, 1)].some((x) => x.toLowerCase().includes(q));
}

// sortPool 是候选池的顺序：检测到的排在前面，其余按名称。
export function sortPool(pool: Agent[]): Agent[] {
  const collator = new Intl.Collator(undefined, { sensitivity: "base", numeric: true });
  return [...pool].sort((a, b) => Number(b.detected) - Number(a.detected) || collator.compare(a.name, b.name));
}

// AlreadyRead 是候选池里一个 Agent 独有的目录已经有已添加的 Agent 在读:dir 是那个目录(全局为绝对路径，项目为相对路径),by 是在读的那几个。
export type AlreadyRead = { dir: string; project: boolean; by: Agent[] };

// alreadyRead 是候选池里这个 Agent 独有的目录(候选池里只有它读的，如 Cursor 的 .cursor/skills;不比 .agents/skills 这种许多 Agent 都读的)
// 已经有已添加的 Agent 在读的那几个:多半是以前手填添加的同一个 Agent,再添加就有两个 Agent 读同一个目录。只标出，不阻止添加。
export function alreadyRead(pool: Agent[], added: Agent[], agent: Agent): AlreadyRead[] {
  return [0, 1].flatMap((place) =>
    directoriesAt(agent, place).flatMap((dir): AlreadyRead[] => {
      const same = (other: Agent) => directoriesAt(other, place).some((d) => cleanPath(d) === cleanPath(dir));
      if (pool.some((other) => other.code !== agent.code && same(other))) return [];
      const by = added.filter(same);
      return by.length ? [{ dir, project: !!place, by }] : [];
    }),
  );
}

// detectedPicks 是升级上来以后提示添加 Agent 时默认勾上的：候选池里在这台机器上检测到的;独有的目录已经有已添加的 Agent 在读的
// (多半是以前手填添加的同一个 Agent,见 alreadyRead)不勾，免得两个 Agent 读同一个目录。
export function detectedPicks(pool: Agent[], added: Agent[]): string[] {
  return pool.filter((a) => a.detected && !alreadyRead(pool, added, a).length).map((a) => a.code);
}

// undetected 是设置页一行要不要标「未检测到」:有检测目录、又都不存在时才标;检测目录为空的不检测，不标。
export function undetected(agent: Agent): boolean {
  return !agent.detected && (agent.detect_directories ?? []).length > 0;
}

// ---------- 目录候选 ----------

// DirectoryKind 是 Agent 表单里的三组目录。
export type DirectoryKind = "global_directories" | "project_directories" | "detect_directories";

// DirectoryOptions 是一组目录输入框下拉里的候选:defaults 是这个 Agent 的默认目录(应用内置的值，不是数据库里那一行;
// 手填添加的 Agent 没有),others 是内置收录的其他 Agent 用到的目录。都按 show 写成界面上的样子，去掉重复，others 按字母排。
export type DirectoryOptions = { defaults: string[]; others: string[] };

export function directoryOptions(catalog: Agent[], code: string | undefined, kind: DirectoryKind, show: (dir: string) => string): DirectoryOptions {
  const dirs = (a: Agent) => (a[kind] ?? []).map(show);
  const own = catalog.find((a) => a.code === code);
  const defaults = [...new Set(own ? dirs(own) : [])];
  const others = [...new Set(catalog.filter((a) => a !== own).flatMap(dirs))].filter((d) => !defaults.includes(d)).sort();
  return { defaults, others };
}

// filterDirectoryOptions 是下拉里显示的候选：去掉这组里别的行已经填了的;typed 不为空时按文字过滤(不分大小写，包含即算)。
// opensWhileTyping 是下拉收着时，输入一下要不要自己展开：有和输入的不一样的候选才展开;没有匹配的、或输入的就是唯一的候选时不展开，
// 免得填完自己的路径后下拉挡住下面的按钮。
export function opensWhileTyping(options: DirectoryOptions, typed: string, taken: string[]): boolean {
  const shown = filterDirectoryOptions(options, typed, taken);
  return [...shown.defaults, ...shown.others].some((d) => cleanPath(d) !== cleanPath(typed.trim()));
}

export function filterDirectoryOptions(options: DirectoryOptions, typed: string, taken: string[]): DirectoryOptions {
  const q = cleanPath(typed.trim()).toLowerCase();
  const keep = (d: string) => !taken.some((x) => cleanPath(x.trim()) === cleanPath(d)) && (!q || d.toLowerCase().includes(q));
  return { defaults: options.defaults.filter(keep), others: options.others.filter(keep) };
}
