// 中间：作用域摘要(选中作用域时)、组标题条(筛选组时)与技能卡片 / 列表。
// 激活开关、整组开关、全部重写照原型摆出，只展示状态、暂不可用;详情抽屉与拖放不在骨架里。
import type { GroupWithMembers, SkillSummary } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import {
  agentName,
  groupStateIn,
  needsRewrite,
  placeOf,
  projectName,
  projectionIn,
  projectionStatus,
  projectionsIn,
  projectionsOf,
  scopeKey,
  shortPath,
  sourceKindLabel,
  sourceLabel,
  sourceShort,
  updateAvailable,
  viaGroupCount,
  type ScopeKey,
  type SkillFilter,
} from "@/lib/model";
import { Icon } from "./icons";
import { AgentMark, LATER, Switch } from "./parts";
import type { ListView } from "./toolbar";

interface SkillListProps {
  s: Snapshot;
  skills: SkillSummary[];
  filter: SkillFilter;
  scope: ScopeKey | null;
  scopeDirectory: string | undefined;
  view: ListView;
}

export function SkillList({ s, skills, filter, scope, scopeDirectory, view }: SkillListProps) {
  const group = filter.kind === "group" ? s.groups.find((g) => g.group.id === filter.groupID) : undefined;
  return (
    <main className="a-center tile">
      {scope && <ScopeSummary s={s} scope={scope} directory={scopeDirectory} />}
      {group && <GroupHead s={s} group={group} />}
      <div className={`a-list ${view}`}>
        {skills.map((skill) => (
          <SkillCard key={skill.id} s={s} skill={skill} scope={scope} view={view} />
        ))}
        {skills.length === 0 &&
          (s.skills.length === 0 ? (
            <div className="list-empty">
              <b>技能库还是空的</b>
              <span>安装技能后，它们会出现在这里。</span>
            </div>
          ) : (
            <div className="list-empty">没有匹配的技能</div>
          ))}
      </div>
    </main>
  );
}

function SkillCard({ s, skill, scope, view }: { s: Snapshot; skill: SkillSummary; scope: ScopeKey | null; view: ListView }) {
  const projections = projectionsOf(s, skill.id);
  const globals = projections.filter((p) => placeOf(p.scope) === 0).map((p) => p.scope.agent);
  const projects = new Set(projections.filter((p) => placeOf(p.scope) !== 0).map((p) => placeOf(p.scope))).size;
  const bad = projections.some(needsRewrite);
  const here = scope ? projectionIn(s, skill.id, scope) : undefined;
  const groups = s.groups.filter((g) => g.members?.includes(skill.id));
  const via = (here?.groups ?? []).map((id) => s.groups.find((g) => g.group.id === id)?.group.name ?? "");

  return (
    <article className="a-card">
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
            <Switch on={!!here?.direct} title={`独立激活 · ${LATER}`} />
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
  const projections = projectionsIn(s, scope);
  const issues = projections.filter(needsRewrite).length;
  return (
    <div className="a-scope-sum">
      <div className="a-sum-row">
        <span className="tiny muted">投影目录</span>
        <span className="mono tiny ellipsis">{directory ? shortPath(s, directory) : "…"}</span>
        <span className="grow" />
        <span className="stat">
          <b>{projections.length}</b> 已激活
        </span>
        <span className={`stat ${issues ? "warn" : ""}`}>
          <b>{issues}</b> 需重写
        </span>
        {issues > 0 && (
          <button type="button" className="pill-btn sm" disabled title={LATER}>
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
                disabled
                title={`「${g.group.name}」${state === "on" ? "已整组激活" : "未整组激活"} · 整组开关${LATER}`}
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

// GroupHead 是筛选某个组时列表顶部的组标题条：成员数与已整组激活的作用域。
function GroupHead({ s, group }: { s: Snapshot; group: GroupWithMembers }) {
  const scopes = s.groupActivations.filter((a) => a.group_id === group.group.id).map((a) => a.scope);
  return (
    <div className="a-group-head">
      <Icon name="layers" />
      <b>{group.group.name}</b>
      <span className="tiny muted">{group.members?.length ?? 0} 个技能</span>
      <span className="a-group-on">
        {scopes.length ? (
          <>
            整组激活于
            {scopes.map((sc) => (
              <span key={scopeKey(sc)} className="chip sm">
                <AgentMark code={sc.agent} name={agentName(s, sc.agent)} />
                {sc.project_id ? projectName(s, sc.project_id) : "全局"}
              </span>
            ))}
          </>
        ) : (
          <span className="tiny muted">尚未整组激活到任何作用域</span>
        )}
      </span>
    </div>
  );
}
