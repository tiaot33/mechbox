// 左栏：入库 / 市场入口;页面——所有技能 / 更新;集合(按来源)与组是所有技能页里的节，点了跳到那一节;底部同步与设置。
// "更新"的计数和 Dock 角标同一套规则：计入角标的技能数(技能库的 UpdateCheck.pending);集合每个来源一行，默认按来源名排，只有一个技能的也列出。
// 集合行可拖到右栏作用域(把集合里的技能逐个直接安装)或组上(加入该组);组行可拖到右栏作用域(组安装),也接受技能与集合(加入该组);
// 集合、组各自拖到同类的行上调整先后，和列表的分节一起变，右键节标题可以恢复默认顺序。两种行名字放不下时悬停显示全名，右键有各自的菜单。
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { ConfigStatus } from "@bindings/cmd/desktop";
import type { Collection, Group } from "@bindings/internal/library";
import { useSnapshot, configQuery } from "@/lib/library";
import { checkUpdates } from "@/features/update/check";
import { addCollectionToGroup, createGroup, deleteGroup, renameGroup } from "@/features/group/group";
import { previewCollection } from "@/features/fetch/preview";
import { openHistory, openUpdate } from "@/features/update/update";
import { useCollectionDrop, useDnD, useGroupDrop } from "./dnd";
import { useT } from "@/lib/i18n";
import {
  checkableCollections,
  collectionSkills,
  hasUpdate,
  isLocal,
  pending,
  shortPath,
  missingFromGroup,
  sourceKindLabel,
  sourceLabel,
  type Page,
} from "@/lib/model";
import { useBusy, useUI } from "@/lib/ui";
import { cn, toggled } from "@/lib/utils";
import { jumpTo, openPage, openSettings, resetOrder, useCurrentPage, useSortedCollections, useSortedGroups, useView } from "@/lib/view";
import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { Hint } from "@/components/ui/field";
import { openFetch } from "@/features/fetch/fetch-dialog";
import { Icon } from "./icons";
import { leftClass } from "./layout";
import { MarketDialog } from "@/features/fetch/market-dialog";
import { NavCount, navItem } from "./nav";
import { TailPath, tailPath, Truncated } from "./parts";
import { SyncDialog } from "@/features/settings/sync-dialog";

const noConfig: ConfigStatus = { sync: false, github_token: false };

// 本地目录集合的路径在左栏最多写这么长，再长省掉开头。
const pathMax = 24;

