// 列表的行：库里的技能(技能库页、更新页与作用域页)与作用域里的未纳管技能。一个技能一行，列对齐，
// 平常的信息用淡色，只有要处理的(有新版本、来源里已删除、同名、要重新安装)才上色。
// 作用域页与项目页的行首一格只表示 Agent 在这里用不用：开 = 在用，关 = 没在用;最后一列写是谁装的。
// 未纳管技能不在技能库里，行首没有开关，是"入库"按钮;在"不在技能库里"一节里时最后一列不再写这句。
// 库里技能的行点开详情抽屉，可以拖到右栏作用域(直接安装)或左栏、节标题上的组(加入该组);
// 技能库页、作用域页与项目页的行开头有勾选框，勾选的几个技能用底部的操作栏一起操作，拖勾选着的一行就是拖它们全部。
import { useEffect, useRef } from "react";
import { AppService } from "@bindings/cmd/desktop";
import { SkillScopeStatus, type Skill, type SkillID, type SkillScopeInspection, type UnmanagedSkill } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { foundSource, useFoundSources, useSnapshot } from "@/lib/library";
import {
  changedAt,
  identicalIn,
  deletedInSource,
  folderNotes,
  identicalState,
  globalScopesSeenFrom,
  groupName,
  hasUpdate,
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
import { doneFlash, toggleDrawer, useView } from "@/lib/view";
import { fetchUnmanaged } from "@/features/fetch/fetch-unmanaged";
import { installSkill, uninstallSkill } from "@/features/install/install";
import { openUpdate } from "@/features/update/update";
import { AlertDot, Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch, SwitchMark } from "@/components/ui/switch";
import { useDnD } from "@/components/app/dnd";
import { Icon } from "@/components/ui/icon";
import { Truncated } from "@/components/app/parts";
import { PickBox } from "./pick";
import { SameNameBadge, sameNameAlert } from "./same-name";
import { flash } from "./flash";

// 列宽：技能库页是 名字 | 描述 | 装在哪 | 最近变化;作用域页是 开关 | 名字 | 描述 | 在这里的状态。
// 能勾选的行(更新页以外)前面多一列勾选框。
// 列表窄了(开着技能详情)时每行两层：描述挪到名字下面;技能库页的装在哪和日期上下叠在最后一列(where、date),
// 作用域页的状态跨两层。名字、装在哪(在这里的状态)和日期仍然看得全;列表的宽度按 .a-list 这个容器算。
// 各列宽下 desc、where、date 是窄的时候它们放的格子，勾选框和开关跨两层、上下居中(span)。
const layouts = {
  library: {
    cols: "grid-cols-[minmax(140px,220px)_minmax(0,1fr)_minmax(80px,160px)_92px] @max-[600px]/list:grid-cols-[minmax(0,1fr)_minmax(110px,170px)]",
    desc: "@max-[600px]/list:col-start-1 @max-[600px]/list:row-start-2",
    where: "@max-[600px]/list:col-start-2 @max-[600px]/list:row-start-1",
    date: "@max-[600px]/list:col-start-2 @max-[600px]/list:row-start-2 @max-[600px]/list:justify-start",
  },
  pickLibrary: {
    cols: "grid-cols-[14px_minmax(140px,220px)_minmax(0,1fr)_minmax(80px,160px)_92px] @max-[600px]/list:grid-cols-[14px_minmax(0,1fr)_minmax(110px,170px)]",
    desc: "@max-[600px]/list:col-start-2 @max-[600px]/list:row-start-2",
    where: "@max-[600px]/list:col-start-3 @max-[600px]/list:row-start-1",
    date: "@max-[600px]/list:col-start-3 @max-[600px]/list:row-start-2 @max-[600px]/list:justify-start",
  },
  scope: {
    cols: "grid-cols-[30px_minmax(140px,220px)_minmax(0,1fr)_minmax(90px,180px)] @max-[600px]/list:grid-cols-[30px_minmax(0,1fr)_minmax(80px,140px)]",
    desc: "@max-[600px]/list:col-start-2 @max-[600px]/list:row-start-2",
    where: "@max-[600px]/list:row-span-2",
    date: "",
  },
  pickScope: {
    cols: "grid-cols-[14px_30px_minmax(140px,220px)_minmax(0,1fr)_minmax(90px,180px)] @max-[600px]/list:grid-cols-[14px_30px_minmax(0,1fr)_minmax(80px,140px)]",
    desc: "@max-[600px]/list:col-start-3 @max-[600px]/list:row-start-2",
    where: "@max-[600px]/list:row-span-2",
    date: "",
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

// 最近几天内的变化用次要档，更早的用标注档;日期本身写成「今天」「3 天前」,近的一眼能认出来。
const recentDays = 7;

// SkillRow 是库里一个技能的一行。scope 为空时是技能库页 / 更新页的样子;有 scope 时是作用域页、项目页的样子:
// 开头一格表示 Agent 在这里用不用(见下),最后一列写是谁装的;fromGlobal 表示它是同一个 Agent 全局装的(项目里也看得到),这里不能开关。
// 这里有一份和它一模一样、不是库装的文件夹时(identicalIn),开头一格是"入库",最后一列写不是 app 装的。
// pickable 的行最前面有勾选框(技能库页、作用域页与项目页;更新页不能勾选)。
export function SkillRow(props: {
  skill: Skill;
  scope?: ScopeKey;
  fromGlobal?: boolean;
  pickable?: boolean;
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
  // 作用域页说这一处的同名后果;技能库页、更新页说它装出去的各处。
  const folder = here?.folder ?? identical?.folder;
  const same = scope ? (folder ? sameNameNote(s, scope, folder) : undefined) : skillSameNameNote(s, skill.id);
  const groups = s.groups.filter((g) => g.members?.includes(skill.id) && g.id !== props.hideGroup);
  const twins = sameNameInLibrary(s, skill);
  // 技能库页上，要重新安装的那几处合进名字后面的图标;作用域页的行在最后一列写这一处的状态，不再合进来。
  const places = scope ? [] : reinstallAlerts(s, skill.id);
  const translated = translatedDescription(skill);
  const ref = useFlash(skill.id, scope);

  const toggle = () => {
    if (!scope) return;
    const sc = parseScope(scope);
    // 只随组安装的：开关不改安装，说明随哪个组装、怎样在这里不用它。
    if (here && !here.direct) return ui.toast(t.list.groupOnly(skill.name, via));
    void (here ? uninstallSkill(skill.id, sc) : installSkill(skill.id, sc));
  };

  const layout = layouts[scope ? (props.pickable ? "pickScope" : "scope") : props.pickable ? "pickLibrary" : "library"];

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
      {props.pickable && <PickBox id={skill.id} className={span} />}
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
        {/* 一行：名字放不下时截断，后面的提醒只画图标(不被截掉),再后面是组名。 */}
        <span
          className={cn("skill-name min-w-0 truncate text-body font-semibold", scope && !here && !identical && !props.fromGlobal && "font-normal text-foreground-secondary")}
          title={skill.note || undefined}
        >
          {skill.name}
        </span>
        {/* 这一行的提醒(技能本身的，加上要重新安装的那几处)合成一个图标，按最重的一级，鼠标停住时逐条列出。 */}
        <AlertDot
          items={[
            same && sameNameAlert(same, t.list.sameName),
            ...places.filter((x) => x.tone !== "update"),
            // 作者从来源里删掉了它：不是新版本，是一条提示，和这一行的其他提醒合在一起。
            deletedInSource(skill) && !skill.kept_deleted && { tone: "info", label: t.list.deleted, tip: t.list.deletedTitle },
            // 当前版本里有用户的修改(用目录里的修改更新过，或入库时说不清):不论有没有新版本都标出来。
            skill.edited && { tone: "info", label: t.list.hasEdits, tip: t.list.hasEditsTitle, className: "has-edits" },
            // 有新版本、但更新会换掉用户的修改：更新计划里它默认不勾。
            pending(skill) && updateAvailable(skill) && skill.edited && { tone: "warn", label: t.list.edited, tip: t.list.editedTitle, className: "update-edited-note" },
            // 库里同名的几个技能(内容不同，不合并):写出各装在哪里，免得只能靠描述分辨。
            twins.length > 0 && { tone: "info", label: t.list.twins(twins.length + 1, skill.name, installedWhere(s, skill.id)), className: "dup-name-note" },
          ]}
        />
        {/* 更新不是提醒：另画一个主题绿的向上箭头，点它打开所在集合的更新计划;只标计入角标的。 */}
        <AlertDot
          items={[
            pending(skill) &&
              hasUpdate(skill) && {
                tone: "update",
                label: renamedTo(skill) ? t.list.maybeRenamed : t.list.newVersion,
                tip: renamedTo(skill) ? t.list.renamedTitle(renamedTo(skill)) : t.list.availableTitle,
                onClick: () => void openUpdate(skill.collection_id, { focus: skill.id }),
              },
            ...places.filter((x) => x.tone === "update"),
          ]}
        />
        {deletedInSource(skill) && skill.kept_deleted && (
          <span className="shrink-0 text-caption text-muted-foreground" title={t.list.keptDeletedTitle}>
            {t.list.keptDeleted}
          </span>
        )}
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
            {props.showSource && (
              <span className="text-muted-foreground" title={`${sourceKindLabel(skill.source.type)} · ${sourceLabel(s, skill.source)}`}>
                {sourceShort(s, skill.source)} ·{" "}
              </span>
            )}
            {translated ?? skill.description}
          </p>
        </Truncated>
      </div>
      <div className={cn("flex min-w-0", layout.where)}>
        {scope ? (
          identical ? (
            <IdenticalState identical={identical} skill={skill} />
          ) : (
            <ScopeState place={place} via={via} fromGlobal={!!props.fromGlobal} />
          )
        ) : (
          <Where skill={skill} />
        )}
      </div>
      {!scope && (
        <div className={cn("flex min-w-0 justify-end", layout.date)}>
          <Changed skill={skill} />
        </div>
      )}
    </article>
  );
}

// useFlash 是作用域页、项目页里刚入库的那一行：滚到视口中间(不被吸顶的节标题盖住)闪一下。
// 同一个技能在页上有几行(项目页的几个 Agent 都装了)时，闪入库的那个作用域的那一行;没有那一行(来自全局一节里的)时闪第一行。
function useFlash(id: SkillID, scope: ScopeKey | undefined) {
  const ref = useRef<HTMLElement>(null);
  const target = useView((v) => (scope && v.flash?.skill === id ? v.flash : null));
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

function Changed({ skill }: { skill: Skill }) {
  const t = useT().model;
  const c = changedAt(skill);
  if (!c) return <span />;
  const recent = (Date.now() - c.date.getTime()) / 864e5 < recentDays;
  return (
    <span className={cn("skill-changed truncate text-right text-caption tabular-nums", recent ? "text-foreground-secondary" : "text-muted-foreground")} title={c.date.toLocaleString()}>
      {t.changed(c.kind, c.date)}
    </span>
  );
}

// ScopeState 是作用域页最后一列：直接安装 / 随组安装 / 来自全局 / 未安装，要重新安装时带上原因。
// place 是这一处(来自全局的是全局那一处)的安装，via 是随哪几个组装。
function ScopeState({ place, via, fromGlobal }: { place: SkillScopeInspection | undefined; via: string[]; fromGlobal: boolean }) {
  const t = useT().list;
  const text = fromGlobal ? t.fromGlobal : place ? t.hereState(place.direct, via) : t.notInstalled;
  return (
    <span className="skill-state flex min-w-0 items-center gap-1.5 text-caption text-muted-foreground">
      <span className="min-w-0 truncate" title={text}>
        {text}
      </span>
      {place && needsReinstall(place) && (
        <Badge tone={skillScopeStatus(place.status, place.source_latest).tone} className="font-sans">
          {skillScopeStatus(place.status, place.source_latest).label}
        </Badge>
      )}
    </span>
  );
}

// TakeOverButton 是和库里技能一模一样、不是库装的那一处的"入库":入库只补记安装，文件夹原样不动。
function TakeOverButton({ identical, name }: { identical: UnmanagedSkill; name: string }) {
  const t = useT().list;
  return (
    <Button
      size="sm"
      variant="pill"
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
function IdenticalState({ identical, skill }: { identical: UnmanagedSkill; skill: Skill }) {
  const { common, list } = useT();
  const stale = skillScopeStatus(SkillScopeStatus.SkillScopeStale);
  const state = identicalState(skill, identical);
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <span
        className="skill-state identical-state flex min-w-0 items-center gap-1.5 text-caption text-muted-foreground"
        title={identical.link_target ? `${state.title}\n${list.linkTitle(identical.link_target)}` : state.title}
      >
        <span className="min-w-0 truncate">{state.text}</span>
        {state.stale && (
          <Badge tone={stale.tone} className="font-sans">
            {stale.label}
          </Badge>
        )}
      </span>
      <RevealButton
        label={common.reveal()}
        onClick={(e) => {
          e.stopPropagation();
          void ui.run(() => AppService.RevealInFileManager(identical.folder), { reload: false });
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
export function UnmanagedRow({ skill, fromGlobal = false, headed = false }: { skill: UnmanagedSkill; fromGlobal?: boolean; headed?: boolean }) {
  const s = useSnapshot();
  const { list: t, common } = useT();
  const same = sameNameNote(s, scopeKey(skill.scope), skill.folder);
  // 来源记录认不出来源的，在 skills.sh 上找到来源后补上。
  const found = foundSource(useFoundSources(), skill);
  const source = skill.source_input || found;
  // 和库里某个技能一样的(就是那个技能)、库里某个技能的新版(在 app 外更新过)各有自己的说法和操作。
  const library = s.skills.find((x) => x.id === skill.skill_id);
  const newer = s.skills.find((x) => x.id === skill.source_latest_of);
  const state = [
    fromGlobal ? { text: t.fromGlobal, title: t.sectionGlobalTitle } : undefined,
    library
      ? { text: t.sameAsLibrary(library.name), title: t.identicalHereTitle(library.name) }
      : newer
        ? { text: t.sourceLatestOf(newer.name), title: t.sourceLatestOfTitle(newer.name) }
        : headed
          ? undefined
          : { text: t.notInLibrary, title: t.notInLibraryTitle },
    ...folderNotes(skill),
  ].filter((x) => !!x);
  // 行高固定，来源和符号链接指向哪里不占一行：写在描述的悬停提示里，同一份留在 DOM 里给读屏(className 是测试钩子)。
  // 没有描述时描述那一格直接写来源和链接(来源是怎么认出来的那句 detail 只在提示里),不留一块悬停不到的空白。
  const notes = [
    source && { text: t.from(source), className: "unmanaged-source-label" },
    source && { text: found ? t.foundTitle(found) : t.recordedTitle(source), className: "unmanaged-source-how", detail: true },
    skill.link_target && { text: t.link(shortPath(s, skill.link_target)), className: "unmanaged-status" },
  ].filter((x): x is { text: string; className: string; detail?: boolean } => !!x);
  return (
    <article className={cn("unmanaged-row", rowClass, layouts.pickScope.cols)} data-name={skill.name}>
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
        ) : (
          <Button
            size="sm"
            variant="pill"
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
        {same && <SameNameBadge note={same} />}
      </div>
      <div className={cn("min-w-0", layouts.pickScope.desc)}>
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
        <RevealButton label={common.reveal()} onClick={() => void ui.run(() => AppService.RevealInFileManager(skill.folder), { reload: false })} />
      </div>
    </article>
  );
}
