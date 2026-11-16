// 列表的行：库里的技能(技能库页与作用域页;更新页「要处理的技能」另有一种只写要处理什么的行)与作用域里的未纳管技能。一个技能一行，列对齐，
// 平常的信息用淡色，只有要处理的(有新版本、来源里已删除、同名、要重新安装)才上色。
// 作用域页与项目页的行首一格只表示 Agent 在这里用不用：开 = 在用，关 = 没在用;最后一列写是谁装的。
// 未纳管技能不在技能库里，行首没有开关，是"入库"按钮;在"不在技能库里"一节里时最后一列不再写这句。
// 库里技能的行点开详情抽屉，可以拖到右栏作用域(直接安装)或左栏、节标题上的组(加入该组);
// 技能库页、作用域页与项目页的行开头有勾选框，勾选的几个技能用底部的操作栏一起操作，拖勾选着的一行就是拖它们全部。
import { useEffect, useRef } from "react";
import { AppService } from "@bindings/cmd/desktop";
import { SkillScopeStatus, type Skill, type SkillID, type SkillScopeInspection, type UnmanagedSkill } from "@bindings/internal/library";
import { useT, type Messages } from "@/lib/i18n";
import { foundSource, useFoundSources, useSnapshot } from "@/lib/library";
import {
  skillFileProblemText,
  identicalIn,
  localChanged,
  notTakenOver,
  deletedInSource,
  folderNotes,
  identicalState,
  globalScopesSeenFrom,
  groupName,
  changePending,
  deletionPending,
  installedSummary,
  installedWhere,
  needsReinstall,
  parseScope,
  pending,
  reinstallAlerts,
  renamedTo,
  sameNameInLibrary,
  sameNameNote,
  scopeKey,
  skillSameNameNote,
  shortPath,
  skillScopeIn,
  skillScopeStatus,
  sourceKindLabel,
  sourceLabel,
  sourceShort,
  translatedDescription,
  updateAvailable,
  type ScopeKey,
} from "@/lib/model";
import { ui } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { doneFlash, doneFresh, toggleDrawer, useView } from "@/lib/view";
import { fetchUnmanaged } from "@/features/fetch/fetch-unmanaged";
import { installSkill, uninstallSkill } from "@/features/install/install";
import { openUpdate } from "@/features/update/update";
import { AlertIcon, Badge, MarkIcon, type Alert } from "@/components/ui/badge";
import type { Mark } from "@/lib/level";
import { Button } from "@/components/ui/button";
import { Switch, SwitchMark } from "@/components/ui/switch";
import { useDnD } from "@/components/app/dnd";
import { Icon } from "@/components/ui/icon";
import { Truncated } from "@/components/app/parts";
import { PickBox } from "./pick";
import { sameNameAlert, sameNameCopiesAlert, useSameNameCopies } from "./same-name";
import { flash } from "./flash";

