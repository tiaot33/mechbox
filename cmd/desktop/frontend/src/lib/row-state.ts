// 列表一行的状态写什么(rows.tsx 只管画):库里的技能一行的提醒与更新——同名、装出去的几处、来源那边的情况、有你的修改、
// 库里有同名的、目录里改过、提到的技能找不到——和没有提醒时写的那句(随组安装、来自全局、来源已删除);
// 未纳管技能一行归谁管、能不能入库、来源和链接。不碰界面、不调技能库，单元测试见 row-state.test.ts。
// 一行只画最要紧的一条、其余写 +N:这里给出的几条已经按画出来的先后排好(lib/level 的 byUrgency;只画图标、不写字的按 byLevel),
// 第一条就是行上画的那一条，点图标做的也是它的事;components/ui/badge 的 AlertIcon 照这个先后画。
// 来源那边的情况取 lib/source-state 的结论，和节标题的计数、更新页、左栏的更新数说的是同一回事。
import { SkillScopeStatus, type MissingMention, type Skill, type SkillScopeInspection, type UnmanagedSkill } from "@bindings/internal/library";
import { messages } from "./i18n";
import { installedWhere, needsReinstall, reinstallAlerts, skillScopeIn, placesAt, skillScopeStatus } from "./installs";
import { byLevel, byUrgency, type Mark } from "./level";
import type { Snapshot } from "./library";
import { missingAt, missingNames } from "./mentions";
import { shortPath } from "./paths";
import { eachPlace, globalScopesSeenFrom, placeLabel } from "./places";
import { sameNameInLibrary, sameNameNote, skillSameNameNote } from "./same-name";
import { groupName, scopeKey, type ScopeKey } from "./scope";
import { sourceState } from "./source-state";
import { extraCopyNote, folderNotes, identicalIn, identicalState, notTakenOver, skillFileProblemText, unmanagedIn } from "./unmanaged";

// RowAlert 是一行状态里的一条：级别、一句话(label)、悬停的说明(tip);className 是测试钩子。
// plan 表示点它打开这个技能所在集合的更新计划;twins 是库里同名的另外几个技能，点它弹出菜单选一个打开。这两样由行组件接上。
export interface RowAlert {
  tone: Mark;
  label: string;
  tip?: string;
  className?: string;
  plan?: boolean;
  twins?: Skill[];
}

// Plain 是没有提醒时最后一列写的一句，title 是悬停的说明。
export interface Plain {
  text: string;
  title?: string;
}

// RowState 是一行的状态：alerts 是提醒与更新(都没有时为空),已按画出来的先后排好，第一条是行上画的那一条;
// plain 是没有提醒时写的那句(不用写时没有)。
export interface RowState {
  alerts: RowAlert[];
  plain?: Plain;
}

// RowAt 是作用域页、项目页上的一行所在的作用域;fromGlobal 表示这一行是同一个 Agent 全局装的(项目里也看得到)。技能库页的行没有。
export interface RowAt {
  scope: ScopeKey;
  fromGlobal?: boolean;
}

// RowInstall 是一个技能在这一行所在作用域(at;技能库页的行没有)的安装情况，技能库页的行都为空:
// - here:装在这里的那一处(来自全局的行没有);合成一处的几个 Agent 各自装在不同目录时是这一行的 Agent 自己认的那一处;
// - places:这一行说的各处：装在这里的(合成一处的几个 Agent 各自的都算，见 lib/installs 的 placesAt),来自全局的是全局那一处;
//   状态、开关和重新安装都覆盖其中每一处;
// - direct:places 里有直接安装(开关能卸载);
// - identical:这里有一份和它一模一样、不是库装的文件夹(就是这个技能，行首是"入库");
// - place:最后一列说的那一处，来自全局的是全局那一处，否则是 here;
// - via:places 随哪几个组安装;
// - foreign:identical 那一份提交在项目仓库里或链到 Agent 目录以外，归团队、归作者自己管，只是内容和库里的一样:
//   行上不带库里这个技能的组和提醒，免得看上去像是库装过来的那一份，只留这个文件夹自己的同名提醒。
export interface RowInstall {
  at?: RowAt;
  here?: SkillScopeInspection;
  places: SkillScopeInspection[];
  direct: boolean;
  identical?: UnmanagedSkill;
  place?: SkillScopeInspection;
  via: string[];
  foreign: boolean;
}

