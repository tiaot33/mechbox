// 左栏：入库 / 市场入口;筛选——所有技能 / 更新 / 集合(按来源)/ 组;底部同步与设置。
// "更新"的计数是有新版本的集合数(本地目录来源不计);只有一个技能的集合不列出，集合这一层只在有两个以上技能时出现。
// 组行可拖到右栏作用域(组安装),也接受技能卡片(加入该组)。
import { useState } from "react";
import type { ConfigStatus } from "@bindings/cmd/desktop";
import { SourceType } from "@bindings/internal/library";
import { checkUpdates, createGroup, deleteGroup, renameGroup } from "@/lib/actions";
import { alreadyInGroup, dropProps, useDnD } from "@/lib/dnd";
import type { Snapshot } from "@/lib/library";
import {
  checkableCollections,
  sameFilter,
  shortPath,
  sourceKindLabel,
  sourceLabel,
  updatableCollections,
  type SkillFilter,
} from "@/lib/model";
import { useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { Icon } from "./icons";
import { leftClass } from "./layout";
import { MarketDialog } from "./market-dialog";
import { NavCount, navItem } from "./nav";
import { TailPath, toggled } from "./parts";
import { SyncDialog } from "./sync-dialog";

interface FilterSidebarProps {
  s: Snapshot;
  config: ConfigStatus;
  filter: SkillFilter;
  onFilter: (filter: SkillFilter) => void;
  onFetch: () => void;
  onSettings: () => void;
}

export function FilterSidebar({ s, config, filter, onFilter, onFetch, onSettings }: FilterSidebarProps) {
  const ui = useUI();
  const dnd = useDnD();
  const updates = updatableCollections(s).length;
  const checkable = checkableCollections(s);
  const collections = s.collections.filter((c) => (c.skills?.length ?? 0) > 1);
  // 收起的分节("集合" / "组")。
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  // 再点一次当前筛选回到"所有技能",与原型一致。
  const pick = (target: SkillFilter) => onFilter(sameFilter(filter, target) ? { kind: "all" } : target);
  // 本地路径放不下时省掉开头整段、保留末尾(末段才是技能目录名)。
  const row = (target: SkillFilter, icon: React.ReactNode | null, label: string, count: number, title?: string, local = false) => (
    <li key={JSON.stringify(target)}>
      <button
        type="button"
        className={navItem({ kind: icon ? "main" : "section" })}
        data-active={sameFilter(filter, target)}
        onClick={() => pick(target)}
        title={title}
      >
        {icon}
        {local ? <TailPath path={label} max={24} className="grow" /> : <span className="grow truncate">{label}</span>}
        <NavCount value={count} accent={!icon} />
      </button>
    </li>
  );

  return (
    <aside className={leftClass}>
      <SideButton onClick={onFetch}>
        <Icon name="plus" />
        <span>入库技能</span>
      </SideButton>
      <SideButton onClick={() => ui.open((close) => <MarketDialog onClose={close} />)}>
        <Icon name="tag" />
        <span>技能市场</span>
      </SideButton>
      <ul className="flex flex-col gap-0.5">
        {row({ kind: "all" }, <Icon name="bolt" />, "所有技能", s.skills.length)}
        {updates > 0 && row({ kind: "updates" }, <Icon name="up" />, "更新", updates, "有新版本的来源")}
        <li>
          <button
            type="button"
            className={navItem()}
            disabled={!checkable.length || ui.busy}
            onClick={() => checkUpdates(ui, s, checkable)}
            title="检查各来源是否有新版本(只读，本地目录来源不检查)"
          >
            <Icon name="refresh" />
            <span className="grow">检查更新</span>
          </button>
        </li>
      </ul>

      <SectionHead name="集合" count={collections.length} collapsed={collapsed} onToggle={setCollapsed}>
        <span className="pr-1.5 text-[10px] font-medium text-subtle">按来源</span>
      </SectionHead>
      {!collapsed.has("集合") && (
        <ul className="mt-0.5 flex flex-col gap-0.5 pl-[15px]">
          {collections.map((c) =>
            row(
              { kind: "collection", collectionID: c.id },
              null,
              sourceLabel(s, c.source),
              c.skills?.length ?? 0,
              `${sourceKindLabel[c.source.type]} · ${sourceLabel(s, c.source)}`,
              c.source.type === SourceType.SourceLocal,
            ),
          )}
        </ul>
      )}
      {collections.length === 0 && !collapsed.has("集合") && (
        <Hint className="mt-1 px-3">从同一来源入库两个以上技能后，按来源自动归成集合。</Hint>
      )}

      <SectionHead name="组" count={s.groups.length} collapsed={collapsed} onToggle={setCollapsed}>
        <Button
          variant="ghost"
          size="icon-sm"
          className="opacity-0 group-hover/sec:opacity-100 focus-visible:opacity-100"
          title="新建组"
          onClick={() => createGroup(ui)}
        >
          <Icon name="plus" />
        </Button>
      </SectionHead>
      {!collapsed.has("组") && (
        <ul className="mt-0.5 flex flex-col gap-0.5 pl-[15px]">
          {s.groups.map((g) => {
            const target: SkillFilter = { kind: "group", groupID: g.id };
            const already = !!dnd.item && alreadyInGroup(s, dnd.item, g.id);
            const canDrop = dnd.item?.kind === "skill" && !already;
            return (
              <li key={g.id}>
                <div
                  role="button"
                  tabIndex={0}
                  className={navItem({ kind: "section" })}
                  data-active={sameFilter(filter, target)}
                  data-already={already || undefined}
                  onClick={() => pick(target)}
                  draggable
                  onDragStart={(e) => dnd.start(e, { kind: "group", id: g.id }, `组「${g.name}」· ${g.members?.length ?? 0} 个技能`)}
                  onDragEnd={dnd.end}
                  data-drop-group={g.id}
                  {...dropProps(canDrop, () => dnd.dropOnGroup(g.id))}
                  title="点击筛选;拖到右侧作用域 = 组安装;把技能卡片拖到这里 = 加入本组"
                >
                  <span className="grow truncate">{g.name}</span>
                  <span className="row-actions hidden gap-0.5 group-hover:inline-flex">
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      title="重命名"
                      onClick={(e) => {
                        e.stopPropagation();
                        void renameGroup(ui, s, g.id);
                      }}
                    >
                      <Icon name="edit" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      title="删除组"
                      onClick={(e) => {
                        e.stopPropagation();
                        void deleteGroup(ui, s, g.id);
                      }}
                    >
                      <Icon name="close" />
                    </Button>
                  </span>
                  <span className="group-hover:hidden">
                    <NavCount value={g.members?.length ?? 0} accent />
                  </span>
                </div>
              </li>
            );
          })}
        </ul>
      )}
      {s.groups.length === 0 && !collapsed.has("组") && (
        <Hint className="mt-1 px-3">还没有组。按场景把技能归到一起，就能整组安装。</Hint>
      )}

      <div className="grow" />
      <ul className="flex flex-col gap-0.5">
        <li>
          <button type="button" className={navItem()} onClick={() => ui.open((close) => <SyncDialog config={config} onClose={close} />)}>
            <Icon name="cloud" />
            <span className="grow">同步</span>
            <span className="text-[11px] text-muted-foreground">{config.sync ? "手动" : "未配置"}</span>
          </button>
        </li>
        <li>
          <button type="button" className={navItem()} onClick={onSettings}>
            <Icon name="gear" />
            <span className="grow">设置</span>
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

function SideButton({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
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

// SectionHead 是可折叠的分节标题：箭头 + 名称 + 数量，右侧放说明或操作(悬停时出现)。
function SectionHead({
  name,
  count,
  collapsed,
  onToggle,
  children,
}: {
  name: string;
  count: number;
  collapsed: Set<string>;
  onToggle: (update: (set: Set<string>) => Set<string>) => void;
  children?: React.ReactNode;
}) {
  const closed = collapsed.has(name);
  return (
    <div className="nav-sec group/sec flex items-center justify-between pt-2.5 pr-0.5">
      <button
        type="button"
        className="sec-toggle flex h-[26px] min-w-0 flex-1 items-center gap-[5px] rounded-md pr-1.5 pl-2 text-[11px] font-semibold text-muted-foreground hover:bg-black/[0.04] hover:text-foreground"
        aria-expanded={!closed}
        onClick={() => onToggle((set) => toggled(set, name))}
      >
        <Icon name="down" className={cn("size-2.5 transition-transform", closed && "-rotate-90")} />
        <span>{name}</span>
        <span className="ml-0.5 font-mono text-[10px] font-medium text-subtle">{count}</span>
      </button>
      {children}
    </div>
  );
}