// 列宽：技能库页是 勾选框 | 名字 | 描述 | 装在哪 | 状态(要处理的事);作用域页是 勾选框 | 开关 | 名字 | 描述 | 在这里的状态。
// 列表窄了(开着技能详情)时每行两层：描述挪到名字下面;技能库页的装在哪和状态上下叠在最后一列(where、status),
// 作用域页的状态跨两层。名字、装在哪(在这里的状态)和状态仍然看得全;列表的宽度按 .a-list 这个容器算。
// 各列宽下 desc、where、status 是窄的时候它们放的格子，勾选框和开关跨两层、上下居中(span)。
const layouts = {
  library: {
    cols: "grid-cols-[14px_minmax(140px,220px)_minmax(0,1fr)_minmax(80px,180px)_minmax(96px,170px)] @max-[600px]/list:grid-cols-[14px_minmax(0,1fr)_minmax(110px,170px)]",
    desc: "@max-[600px]/list:col-start-2 @max-[600px]/list:row-start-2",
    where: "@max-[600px]/list:col-start-3 @max-[600px]/list:row-start-1",
    status: "@max-[600px]/list:col-start-3 @max-[600px]/list:row-start-2 @max-[600px]/list:justify-start",
  },
  scope: {
    cols: "grid-cols-[14px_30px_minmax(140px,220px)_minmax(0,1fr)_minmax(90px,180px)] @max-[600px]/list:grid-cols-[14px_30px_minmax(0,1fr)_minmax(80px,140px)]",
    desc: "@max-[600px]/list:col-start-3 @max-[600px]/list:row-start-2",
    where: "@max-[600px]/list:row-span-2",
    status: "",
  },
};
// 技能名那条线离行的左边多远，记在列表容器的 --name-indent 上(nameIndentVars),一页里所有文字都从这条线开始:
// 技能库页是 42:行的左内边距 12(px-3)+ 勾选框那一列 14 + 列间距 16;作用域页、项目页的行多一列开关，是 88(再加开关 30 + 列间距 16)。
// 节标题、「没装的」折叠行用 pickColumn 留出勾选框那一列，再按 --name-indent 把文字推到这条线上;摘要、说明行用 nameIndent 直接缩进。
export const nameIndentVars = "[--name-indent:42px] data-[page=project]:[--name-indent:88px] data-[page=scope]:[--name-indent:88px]";
export const pickColumn = "w-3.5";
export const nameIndent = "pl-(--name-indent)";

// span 是窄的时候跨两层的格子(勾选框、开关)。
const span = "@max-[600px]/list:row-span-2";

// 行高固定：宽的时候一层(32 px),窄的时候两层(48 px)。行里的每一格都只有一行字，放不下就截断;
// 多出来的说明(同名的后果、符号链接指向哪里、库里同名的几个各装在哪)收进图标或文字的悬停提示。
const rowClass =
  "grid h-8 content-center items-center gap-x-4 border-b border-row-line px-3 text-body last:border-b-0 @max-[600px]/list:h-12 @max-[600px]/list:gap-y-0";


