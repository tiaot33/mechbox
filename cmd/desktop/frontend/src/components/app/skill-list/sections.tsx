// 列表的分节：技能库页与作用域页都按集合或按组分节，作用域页另有未纳管技能与(项目里)来自全局的两节，以及下面那块没装在这里的。
// 节标题可以收起;集合与组的节标题可以拖到右栏作用域(集合 = 把它的技能逐个直接安装，组 = 组安装),
// 拖到同类的节标题上调整先后;组的节标题也接受技能与集合(加入该组)。集合的更新、历史与来源里的新技能放在集合的节标题上，组的重命名、删除与组安装于放在组的节标题上。
import type { Collection, Group, Skill, SkillID } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import {
  collectionSkills,
  hasUpdate,
  isLocal,
  pending,
  placeOf,
  projectName,
  revisionSummary,
  revisionText,
  scopeKey,
  skillScopesOf,
  sourceLabel,
  updateAvailable,
  wslTitle,
} from "@/lib/model";
import { cn } from "@/lib/utils";
import { openGraph, toggleSection, toggleUnfolded, useView } from "@/lib/view";
import { notInterested, previewCollection, previewNewSkills } from "@/features/fetch/preview";
import { deleteGroup, renameGroup } from "@/features/group/group";
import { uninstallGroup } from "@/features/install/install";
import { openHistory, openUpdate } from "@/features/update/update";
import { Button } from "@/components/ui/button";
import { useDnD } from "@/components/app/dnd";
import { Icon, SourceIcon } from "@/components/app/icons";
import { Chip, PlaceAgentMark, WSLBadge } from "@/components/app/parts";
import { SectionPick } from "./pick";

// Section 是一节：标题 + 一块行;给了 pick 时标题上有勾选框，一次勾上这一节的这些技能。defaultCollapsed 的节(项目里来自全局的)默认收起，点一下展开;
// 收起状态记在界面状态里，按 id 存与默认相反的那些。搜索时都展开，搜到的行都看得见。
export function Section({
  id,
  head,
  sub,
  defaultCollapsed = false,
  className,
  headProps,
  pick,
  children,
}: {
  id: string;
  head: React.ReactNode;
  sub?: React.ReactNode; // 标题下面的一行(集合里来源新发布的技能)
  defaultCollapsed?: boolean;
  className?: string;
  headProps?: React.HTMLAttributes<HTMLDivElement> & Record<`data-${string}`, unknown>;
  pick?: SkillID[];
  children: React.ReactNode;
}) {
  const flipped = useView((v) => v.flipped.has(id));
  const searching = useView((v) => !!v.search.trim());
  const collapsed = !searching && defaultCollapsed !== flipped;
  return (
    <section className={cn("list-section mt-3 first:mt-0", className)} data-section={id}>
      <div
        {...headProps}
        className={cn(
          "section-head group/head sticky top-0 z-[1] -mx-1 flex min-h-9 min-w-0 items-center gap-2 glass rounded-lg px-1 py-1 text-[12.5px] [--glass-drop:0_0_#0000]",
          headProps?.className,
        )}
      >
        <button
          type="button"
          className="sec-toggle grid size-5 shrink-0 place-items-center rounded text-muted-foreground hover:bg-black/5 hover:text-foreground"
          aria-expanded={!collapsed}
          onClick={() => toggleSection(id)}
        >
          <Icon name="down" className={cn("size-2.5 transition-transform", collapsed && "-rotate-90")} />
        </button>
        {pick && <SectionPick ids={pick} />}
        {head}
      </div>
      {sub}
      {!collapsed && <div className="section-rows mt-1 rounded-[10px] border border-hairline bg-white/55 shadow-tile">{children}</div>}
    </section>
  );
}

// Counts 是节标题上淡色的数字说明。
function Counts({ children }: { children: React.ReactNode }) {
  return <span className="shrink-0 text-[11px] text-muted-foreground">{children}</span>;
}

// DragHandle 是可拖的节标题前的把手。
function DragHandle() {
  return (
    <span className="grid shrink-0 grid-cols-[repeat(2,3px)] gap-0.5 opacity-50 group-hover/head:opacity-100" aria-hidden>
      {Array.from({ length: 6 }, (_, i) => (
        <i key={i} className="size-[3px] rounded-full bg-muted-foreground" />
      ))}
    </span>
  );
}

