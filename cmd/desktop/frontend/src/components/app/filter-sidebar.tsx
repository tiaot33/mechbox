// 左栏：入库 / 市场入口;筛选——所有技能 / 有可用更新 / 集合(按来源)/ 组;底部同步与设置。
// 组行可拖到右栏作用域(组安装),也接受技能卡片(加入该组)。
import { useState } from "react";
import type { ConfigStatus } from "@bindings/cmd/desktop";
import { SourceType } from "@bindings/internal/library";
import { checkUpdates, createGroup, deleteGroup, renameGroup } from "@/lib/actions";
import { alreadyInGroup, dropProps, useDnD } from "@/lib/dnd";
import type { Snapshot } from "@/lib/library";
import { sameFilter, shortPath, sourceKindLabel, sourceLabel, updateAvailable, type SkillFilter } from "@/lib/model";
import { useUI } from "@/lib/ui";
import { Icon } from "./icons";
import { MarketDialog } from "./market-dialog";
import { Count, toggled } from "./parts";
import { SettingsDialog } from "./settings-dialog";
import { SyncDialog } from "./sync-dialog";

interface FilterSidebarProps {
  s: Snapshot;
  config: ConfigStatus;
  filter: SkillFilter;
  onFilter: (filter: SkillFilter) => void;
  onFetch: () => void;
}

