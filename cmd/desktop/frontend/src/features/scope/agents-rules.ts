// Agent(agents.tsx、agent-settings.tsx)里的判断：改目录时哪些已装的技能、组安装受影响要先问，移除 Agent 时哪些文件夹归它、
// 哪些还有别的 Agent 在读，候选池怎么排、哪几个要标「已有 Agent 读这个目录」。
// 不碰界面、不调技能库，单元测试见 agents-rules.test.ts。
import {
  MigrationOutcome,
  type Agent,
  type AgentCode,
  type AgentInput,
  type AgentMigration,
  type ResetAgentsPlan,
  type Scope,
  type SkillScope,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import { detachedBy } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { messages } from "@/lib/i18n";
import type { LibraryError, Snapshot } from "@/lib/library";
import { folderPlaces } from "@/lib/installs";
import { directoriesAt } from "@/lib/places";
import { shortPath, windowsHome } from "@/lib/paths";
import { agentName, placeOf, projectName, scopeKey, skillName, type Place } from "@/lib/scope";
import type { ResultRow } from "@/components/app/result-rows";

// cleanPath 是比较目录时去掉开头的 ./ 和末尾的 /(Windows 上也去掉 .\ 和末尾的 \):写法不同的同一个目录不算改了。
const cleanPath = (path: string) => path.replace(/^\.[\\/]/, "").replace(/[\\/]+$/, "");

// DirectoryRules 是比较两个目录写法时的规则:foldsCase 是不论大小写(主目录所在的卷不区分大小写，见 AppInfo.home_folds_case),
// backslashes 是反斜杠也是分隔符(主目录是 Windows 上的路径时;macOS、Linux 上反斜杠是文件名的一部分)。
export type DirectoryRules = { foldsCase: boolean; backslashes: boolean };

// directoryRules 是按主目录定的规则：技能库校验一个 Agent 的目录列表时也按主目录所在的卷判断大小写。
export function directoryRules(s: Snapshot): DirectoryRules {
  return { foldsCase: !!s.info?.home_folds_case, backslashes: windowsHome(s.info?.home_dir ?? "") };
}

// comparable 是按 rules 比较两个目录用的写法：去掉开头的 ./ 和末尾的分隔符，反斜杠算分隔符时换成 /;不论大小写时只把 A–Z 转小写，
// 其他字母的大小写规则 JS 与技能库(Go)不完全一样，宁可当作两个目录多问一次。
const comparable = (path: string, rules: DirectoryRules) => {
  const trimmed = rules.backslashes ? path.replaceAll("\\", "/") : path;
  const key = trimmed.replace(/^\.\//, "").replace(/\/+$/, "");
  return rules.foldsCase ? key.replace(/[A-Z]/g, (c) => c.toLowerCase()) : key;
};

// overlappingDirectories 是一组目录里有没有重复的，或一个在另一个里面：技能库不接受这样的目录列表。
export function overlappingDirectories(paths: string[], rules: DirectoryRules): boolean {
  const keys = paths.map((p) => comparable(p, rules));
  return keys.some((a, i) => keys.some((b, j) => i !== j && (a === b || b.startsWith(a.endsWith("/") ? a : `${a}/`))));
}

// agentFieldError 是技能库按字段报的不合法在那组目录下面怎么写：目录重复或一个在另一个里面(技能库在 Details.Paths 给出那两个,
// 包括经符号链接才算同一个的，界面自己查不出)写成和界面检查一样的话，其余照 describeError。
export function agentFieldError(err: LibraryError): string {
  return err.details?.paths?.length ? messages().agents.directoriesOverlap : describeError(err);
}

// AgentFieldError 是表单里一项下面的报错:input 是报错里写出的那一行(去掉首尾空白),整项的报错(没填、重复或一个在另一个里面、技能库报的)没有。
export type AgentFieldError = { text: string; input?: string };

// keepsFieldError 是一项改了之后它的报错还要不要显示:before、after 是这一项改前改后的各行(名称是一行)。
// 去掉空白与空行后没变(添加或去掉空行、重新选回原来的目录、只敲空格)时保留;报错写出了某一行的，只有那一行被改掉或去掉才不再显示;
// 其余的改了就不再显示，改得对不对，再点保存时重新检查。
export function keepsFieldError(error: AgentFieldError, before: string[], after: string[]): boolean {
  const filled = (rows: string[]) => rows.map((r) => r.trim()).filter(Boolean);
  const now = filled(after);
  const was = filled(before);
  if (now.length === was.length && now.every((r, i) => r === was[i])) return true;
  return error.input !== undefined && now.includes(error.input);
}

// agentEdit 是编辑 Agent 时受影响的:affected 是这个 Agent 所在目录被去掉的那几处安装(含因共用目录而算已装的;
// 所在目录还在新列表里的磁盘不动，不算);groupInstalls 是原来的第 1 个目录被去掉的层级里它的组安装数。
// asks 是要不要先问是否迁移;shared 是其中还有别的 Agent 在读的(那份文件夹留在旧目录);detached 是迁移时别处的符号链接
// 指着要搬走的文件夹、库先给那几处换上独立的一份的。
// 只改大小写的全局目录在不区分大小写的卷上不算去掉;项目目录照常算，因为项目可以在区分大小写的卷上(例如 WSL 发行版里),由技能库逐个项目判断。
export function agentEdit(s: Snapshot, agent: Agent, input: AgentInput) {
  const home = directoryRules(s);
  const removed = (dir: string, place: Place) => {
    const rules = place ? { ...home, foldsCase: false } : home;
    return !((place ? input.project_directories : input.global_directories) ?? []).some((d) => comparable(d, rules) === comparable(dir, rules));
  };
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
// 新目录里已经装着同一个技能、改认那一份而旧文件夹留在原处的：那一份是库为别的 Agent 装的写出是谁，没有安装记录的只说和库里某一版一致);listed 是要不要弹出结果列表：有这两种之一就弹，否则只提示迁移了几处;
// migrated 是这个 Agent 在新目录照常算已装的处数(搬过去的、复制过去的、改认那一份的)。
export function migrationReport(s: Snapshot, migrations: AgentMigration[]) {
  const t = messages().actions;
  const label = (m: AgentMigration) => `${skillName(s, m.skill_id)} · ${m.scope.project_id ? projectName(s, m.scope.project_id) : messages().model.global}`;
  const skipped = migrations.filter((m) => m.outcome === MigrationOutcome.MigrationSkipped);
  const already = migrations.filter((m) => m.outcome === MigrationOutcome.MigrationAlreadyInstalled);
  const rows: ResultRow[] = [
    ...skipped.map((m) => ({ label: label(m), outcome: t.notMigrated, tone: "warn" as const, reason: t.notMigratedReason(shortPath(s, m.to), shortPath(s, m.from)) })),
    ...already.map((m) => ({
      label: label(m),
      outcome: t.alreadyMigrated,
      tone: "plain" as const,
      reason: t.alreadyMigratedReason(shortPath(s, m.to), shortPath(s, m.from), (m.installed_for ?? []).map((code) => agentName(s, code))),
    })),
  ];
  return { rows, listed: rows.length > 0, migrated: migrations.length - skipped.length, skipped: skipped.length };
}

// ---------- 按键 ----------

// Keystroke 是表单里一次按键:composing 是输入法还在选词(中文输入法选词时按 Enter 是确定候选词，不是提交或选下拉项)。
export type Keystroke = { key: string; composing: boolean };

// enterSubmits 是这次按键要不要提交表单：按 Enter、又不在输入法选词中。
export const enterSubmits = (k: Keystroke) => k.key === "Enter" && !k.composing;

// DirectoryKey 是目录输入框一次按键要做的事:open 展开下拉,move 把高亮移到 active,pick 填进高亮的那一项,
// submit 收起下拉并提交表单,close 只收起下拉(不关掉弹窗),none 什么都不做(交给输入框自己)。
export type DirectoryKey = { type: "none" | "open" | "pick" | "submit" | "close" } | { type: "move"; active: number };

// directoryKey 是目录输入框的按键：下拉开着时 ↑↓ 在候选间移动(首尾相接)、Enter 填进高亮的那一项，没高亮时 Enter 照常提交;
// 收着时 ↑↓ 展开;Esc 只收起下拉。输入法选词中的按键一概不管。
export function directoryKey(k: Keystroke, state: { open: boolean; active: number; count: number }): DirectoryKey {
  if (k.composing) return { type: "none" };
  const { open, active, count } = state;
  switch (k.key) {
    case "ArrowDown":
    case "ArrowUp":
      if (!open) return { type: "open" };
      if (!count) return { type: "none" };
      return { type: "move", active: k.key === "ArrowDown" ? (active + 1) % count : (active <= 0 ? count : active) - 1 };
    case "Enter":
      return open && active >= 0 && active < count ? { type: "pick" } : { type: "submit" };
    case "Escape":
      return open ? { type: "close" } : { type: "none" };
  }
  return { type: "none" };
}

// ---------- 恢复默认 ----------

// AgentChange 是恢复默认时一个已添加的收录 Agent 要换回的内置值:fields 是和内置值不一样的几项(只用来写出改了什么)。
export type AgentChange = { agent: Agent; defaults: Agent; fields: ("name" | DirectoryKind)[] };

const sameDirectories = (a: string[] | null = [], b: string[] | null = []) =>
  (a ?? []).length === (b ?? []).length && (a ?? []).every((d, i) => cleanPath(d) === cleanPath((b ?? [])[i]));
const sameSet = (a: string[] | null = [], b: string[] | null = []) => sameDirectories((a ?? []).map(cleanPath).sort(), (b ?? []).map(cleanPath).sort());

function changedFields(agent: Agent, defaults: Agent): AgentChange["fields"] {
  const fields: AgentChange["fields"] = [];
  if (agent.name !== defaults.name) fields.push("name");
  for (const kind of ["global_directories", "project_directories"] as const) if (!sameDirectories(agent[kind], defaults[kind])) fields.push(kind);
  if (!sameSet(agent.detect_directories, defaults.detect_directories)) fields.push("detect_directories");
  return fields;
}

// agentReset 把技能库给出的恢复默认计划(PlanResetAgents)整理成确认框要写的，不另算规则:changed / changedPooled 是换回内置值的
// 已添加 / 候选池里的收录 Agent;removed / removedPooled 是移除的手填 Agent;kept 是还有别的 Agent 在读、总是保留的文件夹
// (shared_with 按计划，不算同批移除的手填 Agent);own 是只有这批手填 Agent 在读、选删除时删掉的;migrations 是要迁移的各处
// (to 是新的文件夹);detached 是迁移后旧文件夹被搬走、别处的符号链接指着它、库先给那几处换上独立一份的。各处按快照里的巡检结果写。
export function agentReset(s: Snapshot, plan: ResetAgentsPlan) {
  const inspected = (p: SkillScope): SkillScopeInspection =>
    s.skillScopes.find((x) => x.skill_id === p.skill_id && scopeKey(x.scope) === scopeKey(p.scope)) ?? (p as SkillScopeInspection);
  const reset = plan.reset ?? [];
  const removed = plan.removed ?? [];
  const migrations = (plan.migrations ?? []).map((m) => ({ place: inspected(m.place), to: m.to, leaves: m.leaves }));
  const changed = reset.filter((r) => r.added && r.defaults).map((r) => ({ agent: r.agent, defaults: r.defaults!, fields: changedFields(r.agent, r.defaults!) }));
  return {
    changed,
    changedPooled: reset.filter((r) => !r.added).map((r) => r.agent),
    removed: removed.filter((r) => r.added).map((r) => r.agent),
    removedPooled: removed.filter((r) => !r.added).map((r) => r.agent),
    kept: (plan.kept ?? []).map((p) => ({ ...inspected(p), shared_with: p.shared_with })),
    own: (plan.removable ?? []).map(inspected),
    migrations,
    detached: detachedBy(folderPlaces(s, migrations.filter((m) => m.leaves).map((m) => m.place))),
    nothing: !reset.length && !removed.length,
  };
}

// ---------- 全局目录在哪里 ----------

// asksWhere 是第一次打开时要不要先问 Agent 装在 Windows 本机还是哪个 WSL 发行版：这台电脑有 WSL 发行版、设置里还没选发行版时才问，
// 问过再按那里检测到的添加 Agent。
export function asksWhere(distros: string[], chosen: string): boolean {
  return distros.length > 0 && !chosen;
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
// 判断两个写法是不是同一个目录按 rules(见 directoryRules)。
export function opensWhileTyping(options: DirectoryOptions, typed: string, taken: string[], rules: DirectoryRules): boolean {
  const shown = filterDirectoryOptions(options, typed, taken, rules);
  return [...shown.defaults, ...shown.others].some((d) => comparable(d, rules) !== comparable(typed.trim(), rules));
}

export function filterDirectoryOptions(options: DirectoryOptions, typed: string, taken: string[], rules: DirectoryRules): DirectoryOptions {
  const q = cleanPath(typed.trim()).toLowerCase();
  const keep = (d: string) => !taken.some((x) => comparable(x.trim(), rules) === comparable(d, rules)) && (!q || d.toLowerCase().includes(q));
  return { defaults: options.defaults.filter(keep), others: options.others.filter(keep) };
}