// inUse 是一节里算作在用的技能：装在至少一个地方的。技能库页节标题的个数和行的先后都按它。
const inUse = (s: ReturnType<typeof useSnapshot>, skills: Skill[]) => skills.filter((x) => skillScopesOf(s, x.id).length > 0);

// UseCounts 是技能库页节标题上的"在用 N · 共 M"。
function UseCounts({ skills }: { skills: Skill[] }) {
  const s = useSnapshot();
  const t = useT().list;
  return <Counts>{t.inUse(inUse(s, skills).length, skills.length)}</Counts>;
}

// CollectionHead 是集合的节标题：来源、在用几个(作用域页给 count,写装在这里几个)、来源版本与有变化的个数，
// 看来源里的其他技能、历史、更新，技能库页上还有关系图;可以整个拖到作用域。
export function CollectionHead({ collection, count, showGraph = false }: { collection: Collection; count?: string; showGraph?: boolean }) {
  const s = useSnapshot();
  const dnd = useDnD();
  const t = useT();
  const skills = collectionSkills(s, collection);
  const todo = skills.filter(pending).length;
  // "有变化"只算来源里有新内容的技能(跳过了的也算);来源里已删除、推测出改名的不算。
  const changed = skills.filter(updateAvailable).length;
  const local = isLocal(collection.source);
  const label = sourceLabel(s, collection.source);
  const facts = [revisionText(revisionSummary(skills)), changed ? t.drawer.changedCount(changed) : ""].filter(Boolean);
  return (
    <>
      <span
        className="collection-handle flex min-w-0 cursor-grab items-center gap-2 active:cursor-grabbing"
        draggable
        onDragStart={(e) => dnd.start(e, { kind: "collection", id: collection.id }, t.list.collectionDrag(label, skills.length))}
        onDragEnd={dnd.end}
        title={t.list.collectionDragTitle(skills.length)}
      >
        <DragHandle />
        <SourceIcon type={collection.source.type} className="size-3.5 text-muted-foreground" />
        <b className="section-name truncate font-semibold" title={wslTitle(local ? collection.source.label : undefined)}>
          {label}
        </b>
        {local && <WSLBadge path={collection.source.label} />}
      </span>
      {count ? <Counts>{count}</Counts> : <UseCounts skills={skills} />}
      <span className="min-w-0 grow truncate text-[11px] text-muted-foreground" title={facts.join(" · ")}>
        {facts.join(" · ")}
      </span>
      <span className="flex shrink-0 items-center gap-1">
        {showGraph && <GraphButton onClick={() => openGraph({ kind: "collection", id: collection.id })} />}
        <Button size="sm" variant="ghost" onClick={() => previewCollection(collection.id)} title={t.list.otherSkillsHint}>
          {t.drawer.otherSkills}
        </Button>
        <Button size="sm" variant="ghost" onClick={() => openHistory({ collection_id: collection.id }, t.list.historyTitle(label))}>
          {t.list.history}
        </Button>
        {!local && skills.some(hasUpdate) && (
          <Button size="sm" variant={todo ? "primary" : "default"} onClick={() => openUpdate(collection.id)}>
            <Icon name="up" />
            {todo ? t.list.updateCount(todo) : t.list.update}
          </Button>
        )}
      </span>
    </>
  );
}

// CollectionNews 是集合节标题下的一行：来源里新发布的技能，去入库或不感兴趣。
export function CollectionNews({ collection }: { collection: Collection }) {
  const s = useSnapshot();
  const t = useT();
  const fresh = collection.new_skills ?? [];
  if (!fresh.length) return null;
  return (
    <div className="collection-new flex min-w-0 items-center gap-2 px-8 pt-0.5 pb-1 text-[11px] text-muted-foreground">
      <span className="min-w-0 grow truncate">{t.drawer.newSkills(sourceLabel(s, collection.source), fresh)}</span>
      <Button variant="link" onClick={() => previewNewSkills(collection.id)}>
        {t.drawer.goFetch}
      </Button>
      <Button variant="link" onClick={() => notInterested(collection.id, fresh)}>
        {t.drawer.notInterested}
      </Button>
    </div>
  );
}

