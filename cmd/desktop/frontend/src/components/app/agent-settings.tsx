// 设置页的 Agent 分区：每个 Agent 的显示勾选(存在这台机器的应用设置里),以及自定义 Agent 的添加、编辑、删除。
// 内置 Agent 的名称和目录由应用维护，只能勾选显示。
import { useRef, useState } from "react";
import { ErrorCode, type Agent, type CustomAgent } from "@bindings/internal/library";
import { addAgent, deleteAgent, editAgent } from "@/lib/actions";
import { describeError } from "@/lib/errors";
import type { LibraryError, Snapshot } from "@/lib/library";
import { agentShown, shortPath } from "@/lib/model";
import { resolveLocalPath } from "@/lib/paths";
import { Modal, useUI } from "@/lib/ui";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, FieldError, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { pickDirectory } from "./fetch-dialog";
import { AgentMark } from "./parts";
import { SetRow } from "./set-row";

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
      <Hint className="mb-1">
        勾选的 Agent 出现在选择安装位置的地方。已经装了技能的 Agent 不管勾没勾，装出去的地方在技能详情、更新和巡检里照常列出。
      </Hint>
      {s.agents.map((a) => {
        const on = agentShown(shown, a);
        return (
          <SetRow key={a.code}>
            <AgentMark code={a.code} name={a.name} />
            <div className="min-w-0 grow">
              <div className="flex items-center gap-1.5">
                {a.name}
                {a.custom ? <Badge>自定义</Badge> : <Badge>{a.detected ? "检测到" : "未检测到"}</Badge>}
              </div>
              <div className="truncate font-mono text-[11px] text-muted-foreground select-text">
                {shortPath(s, a.global_directory)} · 项目里 {a.project_directory}
              </div>
            </div>
            {a.custom && (
              <>
                <Button onClick={() => void edit(a)}>编辑</Button>
                <Button onClick={() => void deleteAgent(ui, s, a)}>删除</Button>
              </>
            )}
            {shown !== undefined && (
              <Switch checked={on} aria-label={`显示 ${a.name}`} title={on ? "显示" : "不显示"} onCheckedChange={(v) => onShown(a, v)} />
            )}
          </SetRow>
        );
      })}
    </>
  );
}

type AgentField = keyof CustomAgent;

// AgentDialog 添加一个自定义 Agent,或给出 agent 时编辑它。全局技能目录必须在用户主目录里面。
export function AgentDialog({ s, agent, onClose }: { s: Snapshot; agent?: Agent; onClose: () => void }) {
  const ui = useUI();
  const home = s.info.home_dir;
  const [name, setName] = useState(agent?.name ?? "");
  const [global, setGlobal] = useState(agent ? shortPath(s, agent.global_directory) : "");
  const [project, setProject] = useState(agent?.project_directory ?? "");
  const [errors, setErrors] = useState<Partial<Record<AgentField, string>>>({});
  const [working, setWorking] = useState(false);
  // 提交进行中(含等迁移确认)时不再提交：按钮禁用挡不住输入框里的 Enter。
  const submitting = useRef(false);

  const validate = () => {
    const found: Partial<Record<AgentField, string>> = {};
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
    if (submitting.current) return;
    const { found, global: globalPath } = validate();
    setErrors(found);
    if (Object.keys(found).length) return;
    const input: CustomAgent = { name: name.trim(), global_directory: globalPath, project_directory: project.trim() };
    // 技能库按字段报的不合法，显示在对应输入框下面。
    const onInvalid = (err: LibraryError) => {
      const field = err.details?.field as AgentField | undefined;
      if (err.code !== ErrorCode.InvalidArgument || !field || !(field in input)) return false;
      setErrors({ [field]: describeError(err) });
      return true;
    };
    submitting.current = true;
    setWorking(true);
    try {
      const ok = agent ? await editAgent(ui, s, agent, input, onInvalid) : await addAgent(ui, input, onInvalid);
      if (ok) onClose();
    } finally {
      submitting.current = false;
      setWorking(false);
    }
  };

  const error = (field: AgentField) => errors[field] && <FieldError>{errors[field]}</FieldError>;
  return (
    <Modal
      title={agent ? `编辑 Agent「${agent.name}」` : "添加 Agent"}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" loading={working} onClick={submit}>
            {agent ? "保存" : "添加"}
          </Button>
        </>
      }
    >
      <Field label="名称">
        <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} />
        {error("name")}
      </Field>
      <Field label="全局技能目录(在用户主目录里面)">
        <div className="flex min-w-0 items-center gap-2">
          <Input className="grow" placeholder="~/.my-agent/skills" value={global} onChange={(e) => setGlobal(e.target.value)} />
          <Button
            onClick={async () => {
              const dir = await pickDirectory("选择全局技能目录");
              if (dir) setGlobal(shortPath(s, dir));
            }}
          >
            选择…
          </Button>
        </div>
        {error("global_directory")}
      </Field>
      <Field label="项目里的技能目录(相对项目根目录)">
        <Input
          placeholder=".my-agent/skills"
          value={project}
          onChange={(e) => setProject(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
        {error("project_directory")}
      </Field>
    </Modal>
  );
}
