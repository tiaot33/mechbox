// 中间：作用域摘要(选中作用域时)、组标题条(筛选组时)、更新条(筛选"有可用更新"时)与技能卡片 / 列表。
// 卡片点开详情抽屉、可拖到左栏组或右栏作用域;选中作用域时卡片上的开关控制独立激活。
import type { GroupWithMembers, SkillSummary } from "@bindings/internal/library";
import {
  activateGroup,
  activateSkill,
  deactivateGroup,
  deactivateSkill,
  deleteGroup,
  renameGroup,
  rewrite,
  updateAll,
} from "@/lib/actions";
import { useDnD } from "@/lib/dnd";
import type { Snapshot } from "@/lib/library";
import {
  agentName,
  groupStateIn,
  needsRewrite,
  parseScope,
  placeOf,
  projectName,
  projectionIn,
  projectionStatus,
  projectionsIn,
  projectionsOf,
  scopeKey,
  scopeLabel,
  shortPath,
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
import { AgentMark } from "./parts";
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
  onInstall: () => void;
}

export function SkillList(props: SkillListProps) {
  const { s, skills, filter, scope, view } = props;
  const group = filter.kind === "group" ? s.groups.find((g) => g.group.id === filter.groupID) : undefined;
  return (
    <main className="a-center tile">
      {scope && <ScopeSummary s={s} scope={scope} directory={props.scopeDirectory} />}
      {group && <GroupHead s={s} group={group} scope={scope} />}
      {filter.kind === "updates" && skills.length > 0 && <UpdatesHead s={s} skills={skills} />}
      <div className={`a-list ${view}`}>
        {skills.map((skill) => (
          <SkillCard
            key={skill.id}
            s={s}
            skill={skill}
            scope={scope}
            view={view}
            selected={props.selected === skill.id}
            onOpen={() => props.onOpen(skill.id)}
          />
        ))}
        {skills.length === 0 &&
          (s.skills.length === 0 ? (
            <div className="list-empty">
              <b>技能库还是空的</b>
              <span>从本地目录、GitHub 或技能市场安装技能。</span>
              <button type="button" className="pill-btn accent" onClick={props.onInstall}>
                <Icon name="plus" />
                安装技能
              </button>
            </div>
          ) : (
            <div className="list-empty">没有匹配的技能</div>
          ))}
      </div>
    </main>
  );
}