// GroupHead 是组的节标题：可以整组拖到作用域(组安装),写出组安装于哪些作用域(可以卸载),重命名与删除，技能库页上还有关系图。
// 作用域页给 count,写装在这里几个;否则写组里几个技能。
export function GroupHead({ group, count, showGraph = false }: { group: Group; count?: string; showGraph?: boolean }) {
  const s = useSnapshot();
  const dnd = useDnD();
  const t = useT();
  const scopes = s.groupInstalls.filter((a) => a.group_id === group.id).map((a) => a.scope);
  const size = group.members?.length ?? 0;
  return (
    <>
      <span
        className="group-handle flex min-w-0 cursor-grab items-center gap-2 active:cursor-grabbing"
        draggable
        onDragStart={(e) => dnd.start(e, { kind: "group", id: group.id }, t.list.groupDrag(group.name, size))}
        onDragEnd={dnd.end}
        title={t.list.groupDragTitle(size)}
      >
        <DragHandle />
        <Icon name="layers" className="size-3.5 text-group" />
        <b className="section-name truncate font-semibold">{group.name}</b>
      </span>
      <Counts>{count ?? t.list.skillCount(size)}</Counts>
      <span className="ml-1 inline-flex min-w-0 grow items-center gap-1 overflow-hidden text-[11px] whitespace-nowrap text-muted-foreground">
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
          <span className="text-muted-foreground">{t.list.notGroupInstalled}</span>
        )}
      </span>
      <span className="flex shrink-0 items-center gap-1">
        {showGraph && <GraphButton onClick={() => openGraph({ kind: "group", id: group.id })} />}
        <Button size="sm" variant="ghost" onClick={() => renameGroup(group.id)}>
          {t.list.rename}
        </Button>
        <Button size="sm" variant="ghost" className="text-bad-strong" onClick={() => deleteGroup(group.id)}>
          {t.list.deleteGroup}
        </Button>
      </span>
    </>
  );
}

// GraphButton 是节标题上打开这个集合或组的关系图的按钮。
function GraphButton({ onClick }: { onClick: () => void }) {
  const t = useT().graph;
  return (
    <Button size="sm" variant="ghost" className="graph-open" onClick={onClick} title={t.openTitle}>
      <Icon name="graph" />
      {t.open}
    </Button>
  );
}

// PlainHead 是不能拖的节标题：没有来源、没有归组、作用域页的未纳管技能与来自全局、作用域页下面那块(没装在这里的)的各节、项目页的各个 Agent。
export function PlainHead({ icon, name, count, title }: { icon?: React.ReactNode; name: string; count: React.ReactNode; title?: string }) {
  return (
    <span className="flex min-w-0 grow items-center gap-2" title={title}>
      {icon}
      <b className="section-name truncate font-semibold">{name}</b>
      <Counts>{count}</Counts>
    </span>
  );
}

// SkillRows 列出技能库页一节里的技能：装在至少一个地方的在前;没装的默认折成一行，点开再列出。
// 搜索时、或这一节一个都没装时(刚入库的集合)直接全部列出。
export function SkillRows({ id, skills, render }: { id: string; skills: Skill[]; render: (skill: Skill) => React.ReactNode }) {
  const s = useSnapshot();
  const t = useT().list;
  const searching = useView((v) => !!v.search.trim());
  const unfolded = useView((v) => v.unfolded.has(id));
  const used = inUse(s, skills);
  const idle = skills.filter((x) => !used.includes(x));
  const foldable = !searching && used.length > 0 && idle.length > 0;
  const label = unfolded ? t.hideNotInstalled : t.notInstalledFold(idle.map((x) => x.name));
  const shown = foldable && !unfolded ? used : [...used, ...idle];
  return (
    <>
      {shown.map(render)}
      {foldable && (
        <button
          type="button"
          className="not-installed-fold flex w-full min-w-0 items-center gap-1.5 px-3 py-2 text-left text-[11.5px] text-muted-foreground hover:bg-white/80 hover:text-foreground"
          aria-expanded={unfolded}
          onClick={() => toggleUnfolded(id)}
        >
          <Icon name="chev" className={cn("size-2.5 shrink-0 transition-transform", unfolded && "rotate-90")} />
          <span className="truncate">{label}</span>
        </button>
      )}
    </>
  );
}
