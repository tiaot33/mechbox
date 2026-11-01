// 列表的行：库里的技能(技能库页、更新页与作用域页)与作用域里的未纳管技能。一个技能一行，列对齐，
// 平常的信息用淡色，只有要处理的(有新版本、来源里已删除、同名、要重新安装)才上色。
// 作用域页与项目页的行尾是开关，只表示 Agent 在这里用不用：开 = 在用，关 = 没在用;开关前面一列只写例外(随组安装、来自全局、要重新安装),直接安装是常态，不写。
// 未纳管技能不在技能库里，行尾没有开关，是"入库"按钮;在"不在技能库里"一节里时状态列不再写这句。
// 库里技能的行点开详情抽屉，可以拖到右栏作用域(直接安装)或左栏、节标题上的组(加入该组);
// 技能库页、作用域页与项目页的行开头有勾选框，勾选的几个技能用底部的操作栏一起操作，拖勾选着的一行就是拖它们全部。
import { useEffect, useRef } from "react";
import { AppService } from "@bindings/cmd/desktop";
import type { Skill, SkillID, SkillScopeInspection, UnmanagedSkill } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { foundSource, useFoundSources, useSnapshot } from "@/lib/library";
import {
  changedAt,
  deletedInSource,
  globalScopesSeenFrom,
  groupName,
  hasUpdate,
  installedSummary,
  installedWhere,
  needsReinstall,
  parseScope,
  pending,
  renamedTo,
  sameNameInLibrary,
  sameNameNote,
  scopeKey,
  skillSameNameNote,
  shortPath,
  skillScopeIn,
  skillScopeStatus,
  sourceKindLabel,
  sourceInputLabel,
  sourceLabel,
  sourceShort,
  translatedDescription,
  updateAvailable,
  type ScopeKey,
} from "@/lib/model";
import { ui } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { doneFlash, toggleDrawer, useSortedGroups, useView } from "@/lib/view";
import { fetchUnmanaged } from "@/features/fetch/fetch-unmanaged";
import { installSkill, uninstallSkill } from "@/features/install/install";
import { openUpdate } from "@/features/update/update";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch, SwitchMark } from "@/components/ui/switch";
import { useDnD } from "@/components/app/dnd";
import { Icon } from "@/components/app/icons";
import { Truncated } from "@/components/app/parts";
import { rowClass } from "./list-layout";
import { PickBox } from "./pick";
import { SameNameBadge } from "./same-name";
import { flash } from "./flash";

// 列与对齐见 list-layout.tsx 的 rowClass:开头一格各页都有(不能勾选的行空着),名字和节标题的名字对齐。
// 文字只有三级：名字 13px,描述与各列 12px,标签与描述下的说明 11px。
// 作用域页最后一列：库里技能是开关，未纳管技能是和摘要里"全部入库…"一样的蓝色文字链接"入库",都在这一列居中。
const noteClass = "truncate text-[11px]";

// 最近几天内的变化用正文颜色，排查"最近是不是变过"时一眼看到。
const recentDays = 7;

