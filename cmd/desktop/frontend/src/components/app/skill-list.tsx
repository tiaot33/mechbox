// 中间：作用域摘要(选中作用域时)、组 / 集合标题条(筛选组 / 集合时)、更新列表(筛选"更新"时)与技能卡片 / 列表。
// 卡片点开详情抽屉、可拖到左栏组或右栏作用域;选中作用域时卡片上的开关控制直接安装。
// 选中作用域时，库里的技能之后接着列出这个作用域里的未纳管技能(虚线卡片，只能在 Finder 中显示)。
// 和同一个 Agent 另一层级(全局 / 项目)同名的技能，卡片上标"同名"并写出后果。
import { useEffect, useRef, useState } from "react";
import type { CancellablePromise } from "@wailsio/runtime";
import { AppService, LibraryService } from "@bindings/cmd/desktop";
import { PlannedChange, type Collection, type CollectionID, type Group, type Skill, type UnmanagedSkill } from "@bindings/internal/library";
import {
  deleteGroup,
  fetchUnmanaged,
  installGroup,
  installSkill,
  previewCollection,
  reinstall,
  renameGroup,
  uninstallGroup,
  uninstallSkill,
} from "@/lib/actions";
import { useDnD } from "@/lib/dnd";
import { messages, useT } from "@/lib/i18n";
import { foundSource, useFoundSources, type Snapshot } from "@/lib/library";
import {
  agentName,
  collectionSkills,
  deletedInSource,
  groupStateIn,
  hasUpdate,
  installedWhere,
  isLocal,
  needsReinstall,
  parseScope,
  pending,
  placeOf,
  projectName,
  renamedTo,
  revisionSummary,
  revisionText,
  sameNameInLibrary,
  sameNameNote,
  scopeKey,
  scopeLabel,
  shortPath,
  shortRevision,
  skillScopeIn,
  skillScopeStatus,
  skillScopesIn,
  skillScopesOf,
  sourceKindLabel,
  sourceLabel,
  sourceShort,
  updatableCollections,
  unmanagedIn,
  updateAvailable,
  viaGroupCount,
  type ScopeKey,
  type SkillFilter,
} from "@/lib/model";
import { useUI } from "@/lib/ui";
import { offerFetchUnmanaged } from "./fetch-unmanaged-dialog";
import { notInterested, openHistory, openUpdate, previewNewSkills, riskSummary, updateWithDefaults } from "@/lib/update";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Icon, SourceIcon } from "./icons";
import { centerClass } from "./layout";
import { AgentMark, Chip } from "./parts";
import type { ListView } from "./toolbar";

interface SkillListProps {
  s: Snapshot;
  skills: Skill[];
  unmanaged: UnmanagedSkill[];
  filter: SkillFilter;
  scope: ScopeKey | null;
  scopeDirectory: string | undefined;
  view: ListView;
  selected: number | null;
  onOpen: (id: number) => void;
  onFetch: () => void;
}

// 列表顶部的摘要条(作用域摘要、更新列表、集合标题条)共用的外观。
const barClass = "a-scope-sum mx-3.5 mt-3 flex flex-col gap-2 rounded-[10px] border border-hairline bg-glass-card px-3 py-2.5 shadow-tile";
const barRowClass = "a-sum-row flex min-w-0 items-center gap-2.5";