export function LeftSidebar() {
  const s = useSnapshot();
  const page = useCurrentPage();
  // 设置页保存 GitHub 令牌或同步设置后让它失效重读，保存晚于回到主界面完成时也一样。
  const config = useQuery(configQuery).data ?? noConfig;
  const ui = useUI();
  const busy = useBusy();
  const order = useView((v) => v.order);
  const t = useT().sidebar;
  const updates = s.skills.filter(pending).length;
  const checkable = checkableCollections(s);
  const collections = useSortedCollections(s);
  const groups = useSortedGroups(s);
  // 收起的分节(collections / groups)。
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const pageRow = (target: Page, icon: React.ReactNode, label: string, count: number, title?: string) => (
    <li key={target.kind}>
      <button
        type="button"
        className={navItem({ kind: "main" })}
        data-active={page.kind === target.kind}
        onClick={() => openPage(target)}
        title={title}
      >
        {icon}
        <span className="grow truncate">{label}</span>
        <NavCount value={count} />
      </button>
    </li>
  );

  return (
    <aside className={leftClass}>
      <SideButton onClick={(e) => void openFetch(s.info.home_dir, e.currentTarget)}>
        <Icon name="plus" />
        <span>{t.fetch}</span>
      </SideButton>
      <SideButton onClick={(e) => ui.open((close) => <MarketDialog onClose={close} />, { origin: e.currentTarget })}>
        <Icon name="tag" />
        <span>{t.market}</span>
      </SideButton>
      <ul className="flex flex-col gap-0.5">
        {pageRow({ kind: "all" }, <Icon name="bolt" />, t.allSkills, s.skills.length)}
        {updates > 0 && pageRow({ kind: "updates" }, <Icon name="up" />, t.updates, updates, t.updatesTitle)}
        <li>
          <button
            type="button"
            className={navItem()}
            disabled={!checkable.length || busy}
            onClick={() => checkUpdates(checkable)}
            title={t.checkTitle}
          >
            <Icon name="refresh" />
            <span className="grow">{t.check}</span>
          </button>
        </li>
      </ul>

      <SectionHead
        id="collections"
        name={t.collections}
        count={collections.length}
        collapsed={collapsed}
        onToggle={setCollapsed}
        menu={
          <ContextMenuItem disabled={!order.collections.length} onSelect={() => resetOrder("collection")}>
            {t.resetOrder}
          </ContextMenuItem>
        }
      >
        <span className="pr-1.5 text-[10px] font-medium text-subtle">{t.bySource}</span>
      </SectionHead>
      {!collapsed.has("collections") && (
        <ul className="mt-0.5 flex flex-col gap-0.5 pl-[15px]">
          {collections.map((c) => (
            <CollectionRow key={c.id} collection={c} />
          ))}
        </ul>
      )}
      {collections.length === 0 && !collapsed.has("collections") && <Hint className="mt-1 px-3">{t.noCollections}</Hint>}

      <SectionHead
        id="groups"
        name={t.groups}
        count={s.groups.length}
        collapsed={collapsed}
        onToggle={setCollapsed}
        menu={
          <>
            <ContextMenuItem onSelect={() => createGroup()}>{t.newGroupWith}</ContextMenuItem>
            <ContextMenuSeparator />
            <ContextMenuItem disabled={!order.groups.length} onSelect={() => resetOrder("group")}>
              {t.resetOrder}
            </ContextMenuItem>
          </>
        }
      >
        <Button
          variant="ghost"
          size="icon-sm"
          className="opacity-0 group-hover/sec:opacity-100 focus-visible:opacity-100"
          title={t.newGroup}
          onClick={() => createGroup()}
        >
          <Icon name="plus" />
        </Button>
      </SectionHead>
      {!collapsed.has("groups") && (
        <ul className="mt-0.5 flex flex-col gap-0.5 pl-[15px]">
          {groups.map((g) => (
            <GroupRow key={g.id} group={g} />
          ))}
        </ul>
      )}
      {s.groups.length === 0 && !collapsed.has("groups") && <Hint className="mt-1 px-3">{t.noGroups}</Hint>}

      <div className="grow" />
      <ul className="flex flex-col gap-0.5">
        <li>
          <button type="button" className={navItem()} onClick={() => ui.open((close) => <SyncDialog config={config} onClose={close} />)}>
            <Icon name="cloud" />
            <span className="grow">{t.sync}</span>
            <span className="text-[11px] text-muted-foreground">{config.sync ? (config.auto_push && t.autoPush[config.auto_push]) || t.manual : t.notConfigured}</span>
          </button>
        </li>
        <li>
          <button type="button" className={navItem()} onClick={() => openSettings()}>
            <Icon name="gear" />
            <span className="grow">{t.settings}</span>
          </button>
        </li>
      </ul>
      <div className="flex items-center gap-1.5 px-2.5 pt-1.5 pb-0.5 text-[11px] text-subtle" title={s.info.library_path}>
        <Icon name="database" className="size-[11px]" />
        <TailPath path={shortPath(s, s.info.library_path)} max={28} className="grow font-mono text-[10px]" />
      </div>
    </aside>
  );
}

// CollectionRow 是左栏的一个集合：点了跳到那一节;拖到右栏作用域 = 逐个直接安装，拖到组上 = 加入该组，拖到别的集合上 = 调整先后;
// 名字放不下时悬停显示全名;右键菜单是检查更新、更新、来源里的其他技能、更新历史与加入组。
function CollectionRow({ collection: c }: { collection: Collection }) {
  const s = useSnapshot();
  const dnd = useDnD();
  const busy = useBusy();
  const drop = useCollectionDrop(c.id);
  const groups = useSortedGroups(s);
  const tt = useT();
  const t = tt.sidebar;
  const label = sourceLabel(s, c.source);
  const local = isLocal(c.source);
  const skills = collectionSkills(s, c);
  return (
    <li>
      <ContextMenu>
        <Truncated tip={t.collectionTitle(sourceKindLabel(c.source.type), label)} always={local && tailPath(label, pathMax) !== label}>
          <ContextMenuTrigger asChild>
            <button
              type="button"
              className={navItem({ kind: "section" })}
              onClick={() => jumpTo({ kind: "collection", id: c.id })}
              draggable
              onDragStart={(e) => dnd.start(e, { kind: "collection", id: c.id }, tt.list.collectionDrag(label, skills.length))}
              onDragEnd={dnd.end}
              {...drop}
            >
              {local ? <span className="min-w-0 grow truncate">{tailPath(label, pathMax)}</span> : <span className="grow truncate">{label}</span>}
              <NavCount value={skills.length} accent />
            </button>
          </ContextMenuTrigger>
        </Truncated>
        <ContextMenuContent>
          {!local && (
            <ContextMenuItem disabled={busy} onSelect={() => checkUpdates([c.id])}>
              {t.check}
            </ContextMenuItem>
          )}
          {!local && skills.some(hasUpdate) && <ContextMenuItem onSelect={() => openUpdate(c.id)}>{t.update}</ContextMenuItem>}
          <ContextMenuItem onSelect={() => previewCollection(c.id)}>{t.otherSkills}</ContextMenuItem>
          <ContextMenuItem onSelect={() => openHistory({ collection_id: c.id }, tt.list.historyTitle(label))}>{t.history}</ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuSub>
            <ContextMenuSubTrigger>{t.addToGroup}</ContextMenuSubTrigger>
            <ContextMenuSubContent>
              {groups.map((g) => {
                const already = missingFromGroup(s, c.id, g.id).length === 0;
                return (
                  <ContextMenuItem key={g.id} disabled={already || busy} onSelect={() => addCollectionToGroup(g.id, c.id)}>
                    <span className="min-w-0 grow truncate">{g.name}</span>
                    {already && <span className="shrink-0 text-[10px] text-subtle">{tt.layout.alreadyThere}</span>}
                  </ContextMenuItem>
                );
              })}
              {s.groups.length > 0 && <ContextMenuSeparator />}
              <ContextMenuItem disabled={busy} onSelect={() => createGroup(c.skills ?? [])}>
                {t.newGroupWith}
              </ContextMenuItem>
            </ContextMenuSubContent>
          </ContextMenuSub>
        </ContextMenuContent>
      </ContextMenu>
    </li>
  );
}