function SkillCard(props: {
  s: Snapshot;
  skill: SkillSummary;
  scope: ScopeKey | null;
  view: ListView;
  selected: boolean;
  onOpen: () => void;
}) {
  const { s, skill, scope, view } = props;
  const ui = useUI();
  const dnd = useDnD();
  const projections = projectionsOf(s, skill.id);
  const globals = projections.filter((p) => placeOf(p.scope) === 0).map((p) => p.scope.agent);
  const projects = new Set(projections.filter((p) => placeOf(p.scope) !== 0).map((p) => placeOf(p.scope))).size;
  const bad = projections.some(needsRewrite);
  const here = scope ? projectionIn(s, skill.id, scope) : undefined;
  const groups = s.groups.filter((g) => g.members?.includes(skill.id));
  const via = (here?.groups ?? []).map((id) => s.groups.find((g) => g.group.id === id)?.group.name ?? "");

  const toggle = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!scope) return;
    const sc = parseScope(scope);
    void (here?.direct ? deactivateSkill(ui, s, skill.id, sc) : activateSkill(ui, s, skill.id, sc));
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
        <span className="badge" title={`${sourceKindLabel[skill.source.type]} · ${sourceLabel(s, skill.source)}`}>
          {sourceShort(s, skill.source)}
        </span>
      </div>
      <p className="a-card-desc">{skill.description}</p>
      {view === "card" && groups.length > 0 && (
        <div className="a-card-groups">
          {groups.map((g) => (
            <span key={g.group.id}>{g.group.name}</span>
          ))}
        </div>
      )}
      <div className="a-card-foot">
        {scope ? (
          <>
            <button
              type="button"
              className={`switch ${here?.direct ? "on" : ""} ${here && !here.direct ? "group-held" : ""}`}
              role="switch"
              aria-checked={!!here?.direct}
              onClick={toggle}
              title={here?.direct ? "停用独立激活" : "独立激活到这个作用域"}
            >
              <i />
            </button>
            <span className="tiny">
              {here
                ? [here.direct ? "独立激活" : "", via.length ? `经由「${via.join("」「")}」` : ""].filter(Boolean).join(" + ")
                : "未激活"}
            </span>
            {here && needsRewrite(here) && (
              <span className={`status ${projectionStatus[here.status].tone}`}>{projectionStatus[here.status].label}</span>
            )}
          </>
        ) : (
          <>
            <span className={`dot ${projections.length ? (bad ? "warn" : "on") : ""}`} />
            <span className="tiny">{globals.length ? "全局" : "未全局激活"}</span>
            {globals.length > 0 && (
              <span className="marks" title={globals.map((code) => agentName(s, code)).join("、")}>
                {globals.slice(0, 4).map((code) => (
                  <AgentMark key={code} code={code} name={agentName(s, code)} />
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

// ScopeSummary 是选中作用域时列表顶部的摘要：投影目录、健康状态、全部重写，以及每个组的整组开关。
function ScopeSummary({ s, scope, directory }: { s: Snapshot; scope: ScopeKey; directory: string | undefined }) {
  const ui = useUI();
  const projections = projectionsIn(s, scope);
  const issues = projections.filter(needsRewrite).length;
  const sc = parseScope(scope);
  return (
    <div className="a-scope-sum">
      <div className="a-sum-row">
        <span className="tiny muted">投影目录</span>
        <span className="mono tiny ellipsis" style={{ userSelect: "text" }}>
          {directory ? shortPath(s, directory) : "…"}
        </span>
        <span className="grow" />
        <span className="stat">
          <b>{projections.length}</b> 已激活
        </span>
        <span className={`stat ${issues ? "warn" : ""}`}>
          <b>{issues}</b> 需重写
        </span>
        {issues > 0 && (
          <button type="button" className="pill-btn sm" onClick={() => rewrite(ui, s, { scope: sc }, scopeLabel(s, sc))}>
            <Icon name="refresh" />
            全部重写
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
                  state === "on" ? deactivateGroup(ui, s, g.group.id, sc) : activateGroup(ui, s, g.group.id, sc)
                }
                title={`「${g.group.name}」${state === "on" ? "已整组激活，点击停用(只撤回本组的激活)" : "未整组激活，点击整组激活"}`}
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

// GroupHead 是筛选某个组时列表顶部的组标题条：整组的拖动把手，附带重命名、删除与整组激活到当前作用域。
function GroupHead({ s, group, scope }: { s: Snapshot; group: GroupWithMembers; scope: ScopeKey | null }) {
  const ui = useUI();
  const dnd = useDnD();
  const scopes = s.groupActivations.filter((a) => a.group_id === group.group.id).map((a) => a.scope);
  const count = group.members?.length ?? 0;
  return (
    <div
      className="a-group-head"
      draggable
      onDragStart={(e) => dnd.start(e, { kind: "group", id: group.group.id }, `组「${group.group.name}」· ${count} 个技能`)}
      onDragEnd={dnd.end}
      title={`拖到右侧作用域 = 整组激活(${count} 个技能)`}
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
            整组激活于
            {scopes.map((sc) => (
              <span key={scopeKey(sc)} className="chip sm">
                <AgentMark code={sc.agent} name={agentName(s, sc.agent)} />
                {sc.project_id ? projectName(s, sc.project_id) : "全局"}
                <button
                  type="button"
                  title="停用这个作用域的整组激活"
                  onClick={() => deactivateGroup(ui, s, group.group.id, sc)}
                >
                  ×
                </button>
              </span>
            ))}
          </>
        ) : (
          <span className="tiny muted">尚未整组激活到任何作用域</span>
        )}
      </span>
      <span className="card-actions">
        {scope && !scopes.some((sc) => scopeKey(sc) === scope) && (
          <button type="button" className="pill-btn sm" onClick={() => activateGroup(ui, s, group.group.id, parseScope(scope))}>
            整组激活到 {scopeLabel(s, parseScope(scope))}
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
      <span className="tiny muted">更新只改技能库里的版本，已有投影需要在作用域里"全部重写"才会跟上。</span>
    </div>
  );
}