// inOrder 把几条按画出来的先后排好：labeled 是图标后面写字的(列表一行的状态)，严重、注意在前，接着是能做的更新、可能改名，提示最后;
// 只画图标的按级别从重到轻，更新、可能改名在最后。同一级的保持原来的先后。
function inOrder(alerts: RowAlert[], labeled: boolean): RowAlert[] {
  const by = labeled ? byUrgency : byLevel;
  return [...alerts].sort((a, b) => by(a.tone, b.tone));
}

export function rowInstall(s: Snapshot, skill: Skill, at?: RowAt): RowInstall {
  if (!at) return { places: [], direct: false, via: [], foreign: false };
  const hereAll = at.fromGlobal ? [] : placesAt(s, skill.id, at.scope);
  const here = at.fromGlobal ? undefined : (skillScopeIn(s, skill.id, at.scope) ?? hereAll[0]);
  const identical = !here && !at.fromGlobal ? identicalIn(s, at.scope, skill.id) : undefined;
  // 来自全局的：合成一处的几个 Agent 各自的全局都算(它们的全局目录可以不同),同一个文件夹只算一处。
  const globals = at.fromGlobal
    ? eachPlace(
        s,
        globalScopesSeenFrom(s, at.scope).flatMap((key) => skillScopeIn(s, skill.id, key) ?? []),
      )
    : [];
  const place = at.fromGlobal ? globals[0] : here;
  const places = at.fromGlobal ? globals : hereAll;
  const groups = [...new Set(places.flatMap((p) => p.groups ?? []))];
  return {
    at,
    here,
    places,
    direct: places.some((p) => p.direct),
    identical,
    place,
    via: groups.map((id) => groupName(s, id)),
    foreign: !!identical && !notTakenOver(identical),
  };
}

// skillRowState 是库里一个技能一行的状态;install 是 rowInstall 算出的这一行的安装情况。技能库页的行(没有 at)说它装出去的各处，
// 作用域页、项目页的行只说这一处。
// elsewhere 是这里那一份不是库装的文件夹在同一层的别处同名、内容不同的那几处的地名(技能库比出来的，见 same-name 的 useSameNameCopies)。
export function skillRowState(s: Snapshot, skill: Skill, install: RowInstall, elsewhere?: string[]): RowState {
  const t = messages().list;
  const { at, here, identical, foreign } = install;
  // 作用域页说这一处的同名后果;技能库页说它装出去的各处。
  // 合成一处的几个 Agent 各自装在不同目录时，每个文件夹的同名都说。
  const folders = install.places.length && !at?.fromGlobal ? install.places.map((p) => p.folder) : here ? [here.folder] : identical ? [identical.folder] : [];
  const same = at ? joinNotes(folders.map((folder) => sameNameNote(s, at.scope, folder))) : skillSameNameNote(s, skill.id);
  const own = [same && sameNameAlert(same), sameNameCopiesAlert(skill.name, elsewhere)];
  const alerts = [...own, ...(foreign ? [] : skillAlerts(s, skill, install))].filter((x): x is RowAlert => !!x);
  if (!at) return { alerts: inOrder(alerts, true), plain: sourceState(skill).kept ? { text: t.keptDeleted, title: t.keptDeletedTitle } : undefined };
  if (!identical) return { alerts: inOrder(alerts, true), plain: scopePlain(install, !!at.fromGlobal) };
  // 这里那一份不是 app 装的：先写这一句和归谁管，提醒只跟一个图标;和库里的旧版本一样时标出来。归项目仓库、作者自己管的不跟库里的版本走，和旧版本一样也不提醒。
  const state = identicalState(skill, identical, s.info.home_dir);
  const stale = skillScopeStatus({ status: SkillScopeStatus.SkillScopeStale });
  return {
    alerts: inOrder([...(state.stale && !foreign ? [{ tone: stale.tone as Mark, label: stale.label }] : []), ...alerts], false),
    plain: { text: state.text, title: state.title },
  };
}

