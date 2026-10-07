// 中间：作用域摘要(选中作用域时)、组标题条(筛选组时)、更新条(筛选"有可用更新"时)与技能卡片 / 列表。
// 卡片点开详情抽屉、可拖到左栏组或右栏作用域;选中作用域时卡片上的开关控制直接安装。
import type { GroupWithMembers, SkillSummary } from "@bindings/internal/library";
import {
  deleteGroup,
  installGroup,
  installSkill,
  reinstall,
  renameGroup,
  uninstallGroup,
  uninstallSkill,
  updateAll,
} from "@/lib/actions";
import { useDnD } from "@/lib/dnd";
import type { Snapshot } from "@/lib/library";
import {
  agentName,
  groupStateIn,
  needsReinstall,
  parseScope,
  placeOf,
  projectName,
  scopeKey,
  scopeLabel,
  shortPath,
  skillScopeIn,
  skillScopeStatus,
  skillScopesIn,
  skillScopesOf,
  sourceKindLabel,
  sourceLabel,
  sourceShort,
  updateAvailable,
  viaGroupCount,
  type ScopeKey,
  type SkillFilter,
} from "@/lib/model";
import { useUI } from "@/lib/ui";
import { Icon } from "./icons";
import { AgentIcon, AgentMark } from "./parts";
import type { ListView } from "./toolbar";

interface SkillListProps {
  s: Snapshot;
  skills: SkillSummary[];
  filter: SkillFilter;
  scope: ScopeKey | null;
  scopeDirectory: string | undefined;
  view: ListView;
  selected: number | null;
  onOpen: (id: number) => void;
  onFetch: () => void;
}

export function SkillList(props: SkillListProps) {
  const { s, skills, filter, scope, view } = props;
  const group = filter.kind === "group" ? s.groups.find((g) => g.group.id === filter.groupID) : undefined;
  return (
    <main className="a-center tile">
      {scope && <ScopeSummary s={s} scope={scope} directory={props.scopeDirectory} />}
      {group && <GroupHead s={s} group={group} scope={scope} />}
      {filter.kind === "updates" && skills.length > 0 && <UpdatesHead s={s} skills={skills} />}
      {/* 网格放在单独的滚动容器里：网格高度随内容，grid-auto-rows: 1fr 才等于最高那一行，而不是平分可视高度 */}
      <div className="a-scroll">
        <div className={`a-list is-${view}`}>
          {skills.map((skill) => (
            <SkillCard
              key={skill.id}
              s={s}
              skill={skill}
              scope={scope}
              selected={props.selected === skill.id}
              hideGroup={group?.group.id}
              onOpen={() => props.onOpen(skill.id)}
            />
          ))}
          {skills.length === 0 &&
            (s.skills.length === 0 ? (
              <div className="list-empty">
                <b>技能库还是空的</b>
                <span>从本地目录、GitHub 或技能市场把技能入库。</span>
                <button type="button" className="pill-btn accent" onClick={props.onFetch}>
                  <Icon name="plus" />
                  入库技能
                </button>
              </div>
            ) : (
              <div className="list-empty">没有匹配的技能</div>
            ))}
        </div>
      </div>
    </main>
  );
}

