// 列表的行：库里的技能(所有技能页、更新页与作用域页)与作用域里的未纳管技能。一个技能一行，列对齐，
// 平常的信息用淡色，只有要处理的(有新版本、来源里已删除、同名、要重新安装)才上色。
// 库里技能的行点开详情抽屉，可以拖到右栏作用域(直接安装)或左栏、节标题上的组(加入该组)。
import { AppService } from "@bindings/cmd/desktop";
import type { Skill, UnmanagedSkill } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { foundSource, useFoundSources, useSnapshot } from "@/lib/library";
import {
  changedAt,
  deletedInSource,
  globalScopesSeenFrom,
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
  sourceLabel,
  sourceShort,
  updateAvailable,
  type ScopeKey,
} from "@/lib/model";
import { useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { toggleDrawer, useView } from "@/lib/view";
import { fetchUnmanaged } from "@/features/fetch/fetch-unmanaged";
import { installSkill, uninstallSkill } from "@/features/install/install";
import { openUpdate } from "@/features/update/update";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { useDnD } from "@/components/app/dnd";
import { SameNameBadge, SameNameLine } from "./same-name";

// 列宽：所有技能页是 名字 | 描述 | 装在哪 | 最近变化;作用域页是 开关 | 名字 | 描述 | 在这里的状态。
export const libraryCols = "grid-cols-[minmax(140px,220px)_minmax(0,1fr)_minmax(80px,160px)_92px]";
export const scopeCols = "grid-cols-[30px_minmax(140px,220px)_minmax(0,1fr)_minmax(90px,180px)]";

const rowClass =
  "grid min-h-9 items-center gap-x-4 border-b border-[rgba(17,24,39,0.06)] px-3 py-1.5 text-[12px] last:border-b-0";

// 最近几天内的变化用正文颜色，排查"最近是不是变过"时一眼看到。
const recentDays = 7;

// SkillRow 是库里一个技能的一行。scope 为空时是所有技能页 / 更新页的样子;有 scope 时是作用域页的样子:
// 开头是直接安装的开关，最后一列写在这里的状态;fromGlobal 表示它是同一个 Agent 全局装的(项目里也看得到),这里不能开关。
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
  const t = useT();
  const here = scope && !props.fromGlobal ? skillScopeIn(s, skill.id, scope) : undefined;
  // 作用域页说这一处的同名后果;所有技能页、更新页说它装出去的各处。
  const same = scope ? (here ? sameNameNote(s, scope, here.folder) : undefined) : skillSameNameNote(s, skill.id);
  const groups = s.groups.filter((g) => g.members?.includes(skill.id) && g.id !== props.hideGroup);
  const twins = sameNameInLibrary(s, skill);

  const toggle = () => {
    if (!scope) return;
    const sc = parseScope(scope);
    void (here?.direct ? uninstallSkill(skill.id, sc) : installSkill(skill.id, sc));
  };

  return (
    <article
      className={cn(
        "skill-row group/row cursor-grab transition-colors hover:bg-white/80 active:cursor-grabbing",
        rowClass,
        scope ? scopeCols : libraryCols,
        selected && "bg-primary-soft hover:bg-primary-soft",
      )}
      data-name={skill.name}
      data-selected={selected || undefined}
      onClick={() => toggleDrawer(skill.id)}
      draggable
      onDragStart={(e) => dnd.start(e, { kind: "skill", id: skill.id }, skill.name)}
      onDragEnd={dnd.end}
    >
      {scope && (
        <span onClick={(e) => e.stopPropagation()} className="flex">
          {!props.fromGlobal && (
            <Switch
              checked={!!here?.direct}
              held={!!here && !here.direct}
              onCheckedChange={toggle}
              title={here?.direct ? t.list.uninstallDirect : t.list.installDirect}
            />
          )}
        </span>
      )}
      <div className="flex min-w-0 items-center gap-1.5">
        {/* 名字优先：放不下时先截后面的标记。 */}
        <span className={cn("skill-name min-w-0 truncate text-[12.5px] font-semibold", scope && !here && !props.fromGlobal && "font-medium text-foreground-secondary")}>
          {skill.name}
        </span>
        {same && <SameNameBadge note={same} />}
        {/* 计入角标的才标出来;点标签直接打开所在集合的更新计划。 */}
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
        {groups.length > 0 && (
          <span
            className="max-w-24 shrink-[100] truncate rounded bg-black/5 px-[5px] py-px font-mono text-[9.5px] font-medium text-muted-foreground"
            title={t.list.groupsTitle(groups.map((g) => g.name))}
          >
            {groups[0].name}
            {groups.length > 1 && ` +${groups.length - 1}`}
          </span>
        )}
      </div>
      <div className="min-w-0">
        <p className="truncate text-[11.5px] text-muted-foreground" title={skill.description}>
          {props.showSource && (
            <span className="text-subtle" title={`${sourceKindLabel(skill.source.type)} · ${sourceLabel(s, skill.source)}`}>
              {sourceShort(s, skill.source)} ·{" "}
            </span>
          )}
          {skill.description}
        </p>
        {same && <SameNameLine note={same} />}
        {/* 有新版本、但更新会换掉用户的修改：直接写出来，更新计划里它默认不勾。 */}
        {pending(skill) && updateAvailable(skill) && skill.edited && (
          <p className="update-edited-note truncate text-[10.5px] text-warn-strong" title={t.list.editedTitle}>
            {t.list.edited}
          </p>
        )}
        {/* 库里同名的几个技能(内容不同，不合并)写出各装在哪里，免得只能靠描述分辨。 */}
        {twins.length > 0 && (
          <p className="dup-name-note truncate text-[10.5px] text-muted-foreground" title={t.list.twins(twins.length + 1, skill.name, installedWhere(s, skill.id))}>
            {t.list.twins(twins.length + 1, skill.name, installedWhere(s, skill.id))}
          </p>
        )}
      </div>
      {scope ? <ScopeState skill={skill} scope={scope} fromGlobal={!!props.fromGlobal} /> : <Where skill={skill} />}
      {!scope && <Changed skill={skill} />}
    </article>
  );
}