// skillAlerts 是库里这个技能自己的提醒与更新(不算同名):装出去的几处(技能库页)或这一处(作用域页)和当前版本不一样、来源那边的情况、
// 有你的修改、库里有同名的、目录里改过、提到的技能在这里找不到。
function skillAlerts(s: Snapshot, skill: Skill, { at, here, places: covered }: RowInstall): (RowAlert | false | undefined)[] {
  const t = messages().list;
  const places: RowAlert[] = at ? placeAlerts(s, covered) : reinstallAlerts(s, skill.id);
  const twins = sameNameInLibrary(s, skill);
  // 装在这里的技能提到的、在这里找不到的技能(按规则和已存下的 AI 判断结论)。
  // 目录列表相同、合成一处的几个 Agent 是一行，它们各自找不到的都算(见 lib/mentions 的 missingAt)。
  const unfound = at && here ? missingAt(s, s.missingMentions, covered.map((p) => ({ skill_id: skill.id, scope: p.scope }))) : [];
  // 装在这里的技能，这里还有和它一样的文件夹(多出来的几份，见 lib/unmanaged 的 extraCopy):提示一下，悬停写出它们在哪里。
  const extras = at && here ? unmanagedIn(s, at.scope).filter((u) => u.skill_id === skill.id) : [];
  const extra = here && extras.length > 0 ? extraCopyNote(skill, here) : undefined;
  return [
    // 在 app 外更新到了作者新版的那一处是更新：点它打开更新计划。
    ...places.map((x) => ({ ...x, plan: x.tone === "update" })),
    ...updateAlerts(skill),
    // 当前版本里有用户的修改(用目录里的修改更新过，或入库时说不清):不论有没有新版本都标出来。
    skill.edited && { tone: "info" as const, label: t.hasEdits, tip: t.hasEditsTitle, className: "has-edits" },
    // 库里同名的几个技能(内容不同，不合并):写出各装在哪里，免得只能靠描述分辨;点它弹出另外几个(来源、装在哪),选一个打开它的详情。
    twins.length > 0 && {
      tone: "info" as const,
      label: t.twinsShort,
      tip: t.twins(twins.length + 1, skill.name, installedWhere(s, skill.id)),
      className: "dup-name-note",
      twins,
    },
    // 本地目录来源：目录里的这个技能改过了(读目录时比出来的),在详情里或集合的「…」里重新读取。
    sourceState(skill).kind === "localChanged" && { tone: "update" as const, label: t.localChanged, tip: t.localChangedTitle, className: "local-changed" },
    unfoundAlert(unfound),
    extra && { tone: "info" as const, label: extra.text, tip: [extra.title, ...extras.map((u) => u.folder)].join("\n"), className: "extra-copy" },
  ];
}

// placeAlerts 是作用域页一行说的各处里和当前版本不一样的，按原因各一条;合成一处的几个 Agent 各自装在不同目录、不止一处时，
// 悬停写出是哪几处。
function placeAlerts(s: Snapshot, places: SkillScopeInspection[]): RowAlert[] {
  const by = new Map<string, RowAlert>();
  for (const p of places.filter(needsReinstall)) {
    const st = skillScopeStatus(p);
    const had = by.get(st.label);
    const at = places.length > 1 ? placeLabel(s, p) : undefined;
    by.set(st.label, had ? { ...had, tip: [had.tip, at].filter(Boolean).join("\n") } : ({ ...st, tip: at } as RowAlert));
  }
  return [...by.values()];
}