function SkillCard(props: {
  s: Snapshot;
  skill: SkillSummary;
  scope: ScopeKey | null;
  selected: boolean;
  hideGroup?: number; // 正在筛选的组：每张卡都属于它，不再重复显示
  onOpen: () => void;
}) {
  const { s, skill, scope } = props;
  const ui = useUI();
  const dnd = useDnD();
  const installed = skillScopesOf(s, skill.id);
  const globals = installed.filter((p) => placeOf(p.scope) === 0).map((p) => p.scope.agent);
  const projects = new Set(installed.filter((p) => placeOf(p.scope) !== 0).map((p) => placeOf(p.scope))).size;
  const bad = installed.some(needsReinstall);
  const here = scope ? skillScopeIn(s, skill.id, scope) : undefined;
  const groups = s.groups.filter((g) => g.members?.includes(skill.id) && g.group.id !== props.hideGroup);
  const via = (here?.groups ?? []).map((id) => s.groups.find((g) => g.group.id === id)?.group.name ?? "");

  const toggle = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!scope) return;
    const sc = parseScope(scope);
    void (here?.direct ? uninstallSkill(ui, s, skill.id, sc) : installSkill(ui, s, skill.id, sc));
  };

  return (
    <article
      className={`a-card ${props.selected ? "selected" : ""}`}
      onClick={props.onOpen}
      draggable
      onDragStart={(e) => dnd.start(e, { kind: "skill", id: skill.id }, skill.name)}
      onDragEnd={dnd.end}
    >
      <div className="a-card-top">
        <span className="a-card-name">{skill.name}</span>
        {updateAvailable(skill) && (
          <span className="badge warn" title="来源有新版本">
            可更新
          </span>
        )}
        {/* 右上角：所属组(灰色小标签，多个时显示 +N),其后是淡色的来源。 */}
        {groups.length > 0 && (
          <span className="a-card-tag" title={`组:${groups.map((g) => g.group.name).join("、")}`}>
            {groups[0].group.name}
            {groups.length > 1 && ` +${groups.length - 1}`}
          </span>
        )}
        <span className="a-card-src" title={`${sourceKindLabel[skill.source.type]} · ${sourceLabel(s, skill.source)}`}>
          {sourceShort(s, skill.source)}
        </span>
      </div>
      <p className="a-card-desc">{skill.description}</p>
      <div className="a-card-foot">
        {scope ? (
          <>
            <button
              type="button"
              className={`switch ${here?.direct ? "on" : ""} ${here && !here.direct ? "group-held" : ""}`}
              role="switch"
              aria-checked={!!here?.direct}
              onClick={toggle}
              title={here?.direct ? "卸载直接安装" : "直接安装到这个作用域"}
            >
              <i />
            </button>
            <span className="tiny">
              {here
                ? [here.direct ? "直接安装" : "", via.length ? `随「${via.join("」「")}」安装` : ""].filter(Boolean).join(" + ")
                : "未安装"}
            </span>
            {here && needsReinstall(here) && (
              <span className={`status ${skillScopeStatus[here.status].tone}`}>{skillScopeStatus[here.status].label}</span>
            )}
          </>
        ) : (
          <>
            <span className={`dot ${installed.length ? (bad ? "warn" : "on") : ""}`} />
            <span className="tiny">{globals.length ? "全局" : "未全局安装"}</span>
            {globals.length > 0 && (
              <span className="marks" title={globals.map((code) => agentName(s, code)).join("、")}>
                {globals.slice(0, 4).map((code) => (
                  <AgentIcon key={code} code={code} name={agentName(s, code)} />
                ))}
                {globals.length > 4 && <span className="tiny muted">+{globals.length - 4}</span>}
              </span>
            )}
            <span className="grow" />
            <span className="tiny muted">{projects} 个项目</span>
          </>
        )}
      </div>
    </article>
  );
}