// Where 是所有技能页的"装在哪":常见的情况只有两个字，例外才多写;有地方要重新安装时标黄。
function Where({ skill }: { skill: Skill }) {
  const s = useSnapshot();
  const t = useT().list;
  const w = installedSummary(s, skill.id);
  return (
    <span className="skill-where flex min-w-0 items-center gap-1.5 text-[11.5px]" title={w.title || undefined}>
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
    <span className={cn("skill-changed truncate text-right text-[11px]", recent ? "text-foreground-secondary" : "text-subtle")} title={c.date.toLocaleString()}>
      {t.changed(c.kind, c.date)}
    </span>
  );
}

// ScopeState 是作用域页最后一列：直接安装 / 随组安装 / 来自全局 / 未安装，要重新安装时带上原因。
function ScopeState({ skill, scope, fromGlobal }: { skill: Skill; scope: ScopeKey; fromGlobal: boolean }) {
  const s = useSnapshot();
  const t = useT().list;
  const place = fromGlobal
    ? globalScopesSeenFrom(s, scope).map((key) => skillScopeIn(s, skill.id, key)).find(Boolean)
    : skillScopeIn(s, skill.id, scope);
  const via = (place?.groups ?? []).map((id) => s.groups.find((g) => g.id === id)?.name ?? "");
  const text = fromGlobal ? t.fromGlobal : place ? t.hereState(place.direct, via) : t.notInstalled;
  return (
    <span className="skill-state flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
      <span className={cn("min-w-0 truncate", !place && !fromGlobal && "text-subtle")} title={text}>
        {text}
      </span>
      {place && needsReinstall(place) && (
        <Badge tone={skillScopeStatus(place.status).tone} className="font-sans">
          {skillScopeStatus(place.status).label}
        </Badge>
      )}
    </span>
  );
}

// UnmanagedRow 是作用域里一个不是库装的技能文件夹(未纳管技能):标"未纳管",提交在项目仓库里的再标"在项目仓库里",
// 符号链接写出指向哪里。不能拖动、不打开详情，只提供在 Finder 中显示与入库。
export function UnmanagedRow({ skill }: { skill: UnmanagedSkill }) {
  const s = useSnapshot();
  const ui = useUI();
  const t = useT().list;
  const same = sameNameNote(s, scopeKey(skill.scope), skill.folder);
  // 来源记录认不出来源的，在 skills.sh 上找到来源后补上。
  const found = foundSource(useFoundSources(), skill);
  const source = skill.source_input || found;
  return (
    <article className={cn("unmanaged-row", rowClass, scopeCols)} data-name={skill.name}>
      <span className="grid size-[17px] place-items-center rounded-full border border-dashed border-border-strong" title={t.unmanagedTitle} />
      <div className="flex min-w-0 items-center gap-1.5">
        <span className="unmanaged-name min-w-0 truncate text-[12.5px] font-semibold text-foreground-secondary">{skill.name}</span>
        <Badge title={t.unmanagedTitle}>{t.unmanaged}</Badge>
        {skill.in_repository && <Badge title={t.inRepositoryTitle}>{t.inRepository}</Badge>}
        {!skill.in_repository && skill.in_project_lock && <Badge title={t.inLockTitle}>{t.inLock}</Badge>}
        {skill.link_outside && <Badge title={t.linkOutsideTitle(skill.link_target ?? "")}>{t.linkOutside}</Badge>}
        {same && <SameNameBadge note={same} />}
      </div>
      <div className="min-w-0">
        <p className="truncate text-[11.5px] text-muted-foreground" title={skill.description}>
          {skill.description}
        </p>
        {same && <SameNameLine note={same} />}
        {source && (
          <p className="unmanaged-source-label truncate text-[10.5px] text-muted-foreground" title={found ? t.foundTitle(found) : t.recordedTitle(source)}>
            {t.from(source)}
          </p>
        )}
        <p
          className="unmanaged-status truncate text-[10.5px] text-subtle"
          title={skill.link_target ? t.linkTitle(skill.link_target) : skill.folder}
        >
          {skill.link_target ? t.link(shortPath(s, skill.link_target)) : t.notByLibrary}
        </p>
      </div>
      <span className="flex items-center gap-2.5 text-[11px]">
        <Button
          variant="link"
          className="reveal-in-finder shrink-0 font-sans text-[11px]"
          onClick={() => void ui.run(() => AppService.RevealInFinder(skill.folder), { reload: false })}
        >
          {t.revealInFinder}
        </Button>
        <Button variant="link" className="fetch-unmanaged shrink-0 font-sans text-[11px]" title={t.fetchTitle} onClick={() => void fetchUnmanaged(skill)}>
          {t.fetch}
        </Button>
      </span>
    </article>
  );
}