export function SkillList(props: SkillListProps) {
  const { s, skills, unmanaged, filter, scope, view } = props;
  const group = filter.kind === "group" ? s.groups.find((g) => g.id === filter.groupID) : undefined;
  const collection = filter.kind === "collection" ? s.collections.find((c) => c.id === filter.collectionID) : undefined;
  // 列出的技能都来自同一个来源时，卡片上不再逐张写来源。
  const showSource = new Set(skills.map((x) => x.collection_id)).size > 1;
  const t = useT().list;
  return (
    <main className={centerClass}>
      {scope && <ScopeSummary s={s} scope={scope} directory={props.scopeDirectory} />}
      {group && <GroupHead s={s} group={group} scope={scope} />}
      {collection && <CollectionHead s={s} collection={collection} />}
      {filter.kind === "updates" && updatableCollections(s).length > 0 && <UpdatesHead s={s} />}
      {/* 网格放在单独的滚动容器里：网格高度随内容,auto-rows-fr 才等于最高那一行，而不是平分可视高度 */}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div
          className={cn(
            "a-list grid content-start",
            view === "card"
              ? "auto-rows-fr grid-cols-[repeat(auto-fill,minmax(236px,1fr))] gap-2.5 px-3.5 pt-3 pb-15"
              : "grid-cols-1 px-2.5 pt-1.5 pb-3.5",
          )}
          data-view={view}
        >
          {skills.map((skill) => (
            <SkillCard
              key={skill.id}
              s={s}
              skill={skill}
              scope={scope}
              view={view}
              showSource={showSource}
              selected={props.selected === skill.id}
              hideGroup={group?.id}
              onOpen={() => props.onOpen(skill.id)}
            />
          ))}
          {unmanaged.map((u) => (
            <UnmanagedCard key={u.folder} s={s} skill={u} view={view} />
          ))}
          {skills.length === 0 &&
            unmanaged.length === 0 &&
            (s.skills.length === 0 ? (
              <ListEmpty>
                <b className="text-sm text-foreground">{t.emptyLibrary}</b>
                <span>{t.emptyHint}</span>
                <Button variant="primary" className="mt-1.5" onClick={props.onFetch}>
                  <Icon name="plus" />
                  {t.fetchSkills}
                </Button>
              </ListEmpty>
            ) : (
              <ListEmpty>{t.noMatch}</ListEmpty>
            ))}
        </div>
      </div>
    </main>
  );
}

// 空状态放在列表区的视觉中部;列表是 content-start 的网格,h-full 不生效，用上边距定位。
function ListEmpty({ children }: { children: React.ReactNode }) {
  return <div className="col-span-full flex flex-col items-center justify-center gap-1.5 pt-[22vh] text-muted-foreground">{children}</div>;
}