// ScopeSummary 是选中作用域时列表顶部的摘要:Agent 目录、健康状态、全部重新安装，以及每个组的整组开关。
function ScopeSummary({ s, scope, directory }: { s: Snapshot; scope: ScopeKey; directory: string | undefined }) {
  const ui = useUI();
  const installed = skillScopesIn(s, scope);
  const issues = installed.filter(needsReinstall).length;
  const sc = parseScope(scope);
  return (
    <div className="a-scope-sum">
      <div className="a-sum-row">
        <span className="tiny muted">Agent 目录</span>
        <span className="mono tiny ellipsis" style={{ userSelect: "text" }}>
          {directory ? shortPath(s, directory) : "…"}
        </span>
        <span className="grow" />
        <span className="stat">
          <b>{installed.length}</b> 已安装
        </span>
        <span className={`stat ${issues ? "warn" : ""}`}>
          <b>{issues}</b> 需重新安装
        </span>
        {issues > 0 && (
          <button type="button" className="pill-btn sm" onClick={() => reinstall(ui, s, { scope: sc }, scopeLabel(s, sc))}>
            <Icon name="refresh" />
            全部重新安装
          </button>
        )}
      </div>
      {s.groups.length > 0 && (
        <div className="a-sum-groups">
          <span className="tiny muted">组</span>
          {s.groups.map((g) => {
            const state = groupStateIn(s, g, scope);
            return (
              <button
                type="button"
                key={g.group.id}
                className={`gchip ${state}`}
                onClick={() =>
                  state === "on" ? uninstallGroup(ui, s, g.group.id, sc) : installGroup(ui, s, g.group.id, sc)
                }
                title={`「${g.group.name}」${state === "on" ? "已组安装，点击卸载(只撤回本组的组安装)" : "未组安装，点击组安装"}`}
              >
                <i />
                {g.group.name}
                <span className="tiny">
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

// GroupHead 是筛选某个组时列表顶部的组标题条：整组的拖动把手，附带重命名、删除与组安装到当前作用域。
function GroupHead({ s, group, scope }: { s: Snapshot; group: GroupWithMembers; scope: ScopeKey | null }) {
  const ui = useUI();
  const dnd = useDnD();
  const scopes = s.groupInstalls.filter((a) => a.group_id === group.group.id).map((a) => a.scope);
  const count = group.members?.length ?? 0;
  return (
    <div
      className="a-group-head"
      draggable
      onDragStart={(e) => dnd.start(e, { kind: "group", id: group.group.id }, `组「${group.group.name}」· ${count} 个技能`)}
      onDragEnd={dnd.end}
      title={`拖到右侧作用域 = 组安装(${count} 个技能)`}
    >
      <span className="grip">
        <i />
        <i />
        <i />
        <i />
        <i />
        <i />
      </span>
      <Icon name="layers" />
      <b>{group.group.name}</b>
      <span className="tiny muted">{count} 个技能</span>
      <span className="a-group-on">
        {scopes.length ? (
          <>
            组安装于
            {scopes.map((sc) => (
              <span key={scopeKey(sc)} className="chip sm">
                <AgentMark code={sc.agent} name={agentName(s, sc.agent)} />
                {sc.project_id ? projectName(s, sc.project_id) : "全局"}
                <button
                  type="button"
                  title="卸载这个作用域的组安装"
                  onClick={() => uninstallGroup(ui, s, group.group.id, sc)}
                >
                  ×
                </button>
              </span>
            ))}
          </>
        ) : (
          <span className="tiny muted">尚未组安装到任何作用域</span>
        )}
      </span>
      <span className="card-actions">
        {scope && !scopes.some((sc) => scopeKey(sc) === scope) && (
          <button type="button" className="pill-btn sm" onClick={() => installGroup(ui, s, group.group.id, parseScope(scope))}>
            组安装到 {scopeLabel(s, parseScope(scope))}
          </button>
        )}
        <button type="button" className="pill-btn sm" onClick={() => renameGroup(ui, s, group.group.id)}>
          重命名
        </button>
        <button type="button" className="pill-btn sm danger" onClick={() => deleteGroup(ui, s, group.group.id)}>
          删除组
        </button>
      </span>
    </div>
  );
}

// UpdatesHead 是筛选"有可用更新"时的操作条：一次更新全部。
function UpdatesHead({ s, skills }: { s: Snapshot; skills: SkillSummary[] }) {
  const ui = useUI();
  return (
    <div className="a-scope-sum">
      <div className="a-sum-row">
        <span>
          <b>{skills.length}</b> 个技能的来源有新版本
        </span>
        <span className="grow" />
        <button type="button" className="pill-btn sm accent" disabled={ui.busy} onClick={() => updateAll(ui, s, skills)}>
          <Icon name="up" />
          全部更新
        </button>
      </div>
      <span className="tiny muted">更新只改技能库里的版本，已安装的文件夹需要在作用域里"全部重新安装"才会跟上。</span>
    </div>
  );
}
