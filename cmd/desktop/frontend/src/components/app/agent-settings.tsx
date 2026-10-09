// 设置页的 Agent 分区：每个 Agent 的显示勾选(存在这台机器的应用设置里),以及自定义 Agent 的添加、编辑、删除。
// 内置 Agent 的名称和目录由应用维护，只能勾选显示。
import { useState } from "react";
import { ErrorCode, type Agent, type CustomAgent } from "@bindings/internal/library";
import { addAgent, deleteAgent, editAgent } from "@/lib/actions";
import { describeError } from "@/lib/errors";
import type { LibraryError, Snapshot } from "@/lib/library";
import { agentShown, shortPath } from "@/lib/model";
import { resolveLocalPath } from "@/lib/paths";
import { Modal, useUI } from "@/lib/ui";
import { pickDirectory } from "./fetch-dialog";
import { AgentMark } from "./parts";

export function AgentSection({
  s,
  shown,
  onShown,
}: {
  s: Snapshot;
  shown?: Partial<Record<string, boolean>> | null; // 应用设置还没读到时为 undefined,不显示勾选
  onShown: (agent: Agent, on: boolean) => void;
}) {
  const ui = useUI();
  const edit = (agent: Agent) => ui.open((close) => <AgentDialog s={s} agent={agent} onClose={close} />);
  return (
    <>
      <p className="hint">
        勾选的 Agent 出现在选择安装位置的地方。已经装了技能的 Agent 不管勾没勾，装出去的地方在技能详情、更新和巡检里照常列出。
      </p>
      {s.agents.map((a) => {
        const on = agentShown(shown, a);
        return (
          <div key={a.code} className="set-row">
            <AgentMark code={a.code} name={a.name} />
            <div className="grow">
              <div>
                {a.name}{" "}
                {a.custom ? <span className="badge">自定义</span> : <span className="badge">{a.detected ? "检测到" : "未检测到"}</span>}
              </div>
              <div className="mono tiny muted ellipsis" style={{ userSelect: "text" }}>
                {shortPath(s, a.global_directory)} · 项目里 {a.project_directory}
              </div>
            </div>
            {a.custom && (
              <>
                <button type="button" className="pill-btn" onClick={() => void edit(a)}>
                  编辑
                </button>
                <button type="button" className="pill-btn" onClick={() => void deleteAgent(ui, s, a)}>
                  删除
                </button>
              </>
            )}
            {shown !== undefined && (
              <button
                type="button"
                role="switch"
                aria-checked={on}
                aria-label={`显示 ${a.name}`}
                title={on ? "显示" : "不显示"}
                className={`switch ${on ? "on" : ""}`}
                onClick={() => onShown(a, !on)}
              >
                <i />
              </button>
            )}
          </div>
        );
      })}
    </>
  );
}

type Field = keyof CustomAgent;

// AgentDialog 添加一个自定义 Agent,或给出 agent 时编辑它。全局技能目录必须在用户主目录里面。
export function AgentDialog({ s, agent, onClose }: { s: Snapshot; agent?: Agent; onClose: () => void }) {
  const ui = useUI();
  const home = s.info.home_dir;
  const [name, setName] = useState(agent?.name ?? "");
  const [global, setGlobal] = useState(agent ? shortPath(s, agent.global_directory) : "");
  const [project, setProject] = useState(agent?.project_directory ?? "");
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({});
  const [working, setWorking] = useState(false);

  const validate = () => {
    const found: Partial<Record<Field, string>> = {};
    if (!name.trim()) found.name = "填写名称";
    const resolved = resolveLocalPath(global, home);
    if (!resolved) found.global_directory = "填写全局技能目录";
    else if (resolved.path === undefined) found.global_directory = resolved.error;
    else if (!resolved.path.startsWith(home + "/") || resolved.path.replace(/\/+$/, "") === home) {
      found.global_directory = "全局技能目录要在用户主目录里面(不能是主目录本身)";
    }
    const rel = project.trim();
    if (!rel) found.project_directory = "填写项目里的技能目录";
    else if (rel.startsWith("/") || rel.startsWith("~")) found.project_directory = "填相对项目根目录的路径，如 .agents/skills";
    else if (rel.split("/").some((part) => part === "..") || rel.replace(/^\.\/?|\/+$/g, "") === "") {
      found.project_directory = "要在项目根目录里面(不能是项目根本身)";
    }
    return { found, global: resolved?.path ?? "" };
  };

  const submit = async () => {
    const { found, global: globalPath } = validate();
    setErrors(found);
    if (Object.keys(found).length) return;
    const input: CustomAgent = { name: name.trim(), global_directory: globalPath, project_directory: project.trim() };
    // 技能库按字段报的不合法，显示在对应输入框下面。
    const onInvalid = (err: LibraryError) => {
      const field = err.details?.field as Field | undefined;
      if (err.code !== ErrorCode.InvalidArgument || !field || !(field in input)) return false;
      setErrors({ [field]: describeError(err) });
      return true;
    };
    setWorking(true);
    const ok = agent ? await editAgent(ui, s, agent, input, onInvalid) : await addAgent(ui, input, onInvalid);
    setWorking(false);
    if (ok) onClose();
  };

  const error = (field: Field) => errors[field] && <div className="field-error">{errors[field]}</div>;
  return (
    <Modal
      title={agent ? `编辑 Agent「${agent.name}」` : "添加 Agent"}
      onClose={onClose}
      footer={
        <>
          <button type="button" className="pill-btn" onClick={onClose}>
            取消
          </button>
          <button type="button" className="pill-btn accent" disabled={working} onClick={submit}>
            {working ? <span className="spin" /> : null}
            {agent ? "保存" : "添加"}
          </button>
        </>
      }
    >
      <label className="field">
        <span>名称</span>
        <input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} />
        {error("name")}
      </label>
      <label className="field">
        <span>全局技能目录(在用户主目录里面)</span>
        <div className="row">
          <input
            className="input grow"
            placeholder="~/.my-agent/skills"
            value={global}
            onChange={(e) => setGlobal(e.target.value)}
          />
          <button
            type="button"
            className="pill-btn"
            onClick={async () => {
              const dir = await pickDirectory("选择全局技能目录");
              if (dir) setGlobal(shortPath(s, dir));
            }}
          >
            选择…
          </button>
        </div>
        {error("global_directory")}
      </label>
      <label className="field">
        <span>项目里的技能目录(相对项目根目录)</span>
        <input
          className="input"
          placeholder=".my-agent/skills"
          value={project}
          onChange={(e) => setProject(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
        {error("project_directory")}
      </label>
    </Modal>
  );
}