// joinNotes 把几个文件夹的同名说明合成一条(相同的只写一次);都没有时为 undefined。
function joinNotes(notes: ({ text: string; title: string } | undefined)[]): { text: string; title: string } | undefined {
  const found = notes.filter((n): n is { text: string; title: string } => !!n);
  if (!found.length) return undefined;
  const join = (xs: string[]) => [...new Set(xs)].join("\n");
  return { text: join(found.map((n) => n.text)), title: join(found.map((n) => n.title)) };
}

// updateAlerts 是一个技能和更新有关的要处理的事，技能库页一行的状态和更新页「要处理的技能」共用一份:
// 有新版本或可能改名(只标计入角标的;两种图标不同，点它打开所在集合的更新计划)、有新版本但更新会换掉用户的修改
// (更新计划里它默认不勾)、作者从来源里删掉了它(不是新版本，是一条提示)。
export function updateAlerts(skill: Skill): RowAlert[] {
  const t = messages();
  const source = sourceState(skill);
  const items: (RowAlert | false)[] = [
    source.changed &&
      (source.kind === "renamed"
        ? { tone: "rename", label: t.drawer.maybeRenamedTo(source.renamedTo), tip: t.list.renamedTitle(source.renamedTo), plan: true }
        : { tone: "update", label: t.list.newVersion, tip: t.list.availableTitle, plan: true }),
    source.todo && source.replacesEdits && { tone: "warn", label: t.list.edited, tip: t.list.editedTitle, className: "update-edited-note" },
    source.todo && source.kind === "deleted" && { tone: "info", label: t.list.deleted, tip: t.list.deletedTitle },
  ];
  return items.filter((x): x is RowAlert => !!x);
}

// scopePlain 是作用域页一行没有提醒时最后一列写的：直接安装是常态，不写;随组安装写随哪几个组，来自全局写来自全局。
function scopePlain({ place, direct, via }: RowInstall, fromGlobal: boolean): Plain | undefined {
  const t = messages().list;
  if (fromGlobal) return { text: t.fromGlobal, title: t.sectionGlobalTitle };
  if (!place || !via.length) return undefined;
  return { text: t.hereState(direct, via) };
}

// unfoundAlert 是装在这里的技能提到的技能在这里找不到：提示一级，悬停写出是哪几个。
function unfoundAlert(unfound: MissingMention[]): RowAlert | undefined {
  if (!unfound.length) return undefined;
  const t = messages().list;
  const names = missingNames(unfound);
  return { tone: "info", label: t.mentionsMissing(names.length), tip: t.mentionsMissingTitle(names), className: "mentions-missing" };
}

// sameNameAlert 标出作用域里和同一个 Agent 另一层级同名的技能：行里只画「注意」一级的图标，鼠标停住时写出后果
// (哪个生效、是否都出现),用户排查"这个项目里用的是不是我装的那个"时不用再点开。
// 说明先写后果，再写是哪个文件夹;两句一样时(技能库页)只写一遍。
function sameNameAlert(note: { text: string; title: string }): RowAlert {
  return { tone: "warn", label: messages().list.sameName, tip: note.title === note.text ? note.text : `${note.text}\n${note.title}`, className: "same-name-badge" };
}

// sameNameCopiesAlert 是行上「别处有同名的，内容不同」的提醒;places 是别处的地名。
function sameNameCopiesAlert(name: string, places: string[] | undefined): RowAlert | undefined {
  if (!places?.length) return undefined;
  const t = messages().list;
  return { tone: "warn", label: t.sameNameCopies, tip: t.sameNameCopiesTitle(name, places), className: "same-name-copies" };
}

