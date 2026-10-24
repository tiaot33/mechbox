// 中间列表里库中技能的卡片(列表视图时是一行):装在哪些地方、所属的组、更新与来源的标记;可以拖到右栏作用域或左栏的组。
import type { Skill } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import {
  agentName,
  deletedInSource,
  hasUpdate,
  installedWhere,
  needsReinstall,
  parseScope,
  pending,
  placeOf,
  renamedTo,
  sameNameInLibrary,
  sameNameNote,
  skillScopeIn,
  skillScopeStatus,
  skillScopesOf,
  sourceKindLabel,
  sourceLabel,
  sourceShort,
  updateAvailable,
  type ScopeKey,
} from "@/lib/model";
import { cn } from "@/lib/utils";
import type { ListView } from "@/lib/view";
import { installSkill, uninstallSkill } from "@/features/install/install";
import { openUpdate } from "@/features/update/update";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { useDnD } from "@/components/app/dnd";
import { AgentMark } from "@/components/app/parts";
import { SameNameBadge, SameNameLine } from "./same-name";

export function SkillCard(props: {
  skill: Skill;
  scope: ScopeKey | null;
  view: ListView;
  showSource: boolean;
  selected: boolean;
  hideGroup?: number; // 正在筛选的组：每张卡都属于它，不再重复显示
  onOpen: () => void;
}) {
  const s = useSnapshot();
  const { skill, scope, view } = props;
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
    void (here?.direct ? uninstallSkill(skill.id, sc) : installSkill(skill.id, sc));
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
                void openUpdate(skill.collection_id, { focus: skill.id });
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
          "a-card-foot flex min-h-[17px] items-center gap-[5px] text-[10px] text-muted-foreground",
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
            {/* 英文放不下时截断，悬停看全。 */}
            <span className="min-w-0 truncate" title={here ? t.list.hereState(here.direct, via) : undefined}>
              {here ? t.list.hereState(here.direct, via) : t.list.notInstalled}
            </span>
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
                {/* 也装在全局时项目数靠右;只装在项目里时紧跟圆点，不留一个孤零零的点。 */}
                {globals.length > 0 && <span className="grow" />}
                <span>{t.list.projects(projects)}</span>
              </>
            )}
          </>
        )}
      </div>
    </article>
  );
}
