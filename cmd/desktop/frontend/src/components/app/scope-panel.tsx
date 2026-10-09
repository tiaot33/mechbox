// 右栏：作用域——全局与每个已登记项目下，只展开装有技能的 Agent,其余折进"其他 N 个 Agent";
// 项目行折叠时用小图标显示在用的 Agent。点选一行即选中该作用域(与顶栏胶囊同步)。
// 作用域行接受拖放：技能 = 直接安装，组 = 组安装;拖到折叠的项目或"其他 Agent"上停 0.5 秒自动展开。
import { useRef, useState } from "react";
import type { Agent, RegisteredProject } from "@bindings/internal/library";
import { registerProject, relocateProject, unregisterProject } from "@/lib/actions";
import { alreadyInScope, dropProps, useDnD } from "@/lib/dnd";
import type { Snapshot } from "@/lib/library";
import {
  agentsUsedIn,
  needsReinstall,
  parseScope,
  placeOf,
  projectName,
  scopeKey,
  scopeOf,
  shortPath,
  skillScopesIn,
  type Place,
  type ScopeKey,
} from "@/lib/model";
import { resolveLocalPath } from "@/lib/paths";
import { Modal, useUI } from "@/lib/ui";
import { Icon } from "./icons";
import { pickDirectory } from "./fetch-dialog";
import { AgentIcon, Count } from "./parts";

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

  const scopeRow = (agent: Agent, place: Place) => {
    const key = scopeKey(scopeOf(agent.code, place));
    const installed = skillScopesIn(s, key);
    const issues = installed.filter(needsReinstall).length;
    const already = !!dnd.item && alreadyInScope(s, dnd.item, key);
    return (
      <li key={key}>
        <button
          type="button"
          className={`nav-item sub ${scope === key ? "active" : ""} ${already ? "already" : ""}`}
          onClick={() => onScope(scope === key ? null : key)}
          data-drop-scope={key}
          {...dropProps(!!dnd.item && !already, () => dnd.dropOnScope(key))}
        >
          <AgentIcon code={agent.code} name={agent.name} />
          <span className="grow ellipsis">
            {agent.name}
            {issues > 0 && <i className="issue-dot" title={`${issues} 个技能文件夹与当前版本不一致，需要重新安装`} />}
          </span>
          <Count value={installed.length} />
        </button>
      </li>
    );
  };

  // 每个地方只展开"用到的 Agent"(装有技能，或正被选中);其余折进"其他 N 个 Agent",展开后淡色显示在开关之上。
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
        {more && rest.map((a) => scopeRow(a, place))}
        {rest.length > 0 && (
          <li>
            <button
              type="button"
              className={`nav-item sub more ${more ? "open" : ""}`}
              onClick={() => onToggleMore(place)}
              {...springProps(!more, () => onToggleMore(place))}
            >
              <Icon name="down" />
              <span className="grow">{more ? "收起" : `其他 ${rest.length} 个 Agent`}</span>
            </button>
          </li>
        )}
      </>
    );
  };

  const register = () => ui.open((close) => <ProjectPathDialog home={s.info.home_dir} onClose={close} />);
  const relocate = (project: RegisteredProject) =>
    ui.open((close) => <ProjectPathDialog home={s.info.home_dir} project={project} onClose={close} />);

  return (
    <aside className="a-right">
      <div className="nav-sec rail-head">
        <span>
          <Icon name="globe" /> 全局
        </span>
      </div>
      <ul className="nav tree">{agentRows(0)}</ul>
      <div className="nav-sec rail-head">
        <span>
          <Icon name="folder" /> 已登记项目
        </span>
        <button type="button" className="icon-btn sm sec-add" title="登记项目" onClick={register}>
          <Icon name="plus" />
        </button>
      </div>
      <ul className="nav tree">
        {s.projects.map((p) => {
          const open = expanded.has(p.id);
          const count = s.skillScopes.filter((x) => placeOf(x.scope) === p.id).length;
          return (
            <li key={p.id}>
              <div
                role="button"
                tabIndex={0}
                className={`nav-item proj ${open ? "open" : ""} ${selected && placeOf(selected) === p.id ? "current" : ""}`}
                onClick={() => onToggleProject(p.id)}
                title={p.missing ? `${shortPath(s, p.path)}\n不在这台机器上` : shortPath(s, p.path)}
                {...springProps(!open, () => onToggleProject(p.id))}
              >
                <Icon name="folder" />
                <span className="grow ellipsis">{projectName(s, p.id)}</span>
                {p.missing && (
                  <span className="badge warn" title="项目目录在这台机器上不存在;可以改项目位置">
                    不在这台机器上
                  </span>
                )}
                {!open && (
                  <span className="marks">
                    {agentsUsedIn(s, p.id).map((a) => (
                      <AgentIcon key={a.code} code={a.code} name={a.name} />
                    ))}
                  </span>
                )}
                <span className="row-actions">
                  <button
                    type="button"
                    className="icon-btn sm"
                    title="改项目位置"
                    onClick={(e) => {
                      e.stopPropagation();
                      void relocate(p);
                    }}
                  >
                    <Icon name="edit" />
                  </button>
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
              {open && <ul className="nav tree">{agentRows(p.id)}</ul>}
            </li>
          );
        })}
      </ul>
      {s.projects.length === 0 && <p className="hint pad">还没有登记项目。登记后可以把技能安装到项目级目录。</p>}
    </aside>
  );
}

// ProjectPathDialog 登记一个项目目录，或给出 project 时改它的位置:可直接输入路径，也可用系统目录选择框。
// 设置页的项目分区也用它。
export function ProjectPathDialog({
  home,
  project,
  onClose,
}: {
  home: string;
  project?: RegisteredProject;
  onClose: () => void;
}) {
  const ui = useUI();
  const [path, setPath] = useState(project?.path ?? "");
  const [working, setWorking] = useState(false);
  const resolved = resolveLocalPath(path, home);
  const submit = async () => {
    if (!resolved?.path) return;
    setWorking(true);
    const ok = project ? await relocateProject(ui, project, resolved.path) : await registerProject(ui, resolved.path);
    setWorking(false);
    if (ok) onClose();
  };
  return (
    <Modal
      title={project ? `改项目「${project.name}」的位置` : "登记项目"}
      onClose={onClose}
      footer={
        <>
          <button type="button" className="pill-btn" onClick={onClose}>
            取消
          </button>
          <button type="button" className="pill-btn accent" disabled={!resolved?.path || working} onClick={submit}>
            {working ? <span className="spin" /> : null}
            {project ? "改位置" : "登记"}
          </button>
        </>
      }
    >
      <p className="hint">
        {project
          ? "把项目指到本机上的目录(从别的机器同步过来，或项目目录被移动、改名之后)。直接安装与组安装都保留;旧位置的技能文件夹不搬动，技能库从此不再管。"
          : "登记后，这个项目与每个 Agent 组成项目级作用域，技能可以安装到项目里的 Agent 目录。"}
      </p>
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
            const dir = await pickDirectory(project ? "选择项目在这台机器上的目录" : "选择要登记的项目目录");
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
