// 安装状态：技能装在哪几处、每一处和库里的当前版本一不一样、要不要重新安装，都从快照里的巡检结果派生。
// 逐处列出、数几处、写名字时目录列表相同、合成一处的几个 Agent 算一处，见 lib/places。
import { ErrorCode, SkillScopeStatus, type Group, type Scope, type SkillID, type SkillScopeInspection } from "@bindings/internal/library";
import { messages } from "./i18n";
import { worstLevel, type Level, type Mark, type Tone } from "./level";
import type { Snapshot } from "./library";
import { shortPath } from "./paths";
import { eachPlace, mergedAgents, placeNames, scopeLabel } from "./places";
import { parseScope, placeOf, projectName, scopeKey, type ScopeKey } from "./scope";

// ---------- 装在哪 ----------

export function skillScopesOf(s: Snapshot, skillID: number): SkillScopeInspection[] {
  return s.skillScopes.filter((p) => p.skill_id === skillID);
}

export function skillScopesIn(s: Snapshot, key: ScopeKey): SkillScopeInspection[] {
  return s.skillScopes.filter((p) => scopeKey(p.scope) === key);
}

export function skillScopeIn(s: Snapshot, skillID: number, key: ScopeKey): SkillScopeInspection | undefined {
  return s.skillScopes.find((p) => p.skill_id === skillID && scopeKey(p.scope) === key);
}

// folderKey 是装出去的实际文件夹:几个 Agent 共用一个目录时，它们的这一处是同一个文件夹，只能一起换、一起装回去或卸载。
export const folderKey = (p: SkillScopeInspection) => `${p.skill_id}|${p.folder}`;

// folderPlaces 是和 places 同一个文件夹的各处(含共用这个目录的 Agent),删文件夹时它们一起没了。
export function folderPlaces(s: Snapshot, places: SkillScopeInspection[]): SkillScopeInspection[] {
  const folders = new Set(places.map((p) => `${p.skill_id}|${p.folder}`));
  return s.skillScopes.filter((p) => folders.has(`${p.skill_id}|${p.folder}`));
}

// groupInstallIn 是组在某个作用域的组安装落在哪个作用域上：合成一处的几个 Agent 里任何一个有这个组的组安装都算(代表换了也不变),
// 卸载组安装要落在有记录的那个上;没有时为 undefined。
export function groupInstallIn(s: Snapshot, group: Group, key: ScopeKey): Scope | undefined {
  const scope = parseScope(key);
  const agents = mergedAgents(s, scope.agent, placeOf(scope));
  return s.groupInstalls.find((a) => a.group_id === group.id && placeOf(a.scope) === placeOf(scope) && agents.includes(a.scope.agent))?.scope;
}

// groupStateIn 是组在某个作用域的组安装状态：组安装是独立记录(空组也可以有),有记录即为 on(见 groupInstallIn)。
// 成员各自的直接安装不算组安装。
export function groupStateIn(s: Snapshot, group: Group, key: ScopeKey): "on" | "off" {
  return groupInstallIn(s, group, key) ? "on" : "off";
}

// InstalledWhere 是技能装在哪里，供分辨库里同名的几个技能:places 是前两处("Claude Code · 全局"、"Codex · proj-a"),
// total 是一共几处(超过两处时文案写"等 N 处",为 0 时写"没装在任何地方")。
export interface InstalledWhere {
  places: string[];
  total: number;
}

export function installedWhere(s: Snapshot, skillID: number): InstalledWhere {
  const places = placeNames(s, skillScopesOf(s, skillID));
  return { places: places.slice(0, 2), total: places.length };
}

// installedSummary 是技能库页上一行的"装在哪":全局写出装在哪几个 Agent(按 Agent 列表的先后，后面跟"全局"),
// 项目只有一个时写项目名，多个时写个数。没装在任何地方时 text 为空。title 逐处列出;bad 表示有地方真要重新安装(needsRepair)。
export function installedSummary(s: Snapshot, skillID: SkillID): { text: string; title: string; bad: boolean } {
  const m = messages().model;
  const places = skillScopesOf(s, skillID);
  const globals = s.agents.filter((a) => places.some((p) => !placeOf(p.scope) && p.scope.agent === a.code));
  const projects = [...new Set(places.map((p) => placeOf(p.scope)).filter(Boolean))];
  const text = [
    !globals.length ? "" : m.agentsGlobal(globals.map((a) => a.name)),
    !projects.length ? "" : projects.length === 1 ? projectName(s, projects[0]) : m.projectCount(projects.length),
  ]
    .filter(Boolean)
    .join(m.listSeparator);
  const title = placeNames(s, places).join("\n");
  return { text, title, bad: places.some(needsRepair) };
}

// ---------- 一处和库里的当前版本一不一样 ----------

// folderPresent 是这一处的技能文件夹在这台机器的磁盘上：文件夹不在了、项目不在这台机器上的都不算。
export const folderPresent = (p: Pick<SkillScopeInspection, "status">) =>
  p.status !== SkillScopeStatus.SkillScopeMissing && p.status !== SkillScopeStatus.SkillScopeRootMissing;

// canInstall 是能装到的地方：项目不在这台机器上的装不上。
export const canInstall = (p: Pick<SkillScopeInspection, "status">) => p.status !== SkillScopeStatus.SkillScopeRootMissing;

// needsReinstall 是这一处和库里的当前版本不一样、可以重新安装。项目不在这台机器上的不算：那里装不回去，要处理的是项目本身，
// 提醒只在项目上(作用域栏、设置页的项目行),不落到装在那里的每个技能上。被手动改过的也算(重新安装时默认不覆盖),
// 但那是用户自己的修改，不是要修的问题：提醒和计数用 needsRepair,被手动改过的另用 editedHere。
export function needsReinstall(p: SkillScopeInspection): boolean {
  return p.status !== SkillScopeStatus.SkillScopeCurrent && canInstall(p);
}

