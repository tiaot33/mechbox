// 左栏：安装 / 市场入口;筛选——所有技能 / 有可用更新 / 集合(按来源)/ 组;底部同步与设置。
// 组行可拖到右栏作用域(整组激活),也接受技能卡片(加入该组)。
import type { ConfigStatus } from "@bindings/cmd/desktop";
import { checkUpdates, createGroup, deleteGroup, renameGroup } from "@/lib/actions";
import { alreadyInGroup, dropProps, useDnD } from "@/lib/dnd";
import type { Snapshot } from "@/lib/library";
import { sameFilter, shortPath, sourceKindLabel, sourceLabel, updateAvailable, type SkillFilter } from "@/lib/model";
import { useUI } from "@/lib/ui";
import { Icon, SourceIcon } from "./icons";
import { InstallDialog } from "./install-dialog";
import { MarketDialog } from "./market-dialog";
import { Count } from "./parts";
import { SettingsDialog } from "./settings-dialog";
import { SyncDialog } from "./sync-dialog";

interface FilterSidebarProps {
  s: Snapshot;
  config: ConfigStatus;
  filter: SkillFilter;
  onFilter: (filter: SkillFilter) => void;
}

export function FilterSidebar({ s, config, filter, onFilter }: FilterSidebarProps) {
  const ui = useUI();
  const dnd = useDnD();
  const updates = s.skills.filter(updateAvailable).length;
  // 再点一次当前筛选回到"所有技能",与原型一致。
  const pick = (target: SkillFilter) => onFilter(sameFilter(filter, target) ? { kind: "all" } : target);
  const row = (target: SkillFilter, icon: React.ReactNode, label: string, count: number, title?: string) => (
    <li key={JSON.stringify(target)}>
      <button
        type="button"
        className={`nav-item ${sameFilter(filter, target) ? "active" : ""}`}
        onClick={() => pick(target)}
        title={title}
      >
        {icon}
        <span className="grow ellipsis">{label}</span>
        <Count value={count} />
      </button>
    </li>
  );

  return (
    <aside className="a-left">
      <button
        type="button"
        className="side-btn"
        onClick={() => ui.open((close) => <InstallDialog config={config} home={s.info.home_dir} onClose={close} />)}
      >
        <Icon name="plus" />
        <span>安装技能</span>
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

      <div className="nav-sec">
        <span>集合</span>
        <span className="muted tiny">按来源</span>
      </div>
      <ul className="nav">
        {s.collections.map((c) =>
          row(
            { kind: "collection", sourceID: c.source.id },
            <SourceIcon type={c.source.type} />,
            sourceLabel(s, c.source),
            c.skills?.length ?? 0,
            `${sourceKindLabel[c.source.type]} · ${sourceLabel(s, c.source)}`,
          ),
        )}
      </ul>
      {s.collections.length === 0 && <p className="hint pad">安装技能后，按来源自动归入集合。</p>}

      <div className="nav-sec">
        <span>组</span>
        <button type="button" className="icon-btn sm" title="新建组" onClick={() => createGroup(ui)}>
          <Icon name="plus" />
        </button>
      </div>
      <ul className="nav">
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
                title="点击筛选;拖到右侧作用域 = 整组激活;把技能卡片拖到这里 = 加入本组"
              >
                <Icon name="layers" />
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
                    ✎
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
      {s.groups.length === 0 && <p className="hint pad">还没有组。按场景把技能归到一起，就能整组激活。</p>}

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
      {/* rtl 截断让省略号出现在开头、保留文件名;两端的 LRM 防止路径开头的符号被双向算法挪到末尾。 */}
      <div className="lib-path" title={s.info.library_path}>
        <Icon name="database" />
        <span className="grow ellipsis mono">{`\u200e${shortPath(s, s.info.library_path)}\u200e`}</span>
      </div>
    </aside>
  );
}
