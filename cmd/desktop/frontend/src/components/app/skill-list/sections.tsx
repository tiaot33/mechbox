// 列表的分节：技能库页与作用域页都按集合或按组分节，作用域页另有未纳管技能与(项目里)来自全局的两节，以及下面那块没装在这里的。
// 节标题可以收起;集合与组的节标题可以拖到右栏作用域(集合 = 把它的技能逐个直接安装，组 = 组安装),
// 拖到同类的节标题上调整先后;组的节标题也接受技能与集合(加入该组)。
// 节标题上只常驻名字、统计和要处理的事(集合的更新、来源里的新技能，组安装于哪些作用域);关系图、看来源里的其他技能、历史、
// 组的重命名与删除这类不常用的收在标题右边的「⋯」里，右键节标题是同一个菜单。
// 节标题和技能行对齐：标题的勾选框和行首的勾选框在同一列，后面的图标和行里的技能名对齐;收起箭头在最右边。
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
import { toggleSection, toggleUnfolded, useView } from "@/lib/view";
import { notInterested, previewNewSkills } from "@/features/fetch/preview";
import { uninstallGroup } from "@/features/install/install";
import { openUpdate } from "@/features/update/update";
import { Button } from "@/components/ui/button";
import { ContextMenu, ContextMenuContent, ContextMenuTrigger } from "@/components/ui/context-menu";
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { ContextMenuEntries, DropdownMenuEntries, useCollectionMenu, useGroupMenu, type MenuEntry } from "@/components/app/menus";
import { useCollectionDrop, useDnD, useGroupDrop } from "@/components/app/dnd";
import { Icon, SourceIcon } from "@/components/app/icons";
import { Chip, PlaceAgentMark, WSLBadge } from "@/components/app/parts";
import { SectionPick } from "./pick";
import { nameIndent, pickColumn } from "./rows";

// Section 是一节：标题 + 一块行。标题左边留一列和行首的勾选框对齐(gutter;更新页的行不能勾选，不留),
// 给了 pick 时这一列是勾选框，一次勾上这一节的这些技能;actions 是标题右边常驻的(要处理的事),menu 是收起来的不常用的动作。
// defaultCollapsed 的节(项目里来自全局的)默认收起，点一下展开;收起状态记在界面状态里，按 id 存与默认相反的那些。搜索时都展开，搜到的行都看得见。
export function Section({
  id,
  head,
  actions,
  menu,
  sub,
  defaultCollapsed = false,
  gutter = true,
  className,
  headProps,
  pick,
  children,
}: {
  id: string;
  head: React.ReactNode;
  actions?: React.ReactNode;
  menu?: MenuEntry[];
  sub?: React.ReactNode; // 标题下面的一行(集合里来源新发布的技能)
  defaultCollapsed?: boolean;
  gutter?: boolean;
  className?: string;
  headProps?: React.HTMLAttributes<HTMLDivElement> & Record<`data-${string}`, unknown>;
  pick?: SkillID[];
  children: React.ReactNode;
}) {
  const flipped = useView((v) => v.flipped.has(id));
  const searching = useView((v) => !!v.search.trim());
  const collapsed = !searching && defaultCollapsed !== flipped;
  const t = useT().list;
  const bar = (
    <div
      {...headProps}
      className={cn(
        // 吸顶时底下的行滚过去，标题用内容区的不透明底色挡住。
        "section-head group/head sticky top-0 z-[1] flex min-h-9 min-w-0 items-center gap-2 border-b border-row-line px-3 py-1 text-body bg-tile",
        headProps?.className,
      )}
    >
      {gutter && <span className={cn("mr-2 flex shrink-0", pickColumn)}>{pick && <SectionPick ids={pick} />}</span>}
      <span className="flex min-w-0 grow items-center gap-2">{head}</span>
      {actions && <span className="flex shrink-0 items-center gap-1">{actions}</span>}
      {menu && <SectionMenu entries={menu} />}
      <button
        type="button"
        className="sec-toggle grid size-5 shrink-0 place-items-center rounded-control text-muted-foreground hover:bg-fill hover:text-foreground"
        aria-expanded={!collapsed}
        title={collapsed ? t.expandSection : t.collapseSection}
        onClick={() => toggleSection(id)}
      >
        <Icon name="down" className={cn("size-2.5 transition-transform", collapsed && "-rotate-90")} />
      </button>
    </div>
  );
  return (
    <section className={cn("list-section mt-4 first:mt-0", className)} data-section={id}>
      {menu ? (
        <ContextMenu>
          <ContextMenuTrigger asChild>{bar}</ContextMenuTrigger>
          <ContextMenuContent>
            <ContextMenuEntries entries={menu} />
          </ContextMenuContent>
        </ContextMenu>
      ) : (
        bar
      )}
      {sub}
      {!collapsed && <div className="section-rows">{children}</div>}
    </section>
  );
}