// SkillRow 是库里一个技能的一行。scope 为空时是技能库页的样子;有 scope 时是作用域页、项目页的样子:
// 开头一格表示 Agent 在这里用不用(见下),最后一列写是谁装的;fromGlobal 表示它是同一个 Agent 全局装的(项目里也看得到),这里不能开关。
// 这里有一份和它一模一样、不是库装的文件夹时(identicalIn),开头一格是"入库",最后一列写不是 app 装的。
// 行的最前面是勾选框。
export function SkillRow(props: {
  skill: Skill;
  scope?: ScopeKey;
  fromGlobal?: boolean;
  showSource: boolean;
  hideGroup?: number; // 所在的组的节：每行都属于它，不再重复显示
}) {
  const s = useSnapshot();
  const { skill, scope } = props;
  const dnd = useDnD();
  const selected = useView((v) => v.drawer === skill.id);
  const picked = useView((v) => v.picked.has(skill.id));
  const t = useT();
  const here = scope && !props.fromGlobal ? skillScopeIn(s, skill.id, scope) : undefined;
  // 这里有一份和它一模一样、不是库装的文件夹：就是这个技能，行首是"入库"。
  const identical = scope && !here && !props.fromGlobal ? identicalIn(s, scope, skill.id) : undefined;
  // 最后一列说的那一处：来自全局的是全局那一处，否则是这里。
  const place =
    scope && props.fromGlobal ? globalScopesSeenFrom(s, scope).map((key) => skillScopeIn(s, skill.id, key)).find(Boolean) : here;
  const via = (place?.groups ?? []).map((id) => groupName(s, id));
  // 作用域页说这一处的同名后果;技能库页说它装出去的各处。
  const folder = here?.folder ?? identical?.folder;
  const same = scope ? (folder ? sameNameNote(s, scope, folder) : undefined) : skillSameNameNote(s, skill.id);
  // 这里的那一份提交在项目仓库里或链到 Agent 目录以外：归团队、归作者自己管，只是内容和库里的一样。
  // 行上不带库里这个技能的组和提醒，免得看上去像是库装过来的那一份;只留这个文件夹自己的同名提醒。
  const foreign = !!identical && !notTakenOver(identical);
  // 这一份不是库装的、同一层的别处有同名不同内容的：和「不在技能库里」的行一样说出来。
  const elsewhere = useSameNameCopies(identical ? scope : undefined).copies.get(identical?.folder ?? "");
  const groups = foreign ? [] : s.groups.filter((g) => g.members?.includes(skill.id) && g.id !== props.hideGroup);
  const twins = sameNameInLibrary(s, skill);
  const translated = translatedDescription(skill);
  const ref = useFlash(skill.id, scope);
  const plan = () => void openUpdate(skill.collection_id, { focus: skill.id });
  // 这一行的状态：技能本身的、它装出去的几处(技能库页)或这一处(作用域页)的提醒与更新，在最后一列只写最要紧的一条，
  // 鼠标停住时逐条列出;一行只有这一个图标。
  const placeAlerts: Alert[] = scope
    ? [place && needsReinstall(place) && (skillScopeStatus(place.status, place.source_latest) as Alert)].filter((x): x is Alert => !!x)
    : reinstallAlerts(s, skill.id);
  const alerts: Alert[] = (foreign ? [same && sameNameAlert(same, t.list.sameName), sameNameCopiesAlert(t.list, skill.name, elsewhere)] : [
    same && sameNameAlert(same, t.list.sameName),
    sameNameCopiesAlert(t.list, skill.name, elsewhere),
    ...placeAlerts.map((x) => ({ ...x, onClick: x.tone === "update" ? plan : undefined })),
    ...updateAlerts(t, skill, plan),
    // 当前版本里有用户的修改(用目录里的修改更新过，或入库时说不清):不论有没有新版本都标出来。
    skill.edited && { tone: "info" as const, label: t.list.hasEdits, tip: t.list.hasEditsTitle, className: "has-edits" },
    // 库里同名的几个技能(内容不同，不合并):写出各装在哪里，免得只能靠描述分辨。
    twins.length > 0 && { tone: "info" as const, label: t.list.twinsShort, tip: t.list.twins(twins.length + 1, skill.name, installedWhere(s, skill.id)), className: "dup-name-note" },
    // 本地目录来源：目录里的这个技能改过了(读目录时比出来的),在详情里或集合的「…」里重新读取。
    localChanged(skill) && { tone: "update" as const, label: t.list.localChanged, tip: t.list.localChangedTitle, className: "local-changed" },
  ]).filter((x): x is Alert => !!x);
  const keptDeleted = deletedInSource(skill) && skill.kept_deleted ? { text: t.list.keptDeleted, title: t.list.keptDeletedTitle } : undefined;

  const toggle = () => {
    if (!scope) return;
    const sc = parseScope(scope);
    // 只随组安装的：开关不改安装，说明随哪个组装、怎样在这里不用它。
    if (here && !here.direct) return ui.toast(t.list.groupOnly(skill.name, via));
    void (here ? uninstallSkill(skill.id, sc) : installSkill(skill.id, sc));
  };

  const layout = layouts[scope ? "scope" : "library"];

  return (
    <article
      ref={ref}
      className={cn(
        "skill-row group/row cursor-grab transition-colors hover:bg-fill active:cursor-grabbing data-[flash]:bg-primary-soft",
        rowClass,
        layout.cols,
        picked && "bg-primary-soft/45 hover:bg-primary-soft/60",
        selected && "bg-primary-soft hover:bg-primary-soft",
      )}
      data-name={skill.name}
      data-skill-id={skill.id}
      data-scope={scope}
      data-selected={selected || undefined}
      data-picked={picked || undefined}
      onClick={() => toggleDrawer(skill.id)}
      draggable
      onDragStart={(e) => {
        const all = useView.getState().picked;
        if (picked) dnd.start(e, { kind: "skills", ids: [...all] }, all.size > 1 ? t.list.pickedDrag(all.size) : skill.name);
        else dnd.start(e, { kind: "skill", id: skill.id }, skill.name);
      }}
      onDragEnd={dnd.end}
    >
      <PickBox id={skill.id} className={span} />
      {scope && (
        <span onClick={(e) => e.stopPropagation()} className={cn("flex", span)}>
          {/* 开 = Agent 在这里用着：直接安装是绿色，只随组安装和来自全局是灰色(来自全局的不能点);关 = 没装在这里。 */}
          {props.fromGlobal ? (
            <SwitchMark title={t.list.sectionGlobalTitle} />
          ) : identical ? (
            <TakeOverButton identical={identical} name={skill.name} />
          ) : (
            <Switch
              checked={!!here}
              tone={here && !here.direct ? "group" : undefined}
              onCheckedChange={toggle}
              title={!here ? t.list.installDirect : here.direct ? t.list.uninstallDirect : t.list.groupOnly(skill.name, via)}
            />
          )}
        </span>
      )}
      <div className="flex min-w-0 items-center gap-1.5 overflow-hidden">
        {/* 一行：名字放不下时截断，后面是组名;提醒与更新都在最后一列。 */}
        <span
          className={cn("skill-name min-w-0 truncate text-body font-semibold", scope && !here && !identical && !props.fromGlobal && "font-normal text-foreground-secondary")}
          title={skill.note || undefined}
        >
          {skill.name}
        </span>
        {groups.length > 0 && (
          // 组名不是状态，不套标签的框：只是标注档的灰字，和前面「要处理」的标签分得开。
          <span className="skill-groups max-w-24 min-w-0 shrink-[100] truncate text-caption text-muted-foreground" title={t.list.groupsTitle(groups.map((g) => g.name))}>
            {groups[0].name}
            {groups.length > 1 && ` +${groups.length - 1}`}
          </span>
        )}
      </div>
      <div className={cn("min-w-0", layout.desc)}>
        {/* 有界面语言的译文时显示译文，鼠标停住总能看到原文;没有译文时和原来一样，截断了才提示。 */}
        <Truncated
          always={!!translated}
          tip={
            translated ? (
              <>
                <div>{translated}</div>
                <div className="mt-1.5 opacity-80">{t.list.originalTitle(skill.description)}</div>
              </>
            ) : (
              skill.description
            )
          }
        >
          <p className="skill-desc truncate text-small text-muted-foreground">
            {props.showSource && !foreign && (
              <span className="text-muted-foreground" title={`${sourceKindLabel(skill.source.type)} · ${sourceLabel(s, skill.source)}`}>
                {sourceShort(s, skill.source)} ·{" "}
              </span>
            )}
            {translated ?? skill.description}
          </p>
        </Truncated>
      </div>
      {scope ? (
        <div className={cn("flex min-w-0", layout.where)}>
          {identical ? (
            <IdenticalState identical={identical} skill={skill} alerts={alerts} foreign={foreign} />
          ) : (
            <RowState alerts={alerts} plain={scopePlain(t.list, place, via, !!props.fromGlobal)} className="skill-state" />
          )}
        </div>
      ) : (
        <>
          <div className={cn("flex min-w-0", layout.where)}>
            <Where skill={skill} />
          </div>
          <div className={cn("flex min-w-0", layout.status)}>
            <RowState alerts={alerts} plain={keptDeleted} className="skill-status" />
          </div>
        </>
      )}
    </article>
  );
}

