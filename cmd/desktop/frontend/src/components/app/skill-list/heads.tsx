// 列表顶部的摘要条：选中作用域时的作用域摘要，筛选组时的组标题条，筛选集合时的集合标题条。
import type { Collection, Group } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import {
  collectionSkills,
  groupStateIn,
  hasUpdate,
  isLocal,
  needsReinstall,
  parseScope,
  pending,
  placeOf,
  projectName,
  revisionSummary,
  revisionText,
  scopeKey,
  scopeLabel,
  shortPath,
  skillScopesIn,
  sourceLabel,
  unmanagedIn,
  updateAvailable,
  viaGroupCount,
  type ScopeKey,
} from "@/lib/model";
import { cn } from "@/lib/utils";
import { offerFetchUnmanaged } from "@/features/fetch/fetch-unmanaged-all";
import { notInterested, previewCollection, previewNewSkills } from "@/features/fetch/preview";
import { deleteGroup, renameGroup } from "@/features/group/group";
import { installGroup, uninstallGroup } from "@/features/install/install";
import { reinstall } from "@/features/install/reinstall";
import { openHistory, openUpdate } from "@/features/update/update";
import { Button } from "@/components/ui/button";
import { useDnD } from "@/components/app/dnd";
import { Icon, SourceIcon } from "@/components/app/icons";
import { Chip, PlaceAgentMark } from "@/components/app/parts";

// 列表顶部的摘要条(作用域摘要、更新列表、集合标题条)共用的外观。
export const barClass = "a-scope-sum mx-3.5 mt-3 flex flex-col gap-2 rounded-[10px] border border-hairline bg-glass-card px-3 py-2.5 shadow-tile";

export const barRowClass = "a-sum-row flex min-w-0 items-center gap-2.5";

// ScopeSummary 是选中作用域时列表顶部的摘要:Agent 目录、健康状态、全部重新安装，以及每个组的整组开关。
export function ScopeSummary({ scope, directory }: { scope: ScopeKey; directory: string | undefined }) {
  const s = useSnapshot();
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
          <Button size="sm" onClick={() => reinstall({ scope: sc }, scopeLabel(s, sc))}>
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
            onClick={() => void offerFetchUnmanaged(s.agents, [sc], { kind: "scope", label: scopeLabel(s, sc) }, false)}
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
                onClick={() => (on ? uninstallGroup(g.id, sc) : installGroup(g.id, sc))}
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
export function GroupHead({ group, scope }: { group: Group; scope: ScopeKey | null }) {
  const s = useSnapshot();
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
              <Chip key={scopeKey(sc)} small removeTitle={t.list.uninstallGroupHere} onRemove={() => uninstallGroup(group.id, sc)}>
                <PlaceAgentMark code={sc.agent} place={placeOf(sc)} />
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
          <Button size="sm" onClick={() => installGroup(group.id, parseScope(scope))}>
            {t.list.groupInstallTo(scopeLabel(s, parseScope(scope)))}
          </Button>
        )}
        <Button size="sm" onClick={() => renameGroup(group.id)}>
          {t.list.rename}
        </Button>
        <Button size="sm" variant="danger" onClick={() => deleteGroup(group.id)}>
          {t.list.deleteGroup}
        </Button>
      </span>
    </div>
  );
}

// CollectionHead 是筛选某个集合时的标题条：来源、集合里的技能在哪个来源版本上、更新、历史，以及来源里的新技能。
export function CollectionHead({ collection }: { collection: Collection }) {
  const s = useSnapshot();
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
        <Button size="sm" onClick={() => previewCollection(collection.id)} title={t.list.otherSkillsHint}>
          {t.drawer.otherSkills}
        </Button>
        <Button size="sm" onClick={() => openHistory({ collection_id: collection.id }, t.list.historyTitle(label))}>
          {t.list.history}
        </Button>
        {!local && skills.some(hasUpdate) && (
          <Button size="sm" variant={todo ? "primary" : "default"} onClick={() => openUpdate(collection.id)}>
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
          <Button variant="link" onClick={() => previewNewSkills(collection.id)}>
            {t.drawer.goFetch}
          </Button>
          <Button variant="link" onClick={() => notInterested(collection.id, fresh)}>
            {t.drawer.notInterested}
          </Button>
        </div>
      )}
    </div>
  );
}