export function FilterSidebar({ s, config, filter, onFilter, onFetch }: FilterSidebarProps) {
  const ui = useUI();
  const dnd = useDnD();
  const updates = s.skills.filter(updateAvailable).length;
  // 收起的分节("集合" / "组")。
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  // 再点一次当前筛选回到"所有技能",与原型一致。
  const pick = (target: SkillFilter) => onFilter(sameFilter(filter, target) ? { kind: "all" } : target);
  // startEllipsis:放不下时省略开头、保留末尾(本地路径的末段才是技能目录名)。
  const row = (target: SkillFilter, icon: React.ReactNode | null, label: string, count: number, title?: string, startEllipsis = false) => (
    <li key={JSON.stringify(target)}>
      <button
        type="button"
        className={`nav-item ${sameFilter(filter, target) ? "active" : ""}`}
        onClick={() => pick(target)}
        title={title}
      >
        {icon}
        {startEllipsis ? (
          <span className="grow ellipsis ellipsis-start">{`\u200e${label}\u200e`}</span>
        ) : (
          <span className="grow ellipsis">{label}</span>
        )}
        <Count value={count} />
      </button>
    </li>
  );

  return (
    <aside className="a-left">
      <button type="button" className="side-btn" onClick={onFetch}>
        <Icon name="plus" />
        <span>入库技能</span>
      </button>
      <button type="button" className="side-btn" onClick={() => ui.open((close) => <MarketDialog onClose={close} />)}>
        <Icon name="tag" />
        <span>技能市场</span>
      </button>
      <ul className="nav">
        {row({ kind: "all" }, <Icon name="bolt" />, "所有技能", s.skills.length)}
        {updates > 0 && row({ kind: "updates" }, <Icon name="up" />, "有可用更新", updates)}
        <li>
          <button
            type="button"
            className="nav-item"
            disabled={!s.skills.length || ui.busy}
            onClick={() => checkUpdates(ui, s, s.skills.map((x) => x.id))}
            title="检查全部技能的来源是否有新版本"
          >
            <Icon name="refresh" />
            <span className="grow">检查更新</span>
          </button>
        </li>
      </ul>

      <SectionHead name="集合" count={s.collections.length} collapsed={collapsed} onToggle={setCollapsed}>
        <span className="sec-note">按来源</span>
      </SectionHead>
      <ul className={`nav sec-list ${collapsed.has("集合") ? "hidden" : ""}`}>
        {s.collections.map((c) =>
          row(
            { kind: "collection", sourceID: c.source.id },
            null,
            sourceLabel(s, c.source),
            c.skills?.length ?? 0,
            `${sourceKindLabel[c.source.type]} · ${sourceLabel(s, c.source)}`,
            c.source.type === SourceType.SourceLocal,
          ),
        )}
      </ul>
      {s.collections.length === 0 && !collapsed.has("集合") && <p className="hint pad">技能入库后，按来源自动归入集合。</p>}

      <SectionHead name="组" count={s.groups.length} collapsed={collapsed} onToggle={setCollapsed}>
        <button type="button" className="icon-btn sm sec-add" title="新建组" onClick={() => createGroup(ui)}>
          <Icon name="plus" />
        </button>
      </SectionHead>
      <ul className={`nav sec-list ${collapsed.has("组") ? "hidden" : ""}`}>
        {s.groups.map((g) => {
          const target: SkillFilter = { kind: "group", groupID: g.group.id };
          const already = !!dnd.item && alreadyInGroup(s, dnd.item, g.group.id);
          const canDrop = dnd.item?.kind === "skill" && !already;
          return (
            <li key={g.group.id}>
              <div
                role="button"
                tabIndex={0}
                className={`nav-item ${sameFilter(filter, target) ? "active" : ""} ${already ? "already" : ""}`}
                onClick={() => pick(target)}
                draggable
                onDragStart={(e) => dnd.start(e, { kind: "group", id: g.group.id }, `组「${g.group.name}」· ${g.members?.length ?? 0} 个技能`)}
                onDragEnd={dnd.end}
                data-drop-group={g.group.id}
                {...dropProps(canDrop, () => dnd.dropOnGroup(g.group.id))}
                title="点击筛选;拖到右侧作用域 = 组安装;把技能卡片拖到这里 = 加入本组"
              >
                <span className="grow ellipsis">{g.group.name}</span>
                <span className="row-actions">
                  <button
                    type="button"
                    className="icon-btn sm"
                    title="重命名"
                    onClick={(e) => {
                      e.stopPropagation();
                      void renameGroup(ui, s, g.group.id);
                    }}
                  >
                    <Icon name="edit" />
                  </button>
                  <button
                    type="button"
                    className="icon-btn sm"
                    title="删除组"
                    onClick={(e) => {
                      e.stopPropagation();
                      void deleteGroup(ui, s, g.group.id);
                    }}
                  >
                    <Icon name="close" />
                  </button>
                </span>
                <Count value={g.members?.length ?? 0} />
              </div>
            </li>
          );
        })}
      </ul>
      {s.groups.length === 0 && !collapsed.has("组") && <p className="hint pad">还没有组。按场景把技能归到一起，就能整组安装。</p>}

      <div className="grow" />
      <ul className="nav">
        <li>
          <button type="button" className="nav-item" onClick={() => ui.open((close) => <SyncDialog config={config} onClose={close} />)}>
            <Icon name="cloud" />
            <span className="grow">同步</span>
            <span className="muted tiny">{config.sync ? "手动" : "未配置"}</span>
          </button>
        </li>
        <li>
          <button
            type="button"
            className="nav-item"
            onClick={() => ui.open((close) => <SettingsDialog s={s} config={config} onClose={close} />)}
          >
            <Icon name="gear" />
            <span className="grow">设置</span>
          </button>
        </li>
      </ul>
      <div className="lib-path" title={s.info.library_path}>
        <Icon name="database" />
        <span className="grow ellipsis ellipsis-start mono">{`\u200e${shortPath(s, s.info.library_path)}\u200e`}</span>
      </div>
    </aside>
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
    <div className={`nav-sec sec-head ${closed ? "closed" : ""}`}>
      <button type="button" className="sec-toggle" aria-expanded={!closed} onClick={() => onToggle((set) => toggled(set, name))}>
        <Icon name="down" />
        <span>{name}</span>
        <span className="sec-n">{count}</span>
      </button>
      {children}
    </div>
  );
}