// updateAlerts 是一个技能和更新有关的要处理的事，技能库页一行的状态和更新页「要处理的技能」共用一份:
// 有新版本或可能改名(只标计入角标的;两种图标不同，plan 给了时点它打开所在集合的更新计划)、有新版本但更新会换掉用户的修改
// (更新计划里它默认不勾)、作者从来源里删掉了它(不是新版本，是一条提示)。
function updateAlerts(t: Messages, skill: Skill, plan?: () => void): Alert[] {
  const to = renamedTo(skill);
  const items: (Alert | false)[] = [
    changePending(skill) &&
      (to
        ? { tone: "rename" as const, label: t.drawer.maybeRenamedTo(to), tip: t.list.renamedTitle(to), onClick: plan }
        : { tone: "update" as const, label: t.list.newVersion, tip: t.list.availableTitle, onClick: plan }),
    pending(skill) && updateAvailable(skill) && !!skill.edited && { tone: "warn" as const, label: t.list.edited, tip: t.list.editedTitle, className: "update-edited-note" },
    deletionPending(skill) && { tone: "info" as const, label: t.list.deleted, tip: t.list.deletedTitle },
  ];
  return items.filter((x): x is Alert => !!x);
}

// 更新页「要处理的技能」的列：名字 | 要处理什么 | 来自哪个来源 | 装在哪(更新时一起换成新版的地方)。窄的时候要处理什么挪到名字下面，
// 来源和装在哪上下叠在右边。
const updateCols =
  "grid-cols-[minmax(120px,200px)_minmax(0,1fr)_minmax(80px,180px)_minmax(80px,160px)] @max-[600px]/list:grid-cols-[minmax(0,1fr)_minmax(110px,170px)]";