function SkillCard(props: {
  s: Snapshot;
  skill: Skill;
  scope: ScopeKey | null;
  view: ListView;
  showSource: boolean;
  selected: boolean;
  hideGroup?: number; // 正在筛选的组：每张卡都属于它，不再重复显示
  onOpen: () => void;
}) {
  const { s, skill, scope, view } = props;
  const ui = useUI();
  const dnd = useDnD();
  const t = useT();
  const installed = skillScopesOf(s, skill.id);
  const globals = installed.filter((p) => placeOf(p.scope) === 0).map((p) => p.scope.agent);
  const projects = new Set(installed.filter((p) => placeOf(p.scope) !== 0).map((p) => placeOf(p.scope))).size;
  const bad = installed.some(needsReinstall);
  const here = scope ? skillScopeIn(s, skill.id, scope) : undefined;
  const same = scope && here ? sameNameNote(s, scope, here.folder) : undefined;
  const groups = s.groups.filter((g) => g.members?.includes(skill.id) && g.id !== props.hideGroup);
  const via = (here?.groups ?? []).map((id) => s.groups.find((g) => g.id === id)?.name ?? "");
  const twins = sameNameInLibrary(s, skill);
  const list = view === "list";

  const toggle = () => {
    if (!scope) return;
    const sc = parseScope(scope);
    void (here?.direct ? uninstallSkill(ui, s, skill.id, sc) : installSkill(ui, s, skill.id, sc));
  };

  return (
    <article
      className={cn(
        "a-card group/card flex cursor-grab gap-2 border transition-[background-color,border-color,box-shadow,transform] duration-100 active:cursor-grabbing",
        list
          ? "min-h-10 flex-row items-center gap-5 rounded-[7px] border-transparent px-3 shadow-[inset_0_-1px_0_rgba(17,24,39,0.06)] hover:border-hairline hover:bg-white hover:shadow-[0_1px_2px_rgba(17,24,39,0.04)]"
          : "min-h-28 flex-col rounded-[10px] border-hairline bg-glass-card px-3 pt-3 pb-2.5 shadow-tile hover:-translate-y-px hover:border-border-strong hover:bg-white hover:shadow-raised",
        // 选中时不走过渡：点开抽屉的同时遮罩盖住卡片、悬停态消失，过渡会让边框闪一下。
        props.selected &&
          "translate-y-0 border-primary bg-white shadow-[0_0_0_1px_var(--color-primary)] transition-none hover:translate-y-0 hover:border-primary hover:shadow-[0_0_0_1px_var(--color-primary)]",
      )}
      data-selected={props.selected || undefined}
      onClick={props.onOpen}
      draggable
      onDragStart={(e) => dnd.start(e, { kind: "skill", id: skill.id }, skill.name)}
      onDragEnd={dnd.end}
    >
      <div className={cn("flex min-w-0 items-center gap-1.5", list && "w-[260px] shrink-0")}>
        {/* 名字优先：放不下时先截右边的来源，再截名字。 */}
        <span className={cn("a-card-name min-w-0 truncate font-semibold", list ? "text-[13px]" : "text-[12.5px]")}>{skill.name}</span>
        {same && <SameNameBadge note={same} />}
        {/* 计入角标的才标出来;点标签直接打开所在集合的更新计划(组页面不提供批量更新，就从这里进)。 */}
        {pending(skill) && hasUpdate(skill) && (
          <Badge asChild tone="warn" className="px-[5px] py-0 text-[9.5px]">
            <button
              type="button"
              title={renamedTo(skill) ? t.list.renamedTitle(renamedTo(skill)) : t.list.availableTitle}
              onClick={(e) => {
                e.stopPropagation();
                void openUpdate(ui, s, skill.collection_id, { focus: skill.id });
              }}
            >
              {renamedTo(skill) ? t.list.maybeRenamed : t.list.newVersion}
            </button>
          </Badge>
        )}
        {deletedInSource(skill) &&
          (skill.kept_deleted ? (
            <span className="shrink-0 text-[10px] text-subtle" title={t.list.keptDeletedTitle}>
              {t.list.keptDeleted}
            </span>
          ) : (
            <Badge title={t.list.deletedTitle}>{t.list.deleted}</Badge>
          ))}
        {/* 右上角：所属组(灰色小标签，多个时显示 +N),其后是淡色的来源。 */}
        {groups.length > 0 && (
          <span
            className={cn(
              "max-w-24 shrink-0 truncate rounded bg-black/5 px-[5px] py-px font-mono text-[9.5px] font-medium text-muted-foreground",
              props.selected && "bg-[#dbe4ee] text-slate-800",
            )}
            title={t.list.groupsTitle(groups.map((g) => g.name))}
          >
            {groups[0].name}
            {groups.length > 1 && ` +${groups.length - 1}`}
          </span>
        )}
        {props.showSource && (
          <span
            className="ml-auto max-w-20 min-w-0 shrink-[100] truncate text-[10px] text-subtle"
            title={`${sourceKindLabel(skill.source.type)} · ${sourceLabel(s, skill.source)}`}
          >
            {sourceShort(s, skill.source)}
          </span>
        )}
      </div>
      <p
        className={cn(
          list
            ? "min-w-0 flex-1 truncate text-[11.5px] text-foreground-secondary"
            : "line-clamp-2 min-h-[2.9em] text-[11px] leading-[1.45] text-muted-foreground",
        )}
      >
        {skill.description}
      </p>
      {same && <SameNameLine note={same} list={list} />}
      {/* 有新版本、但更新会换掉用户的修改：卡片上直接写出来，点"有新版本"打开的更新计划里它默认不勾。 */}
      {pending(skill) && updateAvailable(skill) && skill.edited && (
        <p
          className={cn("update-edited-note min-w-0 text-[10.5px] leading-[1.4] text-warn-strong", list ? "w-[160px] shrink-0 truncate" : "-mt-1")}
          title={t.list.editedTitle}
        >
          {t.list.edited}
        </p>
      )}
      {/* 库里同名的几个技能(内容不同，不合并)写出各装在哪里，免得只能靠描述分辨。 */}
      {twins.length > 0 && (
        <p className={cn("dup-name-note min-w-0 text-[10.5px] leading-[1.4] text-muted-foreground", list ? "w-[220px] shrink-0 truncate" : "-mt-1")} title={t.list.twins(twins.length + 1, skill.name, installedWhere(s, skill.id))}>
          {t.list.twins(twins.length + 1, skill.name, installedWhere(s, skill.id))}
        </p>
      )}
      <div
        className={cn(
          "a-card-foot flex min-h-[17px] items-center gap-[5px] font-mono text-[10px] text-muted-foreground",
          list ? "w-[220px] shrink-0" : "mt-auto",
        )}
      >
        {scope ? (
          <>
            <Switch
              checked={!!here?.direct}
              held={!!here && !here.direct}
              onClick={(e) => e.stopPropagation()}
              onCheckedChange={toggle}
              title={here?.direct ? t.list.uninstallDirect : t.list.installDirect}
            />
            <span className="min-w-0 truncate">{here ? t.list.hereState(here.direct, via) : t.list.notInstalled}</span>
            {here && needsReinstall(here) && (
              <Badge tone={skillScopeStatus(here.status).tone} className="font-sans">
                {skillScopeStatus(here.status).label}
              </Badge>
            )}
          </>
        ) : (
          <>
            <span className={cn("size-1.5 shrink-0 rounded-full bg-slate-300", installed.length && (bad ? "bg-warn" : "bg-ok"))} />
            {installed.length === 0 && <span>{t.list.notInstalled}</span>}
            {globals.length > 0 && (
              <>
                <span>{t.model.global}</span>
                <span className="inline-flex items-center gap-[3px]" title={t.list.agents(globals.map((code) => agentName(s, code)))}>
                  {globals.slice(0, 4).map((code) => (
                    <AgentMark key={code} code={code} name={agentName(s, code)} className="size-3 text-[8px]" />
                  ))}
                  {globals.length > 4 && <span>+{globals.length - 4}</span>}
                </span>
              </>
            )}
            {projects > 0 && (
              <>
                <span className="grow" />
                <span>{t.list.projects(projects)}</span>
              </>
            )}
          </>
        )}
      </div>
    </article>
  );
}

