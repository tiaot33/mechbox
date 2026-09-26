// 左栏：筛选——所有技能 / 有可用更新 / 集合(按来源)/ 组。安装、市场、新建组、同步、设置照原型摆出，暂不可用。
import type { Snapshot } from "@/lib/library";
import { sameFilter, shortPath, sourceKindLabel, sourceLabel, updateAvailable, type SkillFilter } from "@/lib/model";
import { Icon, SourceIcon } from "./icons";
import { Count, LATER } from "./parts";

interface FilterSidebarProps {
  s: Snapshot;
  filter: SkillFilter;
  onFilter: (filter: SkillFilter) => void;
}

export function FilterSidebar({ s, filter, onFilter }: FilterSidebarProps) {
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
      <button type="button" className="side-btn" disabled title={LATER}>
        <Icon name="plus" />
        <span>安装技能</span>
      </button>
      <button type="button" className="side-btn" disabled title={LATER}>
        <Icon name="tag" />
        <span>技能市场</span>
      </button>
      <ul className="nav">
        {row({ kind: "all" }, <Icon name="bolt" />, "所有技能", s.skills.length)}
        {updates > 0 && row({ kind: "updates" }, <Icon name="up" />, "有可用更新", updates)}
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
        <button type="button" className="icon-btn sm" disabled title={`新建分组 · ${LATER}`}>
          <Icon name="plus" />
        </button>
      </div>
      <ul className="nav">
        {s.groups.map((g) => row({ kind: "group", groupID: g.group.id }, <Icon name="layers" />, g.group.name, g.members?.length ?? 0))}
      </ul>
      {s.groups.length === 0 && <p className="hint pad">还没有组。</p>}

      <div className="grow" />
      <ul className="nav">
        <li>
          <button type="button" className="nav-item disabled" disabled title={LATER}>
            <Icon name="cloud" />
            <span className="grow">同步</span>
            <span className="muted tiny">手动</span>
          </button>
        </li>
        <li>
          <button type="button" className="nav-item disabled" disabled title={LATER}>
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