// UpdateSkillRow 是更新页「要处理的技能」里的一行：一眼看出这个技能要处理的是什么事(updateAlerts 逐条写出，各用自己的图标和说法);
// 点这一格打开所在集合的更新计划(只勾这一个)。
// 描述、最近变化这些和要处理什么无关的不列。点行的其他地方照常打开技能详情。
export function UpdateSkillRow({ skill }: { skill: Skill }) {
  const s = useSnapshot();
  const tt = useT();
  const t = tt.list;
  const selected = useView((v) => v.drawer === skill.id);
  return (
    <article
      className={cn(
        "skill-row update-skill-row transition-colors hover:bg-fill",
        rowClass,
        updateCols,
        selected && "bg-primary-soft hover:bg-primary-soft",
      )}
      data-name={skill.name}
      data-skill-id={skill.id}
      data-selected={selected || undefined}
      onClick={() => toggleDrawer(skill.id)}
    >
      <span className="skill-name min-w-0 truncate text-body font-semibold" title={skill.note || undefined}>
        {skill.name}
      </span>
      <div className="flex min-w-0 @max-[600px]/list:col-start-1 @max-[600px]/list:row-start-2">
        <button
          type="button"
          className="update-what flex min-w-0 items-center gap-3 overflow-hidden rounded-control text-left outline-none hover:underline focus-visible:ring-3 focus-visible:ring-primary-soft"
          title={t.whatTitle}
          onClick={(e) => {
            e.stopPropagation();
            void openUpdate(skill.collection_id, { focus: skill.id });
          }}
        >
          {updateAlerts(tt, skill).map((x) => (
            <Badge key={x.label} tone={x.tone} className={cn("min-w-0 shrink", x.className)} title={x.tip}>
              <span className="truncate">{x.label}</span>
            </Badge>
          ))}
        </button>
      </div>
      <span
        className="min-w-0 truncate text-caption text-foreground-secondary @max-[600px]/list:col-start-2 @max-[600px]/list:row-start-1"
        title={`${sourceKindLabel(skill.source.type)} · ${sourceLabel(s, skill.source)}`}
      >
        {sourceLabel(s, skill.source)}
      </span>
      <div className="flex min-w-0 @max-[600px]/list:col-start-2 @max-[600px]/list:row-start-2">
        <Where skill={skill} />
      </div>
    </article>
  );
}

// useFlash 是作用域页、项目页里刚入库的那一行：滚到视口中间(不被吸顶的节标题盖住)闪一下。
// 同一个技能在页上有几行(项目页的几个 Agent 都装了)时，闪入库的那个作用域的那一行;没有那一行(来自全局一节里的)时闪第一行。
// 技能库页上(没有 scope)刚从来源入库的几行(view 的 fresh)一起闪，只滚到第一行。
function useFlash(id: SkillID, scope: ScopeKey | undefined) {
  const ref = useRef<HTMLElement>(null);
  const target = useView((v) => (scope && v.flash?.skill === id ? v.flash : null));
  const fresh = useView((v) => (!scope && v.fresh?.ids.has(id) ? v.fresh : null));
  useEffect(() => {
    const el = ref.current;
    if (!fresh || !el) return;
    const first = [...document.querySelectorAll<HTMLElement>(".skill-row:not([data-scope])")].find((r) => fresh.ids.has(Number(r.dataset.skillId)));
    return flash(el, first === el ? "center" : null, first === el ? () => doneFresh(fresh.n) : undefined);
  }, [id, fresh]);
  useEffect(() => {
    const el = ref.current;
    if (!target || !el) return;
    const rows = `.skill-row[data-skill-id="${id}"]`;
    const first = document.querySelector(`${rows}[data-scope="${CSS.escape(target.scope)}"]`) ?? document.querySelector(rows);
    if (first !== el) return;
    return flash(el, "center", () => doneFlash(target.n));
  }, [id, target]);
  return ref;
}