// UnmanagedCard 是作用域里一个不是库装的技能文件夹(未纳管技能):虚线边框、标"未纳管",
// 提交在项目仓库里的再标"在项目仓库里"，符号链接写出指向哪里。不能拖动、不打开详情，只提供在 Finder 中显示与入库。
function UnmanagedCard({ s, skill, view }: { s: Snapshot; skill: UnmanagedSkill; view: ListView }) {
  const ui = useUI();
  const t = useT().list;
  const list = view === "list";
  const same = sameNameNote(s, scopeKey(skill.scope), skill.folder);
  // 来源记录认不出来源的，在 skills.sh 上找到来源后补上。
  const found = foundSource(useFoundSources(), skill);
  const source = skill.source_input || found;
  return (
    <article
      className={cn(
        // 不挂 a-card:e2e 与拖放按 a-card 找库里的技能。
        "unmanaged-card flex gap-2 border border-dashed",
        list
          ? "min-h-10 flex-row items-center gap-5 rounded-[7px] border-transparent px-3 shadow-[inset_0_-1px_0_rgba(17,24,39,0.06)] hover:border-border-strong"
          : "min-h-28 flex-col rounded-[10px] border-border-strong bg-white/30 px-3 pt-3 pb-2.5",
      )}
      data-name={skill.name}
    >
      {/* 卡片上标签多时折到下一行，名字不被挤掉。 */}
      <div className={cn("flex min-w-0 items-center gap-1.5", list ? "w-[260px] shrink-0" : "flex-wrap gap-y-1")}>
        <span className={cn("unmanaged-name min-w-0 truncate font-semibold text-foreground-secondary", list ? "text-[13px]" : "text-[12.5px]")}>
          {skill.name}
        </span>
        <Badge title={t.unmanagedTitle}>{t.unmanaged}</Badge>
        {skill.in_repository && <Badge title={t.inRepositoryTitle}>{t.inRepository}</Badge>}
        {!skill.in_repository && skill.in_project_lock && <Badge title={t.inLockTitle}>{t.inLock}</Badge>}
        {skill.link_outside && <Badge title={t.linkOutsideTitle(skill.link_target ?? "")}>{t.linkOutside}</Badge>}
        {same && <SameNameBadge note={same} />}
      </div>
      <p
        className={cn(
          list
            ? "min-w-0 flex-1 truncate text-[11.5px] text-muted-foreground"
            : "line-clamp-2 min-h-[2.9em] text-[11px] leading-[1.45] text-muted-foreground",
        )}
      >
        {skill.description}
      </p>
      {same && <SameNameLine note={same} list={list} />}
      {/* 卡片上来源单独一行、长的折行，和底部的按钮抢不到位置;列表视图只有一行，放不下时截断、悬停看全。 */}
      {source && (
        <p
          className={cn(
            "unmanaged-source-label min-w-0 text-[10.5px] leading-[1.4] text-muted-foreground",
            list ? "w-[220px] shrink-0 truncate" : "-mt-1 break-all",
          )}
          title={found ? t.foundTitle(found) : t.recordedTitle(source)}
        >
          {t.from(source)}
        </p>
      )}
      <div
        className={cn(
          "a-card-foot flex min-h-[17px] items-center gap-[5px] font-mono text-[10px] text-muted-foreground",
          list ? "w-[220px] shrink-0" : "mt-auto",
        )}
      >
        <span
          className={cn("unmanaged-status min-w-0 grow", skill.link_target ? "truncate" : "whitespace-nowrap")}
          title={skill.link_target ? t.linkTitle(skill.link_target) : skill.folder}
        >
          {skill.link_target ? t.link(shortPath(s, skill.link_target)) : t.notByLibrary}
        </span>
        <Button
          variant="link"
          className="reveal-in-finder shrink-0 font-sans text-[10.5px]"
          onClick={() => void ui.run(() => AppService.RevealInFinder(skill.folder), { reload: false })}
        >
          {t.revealInFinder}
        </Button>
        <Button
          variant="link"
          className="fetch-unmanaged shrink-0 font-sans text-[10.5px]"
          title={t.fetchTitle}
          onClick={() => void fetchUnmanaged(ui, s, skill)}
        >
          {t.fetch}
        </Button>
      </div>
    </article>
  );
}

