// 右栏：作用域——全局与每个已登记项目下，只展开有投影的 Agent,其余折进"其他 N 个 Agent";
// 项目行折叠时用小图标显示在用的 Agent。点选一行即选中该作用域(与顶栏胶囊同步)。
// 作用域行接受拖放：技能 = 独立激活，组 = 整组激活;拖到折叠的项目或"其他 Agent"上停 0.5 秒自动展开。
import { Fragment, useRef, useState } from "react";
import type { Agent } from "@bindings/internal/library";
import { registerProject, unregisterProject } from "@/lib/actions";
import { alreadyInScope, dropProps, useDnD } from "@/lib/dnd";
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
import { resolveLocalPath } from "@/lib/paths";
import { Modal, useUI } from "@/lib/ui";
import { Icon } from "./icons";
import { pickDirectory } from "./install-dialog";
import { AgentMark, Count } from "./parts";

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
  const ui = useUI();
  const dnd = useDnD();
  const selected = scope ? parseScope(scope) : null;
  const spring = useRef<number | undefined>(undefined);

  // springProps:拖动中停在折叠的目标上 0.5 秒后展开它。
  const springProps = (active: boolean, open: () => void) =>
    active && dnd.item
      ? {
          "data-spring": "1",
          onDragEnter: () => {
            window.clearTimeout(spring.current);
            spring.current = window.setTimeout(open, 500);
          },
          onDragLeave: () => window.clearTimeout(spring.current),
        }
      : {};

  const scopeRow = (agent: Agent, place: Place, dim = false) => {
    const key = scopeKey(scopeOf(agent.code, place));
    const projections = projectionsIn(s, key);
    const issues = projections.filter(needsRewrite).length;
    const already = !!dnd.item && alreadyInScope(s, dnd.item, key);
    return (
      <li key={key}>
        <button
          type="button"
          className={`nav-item sub ${dim ? "dim" : ""} ${scope === key ? "active" : ""} ${already ? "already" : ""}`}
          onClick={() => onScope(scope === key ? null : key)}
          data-drop-scope={key}
          {...dropProps(!!dnd.item && !already, () => dnd.dropOnScope(key))}
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
            <button
              type="button"
              className="nav-item sub more"
              onClick={() => onToggleMore(place)}
              {...springProps(!more, () => onToggleMore(place))}
            >
              <Icon name="plus" />
              <span className="grow">{more ? "收起" : `其他 ${rest.length} 个 Agent`}</span>
            </button>
          </li>
        )}
        {more && rest.map((a) => scopeRow(a, place, true))}
      </>
    );
  };

  const register = () => ui.open((close) => <RegisterProjectDialog home={s.info.home_dir} onClose={close} />);

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
        <button type="button" className="icon-btn sm" title="登记项目" onClick={register}>
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
                <div
                  role="button"
                  tabIndex={0}
                  className={`nav-item proj ${open ? "open" : ""}`}
                  onClick={() => onToggleProject(p.id)}
                  title={shortPath(s, p.path)}
                  {...springProps(!open, () => onToggleProject(p.id))}
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
                  <span className="row-actions">
                    <button
                      type="button"
                      className="icon-btn sm"
                      title="注销项目"
                      onClick={(e) => {
                        e.stopPropagation();
                        void unregisterProject(ui, s, p);
                      }}
                    >
                      <Icon name="close" />
                    </button>
                  </span>
                  <Count value={count} />
                  <span className="chev">
                    <Icon name="chev" />
                  </span>
                </div>
              </li>
              {open && agentRows(p.id)}
            </Fragment>
          );
        })}
      </ul>
      {s.projects.length === 0 && <p className="hint pad">还没有登记项目。登记后可以把技能激活到项目级目录。</p>}
      <p className="hint pad">
        把技能卡片或组拖到这里即可激活;拖到折叠的项目或"其他 Agent"上停一下会自动展开。点选一个作用域，卡片上会出现它的激活开关。
      </p>
    </aside>
  );
}

// RegisterProjectDialog 登记一个项目目录：可直接输入路径，也可用系统目录选择框。
function RegisterProjectDialog({ home, onClose }: { home: string; onClose: () => void }) {
  const ui = useUI();
  const [path, setPath] = useState("");
  const [working, setWorking] = useState(false);
  const resolved = resolveLocalPath(path, home);
  const submit = async () => {
    if (!resolved?.path) return;
    setWorking(true);
    const ok = await registerProject(ui, resolved.path);
    setWorking(false);
    if (ok) onClose();
  };
  return (
    <Modal
      title="登记项目"
      onClose={onClose}
      footer={
        <>
          <button type="button" className="pill-btn" onClick={onClose}>
            取消
          </button>
          <button type="button" className="pill-btn accent" disabled={!resolved?.path || working} onClick={submit}>
            {working ? <span className="spin" /> : null}
            登记
          </button>
        </>
      }
    >
      <p className="hint">登记后，这个项目与每个 Agent 组成项目级作用域，技能可以激活到项目里的 Agent 目录。</p>
      <div className="row">
        <input
          className="input grow"
          autoFocus
          placeholder="/path/to/project 或 ~/path/to/project"
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
        <button
          type="button"
          className="pill-btn"
          onClick={async () => {
            const dir = await pickDirectory("选择要登记的项目目录");
            if (dir) setPath(dir);
          }}
        >
          选择…
        </button>
      </div>
      {resolved?.error && <span className="field-error">{resolved.error}</span>}
    </Modal>
  );
}