// Where 是技能库页的"装在哪":常见的情况只有两个字，例外才多写;有地方要重新安装时字用主要档(原因在名字后面的提醒图标里),
// 装着的用次要档，不抢技能名的注意力。
function Where({ skill }: { skill: Skill }) {
  const t = useT().list;
  const w = installedSummary(useSnapshot(), skill.id);
  return (
    <span className="skill-where flex min-w-0 items-center text-caption" title={w.title || undefined}>
      <span className={cn("truncate", w.bad ? "text-foreground" : w.text ? "text-foreground-secondary" : "text-muted-foreground")}>
        {w.text || t.notInstalled}
      </span>
    </span>
  );
}

// RowState 是一行的最后一列：有提醒或更新时写最要紧的那一条(图标 + 一句话，还有别的写 +N,鼠标停住逐条列出;
// 有新版本、可能改名时点它打开更新计划);没有时写 plain(随组安装、来自全局这类要说明的情况),都没有时空着。
function RowState({ alerts, plain, className }: { alerts: Alert[]; plain?: { text: string; title?: string }; className?: string }) {
  if (alerts.length) return <AlertIcon labeled items={alerts} className={className} />;
  if (!plain) return null;
  return (
    <span className={cn("min-w-0 truncate text-caption text-muted-foreground", className)} title={plain.title ?? plain.text}>
      {plain.text}
    </span>
  );
}

// scopePlain 是作用域页一行没有提醒时最后一列写的：直接安装是常态，不写;随组安装写随哪几个组，来自全局写来自全局。
function scopePlain(t: Messages["list"], place: SkillScopeInspection | undefined, via: string[], fromGlobal: boolean) {
  if (fromGlobal) return { text: t.fromGlobal, title: t.sectionGlobalTitle };
  if (!place || !via.length) return undefined;
  return { text: t.hereState(place.direct, via) };
}

// TakeOverButton 是和库里技能一模一样、不是库装的那一处的"入库":入库只补记安装，文件夹原样不动。
// 提交在项目仓库里、指向 Agent 目录以外的默认不入库，按钮是普通的灰底。
function TakeOverButton({ identical, name }: { identical: UnmanagedSkill; name: string }) {
  const t = useT().list;
  return (
    <Button
      size="sm"
      variant={notTakenOver(identical) ? "pill" : "pillQuiet"}
      className="take-over"
      title={t.identicalHereTitle(name)}
      onClick={() => void fetchUnmanaged(identical)}
    >
      {t.takeOver}
    </Button>
  );
}

// IdenticalState 是那一处的最后一列：不是 app 装的，接着写归谁管、是不是指向 Agent 目录以外(folderNotes);
// 和库里的旧版本一样时标出来，下面是在文件管理器中显示。
// foreign(提交在项目仓库里、链到 Agent 目录以外)的那一份不跟库里的版本走，和旧版本一样也不提醒。
function IdenticalState({ identical, skill, alerts, foreign }: { identical: UnmanagedSkill; skill: Skill; alerts: Alert[]; foreign: boolean }) {
  const { common, list } = useT();
  const stale = skillScopeStatus(SkillScopeStatus.SkillScopeStale);
  const state = identicalState(skill, identical, useSnapshot().info.home_dir);
  const title = identical.link_target ? `${state.title}\n${list.linkTitle(identical.link_target)}` : state.title;
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      {/* 先写这一份不是 app 装的、归谁管;有提醒时后面只跟一个图标。 */}
      <span className="skill-state identical-state min-w-0 truncate text-caption text-muted-foreground" title={title}>
        {state.text}
      </span>
      <AlertIcon items={[...(state.stale && !foreign ? [{ tone: stale.tone as Mark, label: stale.label }] : []), ...alerts]} />
      <RevealButton
        label={common.reveal()}
        onClick={(e) => {
          e.stopPropagation();
          void ui.read(() => AppService.RevealInFileManager(identical.folder));
        }}
      />
    </div>
  );
}