// editedHere 是这一处被手动改过(用户或 Agent 直接在 Agent 目录里改的):是用户自己的修改，可以用这里的修改更新技能库。
// 在 app 外更新到了作者新版的不算(那是更新)。
export const editedHere = (p: Pick<SkillScopeInspection, "status" | "source_latest">) =>
  p.status === SkillScopeStatus.SkillScopeModified && !p.source_latest;

// needsRepair 是这一处真要重新安装：文件夹不在了、版本落后于库里。被手动改过的、在 app 外更新到作者新版的都不算。
export const needsRepair = (p: SkillScopeInspection) =>
  canInstall(p) && (p.status === SkillScopeStatus.SkillScopeMissing || p.status === SkillScopeStatus.SkillScopeStale);

// sourceLatest 为 true(见 SkillScopeInspection.source_latest)时被改过的那一处是在 app 外更新到了作者的新版，不说"被手动改过"。
export function skillScopeStatus(status: SkillScopeStatus, sourceLatest = false): { label: string; tone: Tone } {
  const m = messages();
  if (status === SkillScopeStatus.SkillScopeModified && sourceLatest) return { label: m.model.statusSourceLatest, tone: "update" };
  return {
    [SkillScopeStatus.SkillScopeCurrent]: { label: m.model.statusCurrent, tone: "plain" as const },
    [SkillScopeStatus.SkillScopeStale]: { label: m.model.statusStale, tone: "warn" as const },
    // 被手动改过是用户自己的修改，不是要修的错误：提示一级。
    [SkillScopeStatus.SkillScopeModified]: { label: m.model.statusModified, tone: "info" as const },
    [SkillScopeStatus.SkillScopeMissing]: { label: m.model.statusMissing, tone: "critical" as const },
    // 项目不在这台机器上是项目的事：技能详情里那一行照实写出来，不算这个技能的提醒。
    [SkillScopeStatus.SkillScopeRootMissing]: { label: m.model.statusRootMissing, tone: "plain" as const },
    [SkillScopeStatus.$zero]: { label: m.common.unknown, tone: "plain" as const },
  }[status];
}

// reinstallLevel 是几处安装里和当前版本不一样的那些最重到哪一级提醒(作用域栏 Agent 行、作用域页摘要的提醒图标按它取级别):
// 有文件夹不在了的是严重，版本落后的是注意，只有被手动改过的是提示;在 app 外更新到了作者新版的不是提醒(是更新),不算;都一样时没有。
export function reinstallLevel(places: SkillScopeInspection[]): Level | undefined {
  return worstLevel(places.filter(needsReinstall).map((p) => skillScopeStatus(p.status, p.source_latest).tone));
}

// ReinstallAlert 是一个技能在几处和当前版本不一样的原因之一：级别、原因(label)、tip 逐行写出是哪几处。
export type ReinstallAlert = { tone: Mark; label: string; tip: string };

// reinstallAlerts 是一个技能和当前版本不一样的那几处，按原因(文件夹不在了、被手动改过……)各合成一条;
// 技能库页把它们合进这一行的状态。真要重新安装的(needsRepair)tip 先写一句要重新安装，免得「旧版本」被读成库里的是旧版;
// 被手动改过的只列地方(是用户自己的修改);在 app 外更新到了作者新版的那条是更新(tone 为 update),也只列地方。
export function reinstallAlerts(s: Snapshot, id: SkillID): ReinstallAlert[] {
  const m = messages().model;
  const by = new Map<string, ReinstallAlert>();
  for (const p of eachPlace(s, skillScopesOf(s, id)).filter(needsReinstall)) {
    const st = skillScopeStatus(p.status, p.source_latest);
    if (st.tone === "plain") continue;
    const at = scopeLabel(s, p.scope);
    const had = by.get(st.label);
    const head = needsRepair(p) ? `${m.reinstallAt}\n` : editedHere(p) ? `${m.editedAt}\n` : "";
    by.set(st.label, had ? { ...had, tip: `${had.tip}\n${at}` } : { tone: st.tone, label: st.label, tip: head + at });
  }
  return [...by.values()];
}

// ---------- 装的时候 ----------

// linkNote 是库要写一处符号链接时的说明:写成独立的文件夹，不再写进链接指向的地方。
export const linkNote = (s: Snapshot, target: string) => messages().model.linkNote(shortPath(s, target));

// occupierOf 是装不上时占着文件夹的另一个同名技能，界面上能点开它:candidate 是安装计划的 occupier_skill_id,
// 或安装失败时错误里的 details.skill_id(见 errorOccupier)。是这个技能自己(它的另一个作用域算出同一个目录)、不在快照里时没有。
export function occupierOf(s: Snapshot, id: SkillID, candidate: SkillID | undefined): SkillID | undefined {
  return candidate && candidate !== id && s.skills.some((x) => x.id === candidate) ? candidate : undefined;
}

// errorOccupier 是安装失败的错误里占着安装目标的技能(FailedPrecondition 的 details.skill_id),见 occupierOf。
// 安装计划算的时候还没被占、执行时才被同一批里先装的同名技能占上的，只能从这里知道。
export function errorOccupier(s: Snapshot, id: SkillID, err: { code: ErrorCode; details?: { skill_id?: SkillID } } | null | undefined): SkillID | undefined {
  return err?.code === ErrorCode.FailedPrecondition ? occupierOf(s, id, err.details?.skill_id) : undefined;
}