// SkillRow 是库里一个技能的一行。scope 为空时是技能库页 / 更新页的样子;有 scope 时是作用域页、项目页的样子:
// 开头一格表示 Agent 在这里用不用(见下),最后一列写是谁装的;fromGlobal 表示它是同一个 Agent 全局装的(项目里也看得到),这里不能开关。
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
  // 最后一列说的那一处：来自全局的是全局那一处，否则是这里。
  const place =
    scope && props.fromGlobal ? globalScopesSeenFrom(s, scope).map((key) => skillScopeIn(s, skill.id, key)).find(Boolean) : here;
  const via = (place?.groups ?? []).map((id) => groupName(s, id));
  // 作用域页说这一处的同名后果;技能库页、更新页说它装出去的各处。
  const same = scope ? (here ? sameNameNote(s, scope, here.folder) : undefined) : skillSameNameNote(s, skill.id);
  const groups = useSortedGroups(s).filter((g) => g.members?.includes(skill.id) && g.id !== props.hideGroup);
  const twins = sameNameInLibrary(s, skill);
  const translated = translatedDescription(skill);
  const ref = useFlash(skill.id, scope);

  const toggle = () => {
    if (!scope) return;
    const sc = parseScope(scope);
    // 只随组安装的：开关不改安装，说明随哪个组装、怎样在这里不用它。
    if (here && !here.direct) return ui.toast(t.list.groupOnly(skill.name, via));
    void (here ? uninstallSkill(skill.id, sc) : installSkill(skill.id, sc));
  };

  return (
    <article
      ref={ref}
      className={cn(
        "skill-row cursor-grab transition-colors hover:bg-white/80 active:cursor-grabbing data-[flash]:bg-primary-soft",
        rowClass(scope ? "scope" : "library"),
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
        if (picked && all.size > 1) dnd.start(e, { kind: "skills", ids: [...all] }, t.list.pickedDrag(all.size));
        else dnd.start(e, { kind: "skill", id: skill.id }, skill.name);
      }}
      onDragEnd={dnd.end}
    >
      {props.pickable ? <PickBox id={skill.id} /> : <span />}
      <div className="flex min-w-0 items-center gap-1.5">
        {/* 名字优先：放不下时先截后面的标记。 */}
        <span
          className={cn("skill-name min-w-0 truncate text-[13px] font-medium", scope && !here && !props.fromGlobal && "font-normal text-foreground-secondary")}
          title={skill.note || undefined}
        >
          {skill.name}
        </span>
        {same && <SameNameBadge note={same} />}
        {/* 当前版本里有用户的修改(用目录里的修改更新过，或入库时说不清):不论有没有新版本都标出来。 */}
        {skill.edited && (
          <Badge className="has-edits" title={t.list.hasEditsTitle}>
            {t.list.hasEdits}
          </Badge>
        )}
        {/* 计入角标的才标出来;点标签直接打开所在集合的更新计划。 */}
        {pending(skill) && hasUpdate(skill) && (
          <Badge asChild tone="warn">
            <button
              type="button"
              title={renamedTo(skill) ? t.list.renamedTitle(renamedTo(skill)) : t.list.availableTitle}
              onClick={(e) => {
                e.stopPropagation();
                void openUpdate(skill.collection_id, { focus: skill.id });
              }}
            >
              {renamedTo(skill) ? t.list.maybeRenamed : t.list.newVersion}
            </button>
          </Badge>
        )}
        {deletedInSource(skill) &&
          (skill.kept_deleted ? (
            <Badge title={t.list.keptDeletedTitle}>{t.list.keptDeleted}</Badge>
          ) : (
            <Badge title={t.list.deletedTitle}>{t.list.deleted}</Badge>
          ))}
        {groups.length > 0 && (
          <Badge tone="group" className="max-w-24 shrink-[100] truncate" title={t.list.groupsTitle(groups.map((g) => g.name))}>
            {groups[0].name}
            {groups.length > 1 && ` +${groups.length - 1}`}
          </Badge>
        )}
      </div>
      <div className="min-w-0">
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
          <p className="skill-desc truncate text-[12px] text-muted-foreground">
            {props.showSource && (
              <span className="text-subtle" title={`${sourceKindLabel(skill.source.type)} · ${sourceLabel(s, skill.source)}`}>
                {sourceShort(s, skill.source)} ·{" "}
              </span>
            )}
            {translated ?? skill.description}
          </p>
        </Truncated>
        {/* 有新版本、但更新会换掉用户的修改：直接写出来，更新计划里它默认不勾。 */}
        {pending(skill) && updateAvailable(skill) && skill.edited && (
          <p className={cn("update-edited-note text-warn-strong", noteClass)} title={t.list.editedTitle}>
            {t.list.edited}
          </p>
        )}
        {/* 库里同名的几个技能(内容不同，不合并)写出各装在哪里，免得只能靠描述分辨。 */}
        {twins.length > 0 && (
          <p className={cn("dup-name-note text-muted-foreground", noteClass)} title={t.list.twins(twins.length + 1, skill.name, installedWhere(s, skill.id))}>
            {t.list.twins(twins.length + 1, skill.name, installedWhere(s, skill.id))}
          </p>
        )}
      </div>
      {scope ? <ScopeState place={place} via={via} fromGlobal={!!props.fromGlobal} /> : <Where skill={skill} />}
      {!scope && <Changed skill={skill} />}
      {scope && (
        <span onClick={(e) => e.stopPropagation()} className="flex justify-self-center">
          {/* 开 = Agent 在这里用着：直接安装是蓝色，只随组安装是紫色，来自全局是灰色、不能点;关 = 没装在这里。 */}
          {props.fromGlobal ? (
            <SwitchMark title={t.list.sectionGlobalTitle} />
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

// Where 是技能库页的"装在哪":常见的情况只有两个字，例外才多写;有地方要重新安装时标黄。
function Where({ skill }: { skill: Skill }) {
  const s = useSnapshot();
  const t = useT().list;
  const w = installedSummary(s, skill.id);
  return (
    <span className="skill-where flex min-w-0 items-center gap-1.5 text-[12px]" title={w.title || undefined}>
      {w.bad && <i className="size-1.5 shrink-0 rounded-full bg-warn" />}
      <span className={cn("truncate", w.text ? "text-foreground-secondary" : "text-subtle", w.bad && "text-warn-strong")}>
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
    <span className={cn("skill-changed truncate text-right text-[12px]", recent ? "text-foreground-secondary" : "text-subtle")} title={c.date.toLocaleString()}>
      {t.changed(c.kind, c.date)}
    </span>
  );
}

// ScopeState 是作用域页开关前的一列，只写例外：随组安装、来自全局，要重新安装时带上原因。
// 直接安装是常态、没装看开关就知道，都不写。place 是这一处(来自全局的是全局那一处)的安装，via 是随哪几个组装。
function ScopeState({ place, via, fromGlobal }: { place: SkillScopeInspection | undefined; via: string[]; fromGlobal: boolean }) {
  const t = useT().list;
  const text = fromGlobal ? t.fromGlobal : place && via.length ? t.hereState(place.direct, via) : "";
  return (
    <span className="skill-state flex min-w-0 items-center gap-1.5 text-[12px] text-muted-foreground">
      {text && (
        <span className="min-w-0 truncate" title={text}>
          {text}
        </span>
      )}
      {place && needsReinstall(place) && (
        <Badge tone={skillScopeStatus(place.status).tone} className="font-sans">
          {skillScopeStatus(place.status).label}
        </Badge>
      )}
    </span>
  );
}

// UnmanagedRow 是作用域里一个不是库装的技能文件夹(未纳管技能):它不在技能库里，所以行尾没有开关，是"入库"按钮;
// 入库后这一行换成库里技能的一行，出现在它所属的那一节并闪一下。
// 和库里的技能分开：开头一格是入库图标(headed 的节标题已经有，不再放)。状态列只占一行：
// "不在技能库里"(headed:作用域页"不在技能库里"一节里的，节标题已经写了，不再写;fromGlobal:"来自全局"一节里的，先写"来自全局"),
// 提交在项目仓库里、记在 skills-lock.json 里、指向 Agent 目录以外的接着写;鼠标移上去时这一格换成在 Finder 中显示。
// 行和库里的技能一样只占一行：来源记录写在描述前面(和集合的节标题一样去掉 https:// 与 .git,完整的在悬停提示里)、尽量写全，描述接在后面放不下就截断;符号链接是名字后面的链接图标，鼠标停住看指向哪里。
// 不能拖动、不打开详情。
export function UnmanagedRow({ skill, fromGlobal = false, headed = false }: { skill: UnmanagedSkill; fromGlobal?: boolean; headed?: boolean }) {
  const s = useSnapshot();
  const t = useT().list;
  const same = sameNameNote(s, scopeKey(skill.scope), skill.folder);
  // 来源记录认不出来源的，在 skills.sh 上找到来源后补上。
  const found = foundSource(useFoundSources(), skill);
  const source = skill.source_input || found;
  const state = [
    fromGlobal ? { text: t.fromGlobal, title: t.sectionGlobalTitle } : undefined,
    headed ? undefined : { text: t.notInLibrary, title: t.notInLibraryTitle },
    skill.in_repository
      ? { text: t.inRepository, title: t.inRepositoryTitle }
      : skill.in_project_lock
        ? { text: t.inLock, title: t.inLockTitle }
        : undefined,
    skill.link_outside ? { text: t.linkOutside, title: t.linkOutsideTitle(skill.link_target ?? "") } : undefined,
  ].filter((x) => !!x);
  return (
    <article className={cn("unmanaged-row", rowClass("scope"))} data-name={skill.name}>
      {headed ? <span /> : <Icon name="inbox" className="text-subtle" />}
      <div className="flex min-w-0 items-center gap-1.5">
        <span className="unmanaged-name min-w-0 truncate text-[13px] text-foreground-secondary">{skill.name}</span>
        {skill.link_target && (
          <span className="unmanaged-status flex shrink-0 text-subtle" title={t.linkTitle(skill.link_target)}>
            <Icon name="link" className="size-3" />
            <span className="sr-only">{t.link(shortPath(s, skill.link_target))}</span>
          </span>
        )}
        {same && <SameNameBadge note={same} />}
      </div>
      <div className="min-w-0">
        <p className="flex min-w-0 items-baseline gap-1.5 text-[12px]">
          {source && (
            <>
              <span
                className="unmanaged-source-label max-w-full shrink-0 truncate text-foreground-secondary"
                title={found ? t.foundTitle(found) : t.recordedTitle(source)}
              >
                {t.from(sourceInputLabel(s.info.home_dir, source))}
              </span>
              <span className="text-subtle">·</span>
            </>
          )}
          <Truncated tip={skill.description}>
            <span className="unmanaged-desc min-w-0 truncate text-muted-foreground">{skill.description}</span>
          </Truncated>
        </p>
      </div>
      <div className="grid min-w-0 items-center *:[grid-area:1/1]">
        {state.length > 0 && (
          <span
            className="skill-state unmanaged-state min-w-0 truncate text-[12px] text-muted-foreground group-hover/row:invisible group-has-focus-visible/row:invisible"
            title={state.map((x) => x.title).join("\n")}
          >
            {state.map((x) => x.text).join(" · ")}
          </span>
        )}
        <Button
          variant="link"
          className="reveal-in-finder justify-self-start font-sans text-[12px] opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100"
          onClick={() => void ui.run(() => AppService.RevealInFinder(skill.folder), { reload: false })}
        >
          {t.revealInFinder}
        </Button>
      </div>
      <Button
        variant="link"
        className="fetch-unmanaged justify-self-center text-[12px]"
        title={t.fetchTitle}
        onClick={() => void fetchUnmanaged(skill)}
      >
        {t.fetch}
      </Button>
    </article>
  );
}