// RevealButton 是行尾的「在文件管理器中显示」:行高固定，只画文件夹图标，文字在悬停提示里(也留在 DOM 里给读屏)。
function RevealButton({ label, onClick }: { label: string; onClick: (e: React.MouseEvent) => void }) {
  return (
    <Button variant="ghost" size="icon-sm" className="reveal-folder shrink-0" title={label} onClick={onClick}>
      <Icon name="folder" />
      <span className="sr-only">{label}</span>
    </Button>
  );
}

// UnmanagedRow 是作用域里一个不是库装的技能文件夹(未纳管技能):它不在技能库里，所以行首没有开关，是"入库"按钮;
// 入库后这一行换成库里技能的一行，出现在它所属的那一节并闪一下。和库里某个技能一样的(一般显示成那个技能的一行，见 SkillRow;
// 这里只剩"来自全局"一节里的)行首是"入库";是库里某个技能在来源里的新版的，行首是"更新",打开那个技能的更新计划。
// 最后一列写"不在技能库里"(headed:作用域页"不在技能库里"一节里的，节标题已经写了，不再写;fromGlobal:"来自全局"一节里的，先写"来自全局"),
// 提交在项目仓库里、记在 skills-lock.json 里、指向 Agent 目录以外的接着写出来，下面是在文件管理器中显示;符号链接在描述下写出指向哪里。
// 不能拖动、不打开详情。
// copies 是同一层的别处同名、内容不同的那几处的地名(见 same-name 的 useSameNameCopies):不合成一个技能，行上说出来。
export function UnmanagedRow({
  skill,
  fromGlobal = false,
  headed = false,
  copies,
}: {
  skill: UnmanagedSkill;
  fromGlobal?: boolean;
  headed?: boolean;
  copies?: string[];
}) {
  const s = useSnapshot();
  const { list: t, common } = useT();
  const same = sameNameNote(s, scopeKey(skill.scope), skill.folder);
  // 来源记录认不出来源的，在 skills.sh 上找到来源后补上。
  const found = foundSource(useFoundSources(), skill);
  const source = skill.source_input || found;
  // 和库里某个技能一样的(就是那个技能)、库里某个技能的新版(在 app 外更新过)各有自己的说法和操作。
  const library = s.skills.find((x) => x.id === skill.skill_id);
  const newer = s.skills.find((x) => x.id === skill.source_latest_of);
  // SKILL.md 写错了的不能入库：行首不给「入库」,最后一列写不能入库(悬停看原因)。
  const invalid = skill.invalid ? skillFileProblemText(skill.invalid) : "";
  const state = [
    fromGlobal ? { text: t.fromGlobal, title: t.sectionGlobalTitle } : undefined,
    invalid ? { text: t.cannotFetchShort, title: t.cannotFetchTitle(invalid) } : undefined,
    library
      ? { text: t.sameAsLibrary(library.name), title: t.identicalHereTitle(library.name) }
      : newer
        ? { text: t.sourceLatestOf(newer.name), title: t.sourceLatestOfTitle(newer.name) }
        : headed
          ? undefined
          : { text: t.notInLibrary, title: t.notInLibraryTitle },
    ...folderNotes(skill, s.info.home_dir),
  ].filter((x) => !!x);
  // 行高固定，来源和符号链接指向哪里不占一行：写在描述的悬停提示里，同一份留在 DOM 里给读屏(className 是测试钩子)。
  // 没有描述时描述那一格直接写来源和链接(来源是怎么认出来的那句 detail 只在提示里),不留一块悬停不到的空白。
  const notes = [
    // SKILL.md 写错了的没有描述：描述那一格写哪里写错了(这一格最宽，原因写得下)。
    invalid && { text: t.cannotFetch(invalid), className: "unmanaged-invalid" },
    source && { text: t.from(source), className: "unmanaged-source-label" },
    source && { text: found ? t.foundTitle(found) : t.recordedTitle(source), className: "unmanaged-source-how", detail: true },
    skill.link_target && { text: t.link(shortPath(s, skill.link_target)), className: "unmanaged-status" },
  ].filter((x): x is { text: string; className: string; detail?: boolean } => !!x);
  return (
    <article className={cn("unmanaged-row", rowClass, layouts.scope.cols)} data-name={skill.name}>
      <span className={span} />
      <span className={cn("flex", span)}>
        {library ? (
          <TakeOverButton identical={skill} name={library.name} />
        ) : newer ? (
          <Button
            size="sm"
            variant="pill"
            className="update-to-latest"
            title={t.sourceLatestOfTitle(newer.name)}
            onClick={() => void openUpdate(newer.collection_id, { focus: newer.id })}
          >
            {t.update}
          </Button>
        ) : invalid ? (
          // 行首这一格窄：只画「注意」的图标;原因写在描述那一格，「不能入库」写在最后一列。
          <span className="cannot-fetch flex w-full justify-center" title={t.cannotFetchTitle(invalid)}>
            <MarkIcon tone="warn" />
          </span>
        ) : (
          // 提交在项目仓库里、指向 Agent 目录以外的默认不入库(归团队管、作者留在自己目录里改):按钮是普通的灰底，不推用户去入库。
          <Button
            size="sm"
            variant={notTakenOver(skill) ? "pill" : "pillQuiet"}
            className="fetch-unmanaged"
            title={t.fetchTitle}
            onClick={() => void fetchUnmanaged(skill)}
          >
            {t.fetch}
          </Button>
        )}
      </span>
      <div className="flex min-w-0 items-center gap-1.5">
        <span className="unmanaged-name min-w-0 truncate text-body font-semibold">{skill.name}</span>
      </div>
      <div className={cn("min-w-0", layouts.scope.desc)}>
        <Truncated always={notes.length > 0} tip={[skill.description, ...notes.map((x) => x.text)].filter(Boolean).join("\n")}>
          <p className="unmanaged-desc truncate text-small text-muted-foreground">
            {skill.description || notes.filter((x) => !x.detail).map((x) => x.text).join(" · ")}
          </p>
        </Truncated>
        {notes.map((x) => (
          <p key={x.className} className={cn(x.className, "sr-only")}>
            {x.text}
          </p>
        ))}
      </div>
      <div className={cn("flex min-w-0 items-center gap-1.5", span)}>
        {state.length > 0 && (
          <span className="skill-state unmanaged-state min-w-0 truncate text-caption text-muted-foreground" title={state.map((x) => x.title).join("\n")}>
            {state.map((x) => x.text).join(" · ")}
          </span>
        )}
        {/* 同名的提醒(和同一个 Agent 另一层级同名、同一层的别处同名不同内容)只画一个图标;前面没有字时图标后面写最要紧的那一条。 */}
        <AlertIcon
          labeled={!state.length}
          items={[
            same && sameNameAlert(same, t.sameName),
            sameNameCopiesAlert(t, skill.name, copies),
            // 库里有同名的技能，而这里和它的哪个版本都不一样(一样的会认成那个技能):入库后会多出一个同名的。
            !library && !newer && sameNameInLibrary(s, { id: 0, name: skill.name }).length > 0 && {
              tone: "info",
              label: t.twinsShort,
              tip: t.libraryTwinTitle(skill.name),
              className: "library-twin",
            },
          ]}
        />
        <RevealButton label={common.reveal()} onClick={() => void ui.read(() => AppService.RevealInFileManager(skill.folder))} />
      </div>
    </article>
  );
}