// GroupRow 是左栏的一个组：点了跳到那一节;拖到右栏作用域 = 组安装，拖到别的组上 = 调整先后，接受技能与集合(加入本组);
// 名字放不下时悬停显示全名;悬停出现重命名、删除按钮，右键菜单是重命名、删除与新建组。
function GroupRow({ group: g }: { group: Group }) {
  const dnd = useDnD();
  const drop = useGroupDrop(g.id);
  const tt = useT();
  const t = tt.sidebar;
  return (
    <li>
      <ContextMenu>
        <Truncated tip={g.name}>
          <ContextMenuTrigger asChild>
            <div
              role="button"
              tabIndex={0}
              className={navItem({ kind: "section" })}
              onClick={() => jumpTo({ kind: "group", id: g.id })}
              draggable
              onDragStart={(e) => dnd.start(e, { kind: "group", id: g.id }, tt.list.groupDrag(g.name, g.members?.length ?? 0))}
              onDragEnd={dnd.end}
              {...drop}
            >
              <span className="grow truncate">{g.name}</span>
              <span className="row-actions hidden gap-0.5 group-hover:inline-flex">
                <Button
                  variant="ghost"
                  size="icon-sm"
                  title={t.rename}
                  onClick={(e) => {
                    e.stopPropagation();
                    void renameGroup(g.id);
                  }}
                >
                  <Icon name="edit" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  title={t.deleteGroup}
                  onClick={(e) => {
                    e.stopPropagation();
                    void deleteGroup(g.id);
                  }}
                >
                  <Icon name="close" />
                </Button>
              </span>
              <span className="group-hover:hidden">
                <NavCount value={g.members?.length ?? 0} accent />
              </span>
            </div>
          </ContextMenuTrigger>
        </Truncated>
        <ContextMenuContent>
          <ContextMenuItem onSelect={() => renameGroup(g.id)}>{t.renameWith}</ContextMenuItem>
          <ContextMenuItem danger onSelect={() => deleteGroup(g.id)}>
            {t.deleteGroupWith}
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem onSelect={() => createGroup()}>{t.newGroupWith}</ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
    </li>
  );
}

function SideButton({ onClick, children }: { onClick: (e: React.MouseEvent<HTMLButtonElement>) => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      className="side-btn mb-2.5 flex h-8 w-full items-center justify-center gap-[7px] rounded-lg border border-border bg-white text-[12.5px] font-medium shadow-[0_1px_2px_rgba(0,0,0,0.03)] transition-colors hover:border-border-strong hover:bg-gray-50 [&_[data-slot=icon]]:size-3"
      onClick={onClick}
    >
      {children}
    </button>
  );
}

// SectionHead 是可折叠的分节标题：箭头 + 名称 + 数量，右侧放说明或操作(悬停时出现),menu 是右键菜单的选项;id 记折叠状态。
function SectionHead({
  id,
  name,
  count,
  collapsed,
  onToggle,
  menu,
  children,
}: {
  id: string;
  name: string;
  count: number;
  collapsed: Set<string>;
  onToggle: (update: (set: Set<string>) => Set<string>) => void;
  menu: React.ReactNode;
  children?: React.ReactNode;
}) {
  const closed = collapsed.has(id);
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div className="nav-sec group/sec flex items-center justify-between pt-2.5 pr-0.5">
          <button
            type="button"
            className="sec-toggle flex h-[26px] min-w-0 flex-1 items-center gap-[5px] rounded-md pr-1.5 pl-2 text-[11px] font-semibold text-muted-foreground hover:bg-black/[0.04] hover:text-foreground"
            aria-expanded={!closed}
            onClick={() => onToggle((set) => toggled(set, id))}
          >
            <Icon name="down" className={cn("size-2.5 transition-transform", closed && "-rotate-90")} />
            <span>{name}</span>
            <span className="ml-0.5 font-mono text-[10px] font-medium text-subtle">{count}</span>
          </button>
          {children}
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>{menu}</ContextMenuContent>
    </ContextMenu>
  );
}