// SameNameBadge 与 SameNameLine 标出作用域里和同一个 Agent 另一层级同名的技能：标签之外直接写出后果，
// 用户排查"这个项目里用的是不是我装的那个"时不用再点开。
function SameNameBadge({ note }: { note: { title: string } }) {
  const t = useT().list;
  return (
    <Badge tone="warn" className="same-name-badge" title={note.title}>
      {t.sameName}
    </Badge>
  );
}

function SameNameLine({ note, list }: { note: { text: string; title: string }; list: boolean }) {
  return (
    <p
      className={cn("same-name-note min-w-0 text-[10.5px] leading-[1.4] text-warn-strong", list ? "w-[300px] shrink-0 truncate" : "-mt-1")}
      title={note.title}
    >
      {note.text}
    </p>
  );
}

// ScopeSummary 是选中作用域时列表顶部的摘要:Agent 目录、健康状态、全部重新安装，以及每个组的整组开关。
function ScopeSummary({ s, scope, directory }: { s: Snapshot; scope: ScopeKey; directory: string | undefined }) {
  const ui = useUI();
  const t = useT().list;
  const installed = skillScopesIn(s, scope);
  const issues = installed.filter(needsReinstall).length;
  const unmanaged = unmanagedIn(s, scope).length;
  // 在项目仓库里的、项目 skills-lock.json 记的归项目仓库管，指向 Agent 目录以外的是作者有意留在自己目录里的，都不算进"还没交给 app 管"的数里。
  const notTaken = unmanagedIn(s, scope).filter((u) => !u.in_repository && !u.in_project_lock && !u.link_outside).length;
  const sc = parseScope(scope);
  return (
    <div className={barClass}>
      <div className={barRowClass}>
        <span className="text-[11px] text-muted-foreground">{t.agentDirectory}</span>
        <span className="max-w-[360px] truncate font-mono text-[11px] select-text">{directory ? shortPath(s, directory) : "…"}</span>
        <span className="grow" />
        <Stat value={installed.length} label={t.statInstalled} />
        {unmanaged > 0 && <Stat value={unmanaged} label={t.statUnmanaged} />}
        <Stat value={issues} label={t.statStale} warn={issues > 0} />
        {issues > 0 && (
          <Button size="sm" onClick={() => reinstall(ui, s, { scope: sc }, scopeLabel(s, sc))}>
            <Icon name="refresh" />
            {t.reinstallAll}
          </Button>
        )}
      </div>
      {notTaken > 0 && (
        <div className="unmanaged-hint flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <span>{t.notTaken(notTaken)}</span>
          <Button
            variant="link"
            className="fetch-all-unmanaged text-[11px]"
            onClick={() => void offerFetchUnmanaged(ui, s.agents, [sc], { kind: "scope", label: scopeLabel(s, sc) }, false)}
          >
            {t.fetchAll}
          </Button>
        </div>
      )}
      {s.groups.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-[11px] text-muted-foreground">{t.groups}</span>
          {s.groups.map((g) => {
            const state = groupStateIn(s, g, scope);
            const on = state === "on";
            return (
              <button
                type="button"
                key={g.id}
                data-state={state}
                aria-pressed={on}
                className={cn(
                  "gchip inline-flex h-6 items-center gap-1.5 rounded-md border border-border px-[9px] text-[11.5px] text-foreground-secondary transition-colors hover:border-border-strong hover:bg-white/70",
                  on && "border-group-border bg-group-soft font-medium text-[#5b21b6] hover:border-group hover:bg-group-soft",
                )}
                onClick={() => (on ? uninstallGroup(ui, s, g.id, sc) : installGroup(ui, s, g.id, sc))}
                title={t.groupChipTitle(g.name, on)}
              >
                <i className={cn("size-[7px] rounded-full border-[1.5px] border-subtle", on && "border-group bg-group")} />
                {g.name}
                <span className={cn("font-mono text-[11px] text-subtle", on && "text-[#7c3aed]")}>
                  {viaGroupCount(s, g, scope)}/{g.members?.length ?? 0}
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

function Stat({ value, label, warn = false }: { value: number; label: string; warn?: boolean }) {
  return (
    <span className="stat text-xs text-foreground-secondary" data-tone={warn ? "warn" : undefined}>
      <b className={cn("mr-0.5 text-sm", warn && "text-amber-600")}>{value}</b> {label}
    </span>
  );
}

// GroupHead 是筛选某个组时列表顶部的组标题条：整组的拖动把手，附带重命名、删除与组安装到当前作用域。
function GroupHead({ s, group, scope }: { s: Snapshot; group: Group; scope: ScopeKey | null }) {
  const ui = useUI();
  const dnd = useDnD();
  const t = useT();
  const scopes = s.groupInstalls.filter((a) => a.group_id === group.id).map((a) => a.scope);
  const count = group.members?.length ?? 0;
  return (
    <div
      className="a-group-head mx-3.5 mt-3 flex min-w-0 cursor-grab items-center gap-2 rounded-[10px] border border-group-border bg-group-soft px-3 py-2.5 text-[#4c1d95] hover:border-group hover:shadow-[0_2px_8px_rgba(139,92,246,0.15)] active:cursor-grabbing"
      draggable
      onDragStart={(e) => dnd.start(e, { kind: "group", id: group.id }, t.list.groupDrag(group.name, count))}
      onDragEnd={dnd.end}
      title={t.list.groupDragTitle(count)}
    >
      <span className="mr-0.5 grid grid-cols-[repeat(2,3px)] gap-0.5">
        {Array.from({ length: 6 }, (_, i) => (
          <i key={i} className="size-[3px] rounded-full bg-[#a78bfa]" />
        ))}
      </span>
      <Icon name="layers" className="text-group" />
      <b>{group.name}</b>
      <span className="text-[11px] text-muted-foreground">{t.list.skillCount(count)}</span>
      <span className="ml-1.5 inline-flex min-w-0 items-center gap-1 overflow-hidden text-[11.5px] whitespace-nowrap text-foreground-secondary">
        {scopes.length ? (
          <>
            {t.list.groupInstalledIn}
            {scopes.map((sc) => (
              <Chip key={scopeKey(sc)} small removeTitle={t.list.uninstallGroupHere} onRemove={() => uninstallGroup(ui, s, group.id, sc)}>
                <AgentMark code={sc.agent} name={agentName(s, sc.agent)} />
                {sc.project_id ? projectName(s, sc.project_id) : t.model.global}
              </Chip>
            ))}
          </>
        ) : (
          <span className="text-[11px] text-muted-foreground">{t.list.notGroupInstalled}</span>
        )}
      </span>
      <span className="ml-auto flex items-center gap-1.5">
        {scope && !scopes.some((sc) => scopeKey(sc) === scope) && (
          <Button size="sm" onClick={() => installGroup(ui, s, group.id, parseScope(scope))}>
            {t.list.groupInstallTo(scopeLabel(s, parseScope(scope)))}
          </Button>
        )}
        <Button size="sm" onClick={() => renameGroup(ui, s, group.id)}>
          {t.list.rename}
        </Button>
        <Button size="sm" variant="danger" onClick={() => deleteGroup(ui, s, group.id)}>
          {t.list.deleteGroup}
        </Button>
      </span>
    </div>
  );
}

// UpdatesHead 是筛选"更新"时的更新列表：每个有要处理的更新的集合一行——来源版本从哪到哪、哪些技能有变化、风险提示。
// 信得过的直接"更新"(按默认勾选),不放心的"查看"打开更新计划。集合里只有一个技能时直接显示技能名。
// 更新会换掉用户修改的技能(Skill.edited)在更新计划里默认不勾：这一行写明;都是这种时没有能直接更新的，"更新…"只打开更新计划。
function UpdatesHead({ s }: { s: Snapshot }) {
  const ui = useUI();
  const t = useT().list;
  const collections = updatableCollections(s);
  const risks = usePlanRisks(s, collections);
  const direct = collections.filter((c) => collectionSkills(s, c).some((x) => pending(x) && updateAvailable(x) && !x.edited));
  return (
    <div className={barClass}>
      {collections.map((c) => {
        const skills = collectionSkills(s, c);
        const single = skills.length === 1;
        const todo = skills.filter(pending);
        const available = todo.filter(updateAvailable);
        const plain = available.filter((x) => !x.edited);
        const edited = available.filter((x) => x.edited);
        const to = todo.map((x) => x.last_update_check?.revision).find(Boolean);
        const risk = risks.get(c.id);
        const names = (list: Skill[]) => list.map((x) => x.name);
        const facts = [
          single ? t.from(sourceLabel(s, c.source)) : "",
          revisionMove(skills, to),
          plain.length ? (single ? t.newVersion : t.changedSkills(names(plain))) : "",
          edited.length ? (single ? t.newVersionEdited : t.changedSkillsEdited(names(edited))) : "",
          ...todo.filter(renamedTo).map((x) => t.renamedSkill(x.name, renamedTo(x))),
          todo.some(deletedInSource) ? t.deletedSkills(names(todo.filter(deletedInSource))) : "",
        ].filter(Boolean);
        return (
          <div key={c.id} className={cn(barRowClass, "update-row")}>
            <b className="truncate">{single ? skills[0].name : sourceLabel(s, c.source)}</b>
            <span className="min-w-0 grow truncate text-[11px] text-muted-foreground" title={facts.join(" · ")}>
              {facts.join(" · ")}
            </span>
            {risk && (
              <span className="shrink-0 text-[11px] text-warn-strong" title={t.riskTitle}>
                ⚠ {risk}
              </span>
            )}
            {plain.length === 0 && edited.length > 0 ? (
              <Button size="sm" onClick={() => openUpdate(ui, s, c.id)} title={t.updateEditedTitle}>
                <Icon name="up" />
                {t.updateEdited}
              </Button>
            ) : (
              <Button size="sm" onClick={() => openUpdate(ui, s, c.id)}>
                {t.view}
              </Button>
            )}
            {plain.length > 0 && (
              <Button size="sm" variant="primary" disabled={ui.busy} onClick={() => updateWithDefaults(ui, s, [c.id])}>
                <Icon name="up" />
                {t.update}
              </Button>
            )}
          </div>
        );
      })}
      <div className={barRowClass}>
        <span className="grow text-[11px] text-muted-foreground">{t.updatesFooter}</span>
        {direct.length > 1 && (
          <Button size="sm" variant="primary" disabled={ui.busy} onClick={() => updateWithDefaults(ui, s, direct.map((c) => c.id))}>
            <Icon name="up" />
            {t.updateAll}
          </Button>
        )}
      </div>
    </div>
  );
}

// revisionMove 是"a1b2c3 → d4e5f6";集合里的技能不在同一个来源版本上时如实写出各在哪里。
function revisionMove(skills: Skill[], to: string | undefined): string {
  const from = revisionSummary(skills);
  if (!from || !to) return revisionText(from);
  const t = messages().list;
  return from.kind === "same" ? t.revisionMove(from.revision, shortRevision(to)) : t.revisionLatest(revisionText(from), shortRevision(to));
}

// 更新列表上的风险提示要读一次更新计划(要连来源);按"集合 + 来源最新版本 + 库里各技能的当前版本"记下来，
// 同一份差异只读一次。读失败的不记，下次再读;离开更新列表时取消还在读的。
// 生成更新计划会写下这次读到的检查结论：来源在检查更新之后又变了时，计划和快照对不上，重读快照让更新列表、计数跟上，
// 风险只按计划实际读到的来源版本记。重读后快照仍停在旧的键上时不再反复读来源，快照换了键才重新算。
const riskCache = new Map<string, string>();

const riskKey = (id: CollectionID, revision: string | undefined, versions: number[]) =>
  `${id}:${revision ?? ""}:${[...versions].sort((a, b) => a - b).join(",")}`;

function usePlanRisks(s: Snapshot, collections: Collection[]): Map<CollectionID, string> {
  const ui = useUI();
  const keys = collections.map((c) => {
    const skills = collectionSkills(s, c);
    const to = skills.map((x) => x.last_update_check?.revision).find(Boolean);
    return [c.id, riskKey(c.id, to, skills.map((x) => x.version_id))] as const;
  });
  const [, setVersion] = useState(0);
  const calls = useRef(new Map<string, CancellablePromise<unknown>>());
  const settled = useRef(new Set<string>());
  const wanted = keys.map(([, key]) => key).join("|");

  useEffect(() => {
    const running = calls.current;
    return () => {
      for (const call of running.values()) void call.cancel();
      running.clear();
    };
  }, []);

  useEffect(() => {
    const current = new Set(keys.map(([, key]) => key));
    for (const key of settled.current) if (!current.has(key)) settled.current.delete(key);
    for (const [id, key] of keys) {
      if (riskCache.has(key) || calls.current.has(key) || settled.current.has(key)) continue;
      const call = LibraryService.PlanUpdate(id);
      calls.current.set(key, call);
      call
        .then(
          (plan) => {
            const skills = plan.skills ?? [];
            const movable = skills.filter((p) => p.change === PlannedChange.PlannedChanged || p.renamed_to);
            const risk = riskSummary(movable.flatMap((p) => p.risks ?? []));
            const actual = riskKey(id, plan.revision, skills.map((p) => p.skill.version_id));
            riskCache.set(actual, risk);
            if (actual === key) return;
            settled.current.add(key);
            ui.reload();
          },
          () => {},
        )
        .finally(() => {
          if (calls.current.get(key) === call) calls.current.delete(key);
          setVersion((n) => n + 1);
        });
    }
  }, [wanted]);

  return new Map(keys.map(([id, key]) => [id, riskCache.get(key) ?? ""]));
}

// CollectionHead 是筛选某个集合时的标题条：来源、集合里的技能在哪个来源版本上、更新、历史，以及来源里的新技能。
function CollectionHead({ s, collection }: { s: Snapshot; collection: Collection }) {
  const ui = useUI();
  const t = useT();
  const skills = collectionSkills(s, collection);
  const todo = skills.filter(pending).length;
  // "有变化"只算来源里有新内容的技能(跳过了的也算);来源里已删除、推测出改名的不算。
  const changed = skills.filter(updateAvailable).length;
  const local = isLocal(collection.source);
  const label = sourceLabel(s, collection.source);
  const facts = [revisionText(revisionSummary(skills)), changed ? t.drawer.changedCount(changed) : ""].filter(Boolean);
  const fresh = collection.new_skills ?? [];
  return (
    <div className={barClass}>
      <div className={barRowClass}>
        <span className="inline-flex items-center gap-[5px]">
          <SourceIcon type={collection.source.type} className="size-3 text-muted-foreground" />
          {label}
        </span>
        <span className="grow truncate text-[11px] text-muted-foreground">{facts.join(" · ")}</span>
        <Button size="sm" onClick={() => previewCollection(ui, collection.id)} title={t.list.otherSkillsHint}>
          {t.drawer.otherSkills}
        </Button>
        <Button size="sm" onClick={() => openHistory(ui, s, { collection_id: collection.id }, t.list.historyTitle(label))}>
          {t.list.history}
        </Button>
        {!local && skills.some(hasUpdate) && (
          <Button size="sm" variant={todo ? "primary" : "default"} onClick={() => openUpdate(ui, s, collection.id)}>
            <Icon name="up" />
            {t.list.update}
          </Button>
        )}
      </div>
      {fresh.length > 0 && (
        <div className={cn(barRowClass, "collection-new")}>
          <span className="grow truncate text-[11px] text-muted-foreground">
            {t.drawer.newSkills(label, fresh)}
          </span>
          <Button variant="link" onClick={() => previewNewSkills(ui, collection.id)}>
            {t.drawer.goFetch}
          </Button>
          <Button variant="link" onClick={() => notInterested(ui, collection.id, fresh)}>
            {t.drawer.notInterested}
          </Button>
        </div>
      )}
    </div>
  );
}