// UnmanagedRowState 是作用域里一个不是库装的技能文件夹(未纳管技能)一行的状态:
// - library:它和库里这个技能一样(就是那个技能),行首是"入库";newer:它是库里这个技能在来源里的新版(在 app 外更新过),行首是"更新";
// - invalid:SKILL.md 哪里写错了，不能入库(能入库时为空);
// - state:最后一列逐条写的——来自全局、不能入库、和库里哪个技能的关系或不在技能库里、归谁管与链到哪里;
// - notes:不占一行的说明(哪里写错了、来源、来源是怎么认出来的、符号链接指向哪里),写在描述的悬停提示里，
//   没有描述时描述那一格直接写(detail 的只在提示里);className 是测试钩子;
// - alerts:同名的提醒(和同一个 Agent 另一层级同名、同一层的别处同名不同内容、库里有同名的),已按画出来的先后排好:
//   state 为空时图标后面写最要紧的那一条。
export interface UnmanagedRowState {
  library?: Skill;
  newer?: Skill;
  invalid: string;
  state: { text: string; title: string }[];
  notes: { text: string; className: string; detail?: boolean }[];
  alerts: RowAlert[];
}

// unmanagedRowState 的 fromGlobal 表示这一行在"来自全局"一节里(先写"来自全局");headed 表示在"不在技能库里"一节里(节标题已经写了，不再写);
// copies 是同一层的别处同名、内容不同的那几处的地名;found 是来源记录认不出来源、在 skills.sh 上找到的来源。
export function unmanagedRowState(
  s: Snapshot,
  skill: UnmanagedSkill,
  { fromGlobal = false, headed = false, copies, found }: { fromGlobal?: boolean; headed?: boolean; copies?: string[]; found?: string } = {},
): UnmanagedRowState {
  const t = messages().list;
  const same = sameNameNote(s, scopeKey(skill.scope), skill.folder);
  const library = s.skills.find((x) => x.id === skill.skill_id);
  // 认出是库里技能的不写来源：入库只补记安装，不跟着这个来源(没有来源的技能在技能详情里接上来源)。
  const source = library ? undefined : skill.source_input || found;
  const newer = s.skills.find((x) => x.id === skill.source_latest_of);
  const invalid = skill.invalid ? skillFileProblemText(skill.invalid) : "";
  const state = [
    fromGlobal ? { text: t.fromGlobal, title: t.sectionGlobalTitle } : undefined,
    invalid ? { text: t.cannotFetchShort, title: t.cannotFetchTitle(invalid) } : undefined,
    library
      ? { text: t.sameAsLibrary(library.name), title: t.identicalHereTitle(library.name) }
      : newer
        ? { text: t.sourceLatestOf(newer.name), title: t.sourceLatestOfTitle(newer.name, notTakenOver(skill)) }
        : headed
          ? undefined
          : { text: t.notInLibrary, title: t.notInLibraryTitle },
    ...folderNotes(skill, s.info.home_dir),
  ].filter((x) => !!x);
  const notes = [
    // SKILL.md 写错了的没有描述：描述那一格写哪里写错了(这一格最宽，原因写得下)。
    invalid && { text: t.cannotFetch(invalid), className: "unmanaged-invalid" },
    source && { text: t.from(source), className: "unmanaged-source-label" },
    source && { text: found ? t.foundTitle(found) : t.recordedTitle(source), className: "unmanaged-source-how", detail: true },
    skill.link_target && { text: t.link(shortPath(s, skill.link_target)), className: "unmanaged-status" },
  ].filter((x): x is { text: string; className: string; detail?: boolean } => !!x);
  const alerts = [
    same && sameNameAlert(same),
    sameNameCopiesAlert(skill.name, copies),
    // 库里有同名的技能，而这里和它的哪个版本都不一样(一样的会认成那个技能):入库后会多出一个同名的。
    !library &&
      !newer &&
      sameNameInLibrary(s, { id: 0, name: skill.name }).length > 0 && {
        tone: "info" as const,
        label: t.twinsShort,
        tip: t.libraryTwinTitle(skill.name),
        className: "library-twin",
      },
  ].filter((x): x is RowAlert => !!x);
  return { library, newer, invalid, state, notes, alerts: inOrder(alerts, !state.length) };
}
