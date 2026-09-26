// 右栏：作用域——全局与每个已登记项目下，只展开有投影的 Agent,其余折进"其他 N 个 Agent";
// 项目行折叠时用小图标显示在用的 Agent。点选一行即选中该作用域(与顶栏胶囊同步)。
import { Fragment } from "react";
import type { Agent } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import {
  agentsUsedIn,
  needsRewrite,
  parseScope,
  placeOf,
  projectName,
  projectionsIn,
  scopeKey,
  scopeOf,
  shortPath,
  type Place,
  type ScopeKey,
} from "@/lib/model";
import { Icon } from "./icons";
import { AgentMark, Count, LATER } from "./parts";

interface ScopePanelProps {
  s: Snapshot;
  scope: ScopeKey | null;
  expanded: Set<Place>;
  moreAgents: Set<Place>;
  onScope: (key: ScopeKey | null) => void;
  onToggleProject: (id: Place) => void;
  onToggleMore: (place: Place) => void;
}

export function ScopePanel({ s, scope, expanded, moreAgents, onScope, onToggleProject, onToggleMore }: ScopePanelProps) {
  const selected = scope ? parseScope(scope) : null;

  const scopeRow = (agent: Agent, place: Place, dim = false) => {
    const key = scopeKey(scopeOf(agent.code, place));
    const projections = projectionsIn(s, key);
    const issues = projections.filter(needsRewrite).length;
    return (
      <li key={key}>
        <button
          type="button"
          className={`nav-item sub ${dim ? "dim" : ""} ${scope === key ? "active" : ""}`}
          onClick={() => onScope(scope === key ? null : key)}
        >
          <AgentMark code={agent.code} name={agent.name} />
          <span className="grow">{agent.name}</span>
          {issues > 0 && (
            <span className="status warn" title={`${issues} 条投影与当前版本不一致`}>
              {issues}
            </span>
          )}
          {projections.length > 0 && <Count value={projections.length} />}
        </button>
      </li>
    );
  };

  // 每个地方只展开"用到的 Agent"(有投影，或正被选中);其余折进"其他 N 个 Agent",展开后淡色显示。
  const agentRows = (place: Place) => {
    const used = agentsUsedIn(s, place);
    const shown = s.agents.filter(
      (a) => used.includes(a) || (selected?.agent === a.code && placeOf(selected) === place),
    );
    const rest = s.agents.filter((a) => !shown.includes(a));
    const more = moreAgents.has(place);
    return (
      <>
        {shown.map((a) => scopeRow(a, place))}
        {rest.length > 0 && (
          <li>
            <button type="button" className="nav-item sub more" onClick={() => onToggleMore(place)}>
              <Icon name="plus" />
              <span className="grow">{more ? "收起" : `其他 ${rest.length} 个 Agent`}</span>
            </button>
          </li>
        )}
        {more && rest.map((a) => scopeRow(a, place, true))}
      </>
    );
  };

  return (
    <aside className="a-right">
      <div className="nav-sec">
        <span>
          <Icon name="globe" /> 全局
        </span>
      </div>
      <ul className="nav">{agentRows(0)}</ul>
      <div className="nav-sec">
        <span>
          <Icon name="folder" /> 已登记项目
        </span>
        <button type="button" className="icon-btn sm" disabled title={`登记项目 · ${LATER}`}>
          <Icon name="plus" />
        </button>
      </div>
      <ul className="nav">
        {s.projects.map((p) => {
          const open = expanded.has(p.id);
          const count = s.projections.filter((x) => placeOf(x.scope) === p.id).length;
          return (
            <Fragment key={p.id}>
              <li>
              <button
                type="button"
                className={`nav-item proj ${open ? "open" : ""}`}
                onClick={() => onToggleProject(p.id)}
                title={shortPath(s, p.path)}
              >
                <Icon name="folder" />
                <span className="grow ellipsis">{projectName(s, p.id)}</span>
                {!open && (
                  <span className="marks">
                    {agentsUsedIn(s, p.id).map((a) => (
                      <AgentMark key={a.code} code={a.code} name={a.name} />
                    ))}
                  </span>
                )}
                <Count value={count} />
                <span className="chev">
                  <Icon name="chev" />
                </span>
              </button>
              </li>
              {open && agentRows(p.id)}
            </Fragment>
          );
        })}
      </ul>
      {s.projects.length === 0 && <p className="hint pad">还没有登记项目。</p>}
      <p className="hint pad">点选一个作用域，卡片上会显示它在该作用域的激活状态。激活、停用与拖放{LATER}。</p>
    </aside>
  );
}