// SectionMenu 是节标题右边的「⋯」:点开是这一节不常用的动作，和右键节标题的菜单一样。
function SectionMenu({ entries }: { entries: MenuEntry[] }) {
  const t = useT().list;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" className="section-more" title={t.sectionMore} aria-label={t.sectionMore}>
          <Icon name="more" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuEntries entries={entries} />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// Counts 是节标题上淡色的数字说明。
function Counts({ children }: { children: React.ReactNode }) {
  // 放不下时比名字先截短(名字是认出这一节的依据)，比后面的来源版本晚截。
  return <span className="min-w-0 shrink-[30] truncate text-caption text-muted-foreground">{children}</span>;
}

// inUse 是一节里算作在用的技能：装在至少一个地方的。技能库页节标题的个数和行的先后都按它。
const inUse = (s: ReturnType<typeof useSnapshot>, skills: Skill[]) => skills.filter((x) => skillScopesOf(s, x.id).length > 0);

// UseCounts 是技能库页节标题上的"在用 N · 共 M"。
function UseCounts({ skills }: { skills: Skill[] }) {
  const s = useSnapshot();
  const t = useT().list;
  return <Counts>{t.inUse(inUse(s, skills).length, skills.length)}</Counts>;
}

// CollectionSection 是集合的一节。标题：来源、在用几个(作用域页给 count,写装在这里几个)、来源版本与有变化的个数，有更新时右边是「更新」;
// 「⋯」里是关系图(技能库页)、看来源里的其他技能与历史。标题可以整个拖到作用域，也接受拖来的集合(调整先后);标题下面是来源里新发布的技能。
export function CollectionSection({
  id,
  collection,
  count,
  pick,
  showGraph = false,
  children,
}: {
  id: string;
  collection: Collection;
  count?: string;
  pick: SkillID[];
  showGraph?: boolean;
  children: React.ReactNode;
}) {
  const s = useSnapshot();
  const dnd = useDnD();
  const drop = useCollectionDrop(collection.id);
  const t = useT();
  const skills = collectionSkills(s, collection);
  const todo = skills.filter(pending).length;
  // "有变化"只算来源里有新内容的技能(跳过了的也算);来源里已删除、推测出改名的不算。
  const changed = skills.filter(updateAvailable).length;
  const local = isLocal(collection.source);
  const label = sourceLabel(s, collection.source);
  const facts = [revisionText(revisionSummary(skills)), changed ? t.drawer.changedCount(changed) : ""].filter(Boolean);
  const menu = useCollectionMenu(collection, { graph: showGraph });
  const head = (
    <>
      <span
        className="collection-handle flex min-w-0 cursor-grab items-center gap-2 active:cursor-grabbing"
        draggable
        onDragStart={(e) => dnd.start(e, { kind: "collection", id: collection.id }, t.list.collectionDrag(label, skills.length))}
        onDragEnd={dnd.end}
        title={t.list.collectionDragTitle(skills.length)}
      >
        <SourceIcon type={collection.source.type} className="size-3.5 shrink-0 text-muted-foreground" />
        <b className="section-name truncate font-mono font-semibold" title={wslTitle(local ? collection.source.label : undefined)}>
          {label}
        </b>
        {local && <WSLBadge path={collection.source.label} />}
      </span>
      {count ? <Counts>{count}</Counts> : <UseCounts skills={skills} />}
      <span className="min-w-0 grow shrink-[100] truncate text-caption text-muted-foreground" title={facts.join(" · ")}>
        {facts.join(" · ")}
      </span>
    </>
  );
  const actions = !local && skills.some(hasUpdate) && (
    <Button size="sm" onClick={() => openUpdate(collection.id)}>
      <Icon name="up" />
      {todo ? t.list.updateCount(todo) : t.list.update}
    </Button>
  );
  return (
    <Section id={id} pick={pick} head={head} actions={actions || undefined} menu={menu} sub={<CollectionNews collection={collection} />} headProps={drop}>
      {children}
    </Section>
  );
}

// CollectionNews 是集合节标题下的一行：来源里新发布的技能，去入库或不感兴趣。和技能名对齐。
function CollectionNews({ collection }: { collection: Collection }) {
  const s = useSnapshot();
  const t = useT();
  const fresh = collection.new_skills ?? [];
  if (!fresh.length) return null;
  return (
    <div className={cn("collection-new flex min-w-0 items-center gap-3 border-b border-row-line py-1 pr-3 text-caption text-muted-foreground", nameIndent)}>
      <span className="min-w-0 grow truncate">{t.drawer.newSkills(sourceLabel(s, collection.source), fresh)}</span>
      <Button variant="link" className="text-caption" onClick={() => previewNewSkills(collection.id)}>
        {t.drawer.goFetch}
      </Button>
      <Button variant="ghost" size="inline" onClick={() => notInterested(collection.id, fresh)}>
        {t.drawer.notInterested}
      </Button>
    </div>
  );
}

// GroupSection 是组的一节。标题：组名、几个技能(作用域页给 count,写装在这里几个)、组安装于哪些作用域(可以卸载);
// 「⋯」里是关系图(技能库页)、重命名与删除。标题可以整组拖到作用域(组安装),也接受技能与集合(加入该组)、拖来的组(调整先后)。
export function GroupSection({
  id,
  group,
  count,
  pick,
  showGraph = false,
  children,
}: {
  id: string;
  group: Group;
  count?: string;
  pick: SkillID[];
  showGraph?: boolean;
  children: React.ReactNode;
}) {
  const s = useSnapshot();
  const dnd = useDnD();
  const drop = useGroupDrop(group.id);
  const t = useT();
  const scopes = s.groupInstalls.filter((a) => a.group_id === group.id).map((a) => a.scope);
  const size = group.members?.length ?? 0;
  const menu = useGroupMenu(group, { graph: showGraph });
  const head = (
    <>
      <span
        className="group-handle flex min-w-0 cursor-grab items-center gap-2 active:cursor-grabbing"
        draggable
        onDragStart={(e) => dnd.start(e, { kind: "group", id: group.id }, t.list.groupDrag(group.name, size))}
        onDragEnd={dnd.end}
        title={t.list.groupDragTitle(size)}
      >
        <Icon name="layers" className="size-3.5 shrink-0 text-muted-foreground" />
        <b className="section-name truncate font-semibold">{group.name}</b>
      </span>
      <Counts>{count ?? t.list.skillCount(size)}</Counts>
      <span className="ml-1 inline-flex min-w-0 grow shrink-[100] items-center gap-1 overflow-hidden text-caption whitespace-nowrap text-muted-foreground">
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
    </>
  );
  return (
    <Section id={id} pick={pick} head={head} menu={menu} headProps={drop}>
      {children}
    </Section>
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
          className="not-installed-fold flex w-full min-w-0 items-center gap-4 px-3 py-2 text-left text-caption text-muted-foreground hover:bg-fill hover:text-foreground"
          aria-expanded={unfolded}
          onClick={() => toggleUnfolded(id)}
        >
          {/* 箭头在勾选框那一列，文字和技能名对齐。 */}
          <span className={cn("flex shrink-0 justify-center", pickColumn)}>
            <Icon name="chev" className={cn("size-2.5 transition-transform", unfolded && "rotate-90")} />
          </span>
          <span className="truncate">{label}</span>
        </button>
      )}
    </>
  );
}
