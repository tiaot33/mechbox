// 设置页的 Agent 分区：每个 Agent 的显示勾选(应用设置，存在数据库的 config 表里，随推送、拉回走),以及自定义 Agent 的添加、编辑、删除。
// 内置 Agent 的名称和目录由应用维护，只能勾选显示。
import { useRef, useState } from "react";
import { ErrorCode, type Agent, type CustomAgent } from "@bindings/internal/library";
import { addAgent, deleteAgent, editAgent } from "./agents";
import { describeError } from "@/lib/errors";
import { useT } from "@/lib/i18n";
import { useSnapshot, type LibraryError } from "@/lib/library";
import { agentShown, projectDirectory, projectDirectoryInput, shortPath } from "@/lib/model";
import { insideHome, resolveLocalPath } from "@/lib/paths";
import { Modal, useUI } from "@/lib/ui";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, FieldError, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { pickDirectory } from "@/features/fetch/fetch-dialog";
import { AgentMark } from "@/components/app/parts";
import { SetRow } from "@/components/app/set-row";

export function AgentSection({
  shown,
  onShown,
}: {
  shown?: Partial<Record<string, boolean>> | null; // 应用设置还没读到时为 undefined,不显示勾选
  onShown: (agent: Agent, on: boolean) => void;
}) {
  const s = useSnapshot();
  const ui = useUI();
  const t = useT().agents;
  const edit = (agent: Agent) => ui.open((close) => <AgentDialog agent={agent} onClose={close} />);
  return (
    <>
      <Hint className="mb-1">{t.shownHint}</Hint>
      {s.agents.map((a) => {
        const on = agentShown(shown, a);
        return (
          <SetRow key={a.code}>
            <AgentMark code={a.code} name={a.name} />
            <div className="min-w-0 grow">
              <div className="flex items-center gap-1.5">
                {a.name}
                {a.custom ? <Badge>{t.custom}</Badge> : <Badge>{a.detected ? t.detected : t.notDetected}</Badge>}
              </div>
              <div className="truncate font-mono text-[11px] text-muted-foreground select-text">
                {t.directories(shortPath(s, a.global_directory), projectDirectory(a.project_directory))}
              </div>
            </div>
            {a.custom && (
              <>
                <Button onClick={() => void edit(a)}>{t.edit}</Button>
                <Button onClick={() => void deleteAgent(a)}>{t.delete}</Button>
              </>
            )}
            {shown !== undefined && (
              <Switch checked={on} aria-label={t.show(a.name)} title={on ? t.shown : t.hidden} onCheckedChange={(v) => onShown(a, v)} />
            )}
          </SetRow>
        );
      })}
    </>
  );
}

type AgentField = keyof CustomAgent;

// AgentDialog 添加一个自定义 Agent,或给出 agent 时编辑它。全局技能目录必须在用户主目录里面。
export function AgentDialog({ agent, onClose }: { agent?: Agent; onClose: () => void }) {
  const s = useSnapshot();
  const t = useT().agents;
  const home = s.info.home_dir;
  const sep = home.includes("\\") ? "\\" : "/";
  const [name, setName] = useState(agent?.name ?? "");
  const [global, setGlobal] = useState(agent ? shortPath(s, agent.global_directory) : "");
  const [project, setProject] = useState(agent ? projectDirectory(agent.project_directory) : "");
  const [errors, setErrors] = useState<Partial<Record<AgentField, string>>>({});
  const [working, setWorking] = useState(false);
  // 提交进行中(含等迁移确认)时不再提交：按钮禁用挡不住输入框里的 Enter。
  const submitting = useRef(false);

  const validate = () => {
    const found: Partial<Record<AgentField, string>> = {};
    if (!name.trim()) found.name = t.nameRequired;
    const resolved = resolveLocalPath(global, home);
    if (!resolved) found.global_directory = t.globalRequired;
    else if (resolved.path === undefined) found.global_directory = resolved.error;
    else if (!insideHome(resolved.path, home)) found.global_directory = t.globalInHome;
    const rel = projectDirectoryInput(project.trim());
    if (!rel) found.project_directory = t.projectRequired;
    else if (rel.startsWith("/") || rel.startsWith("~")) found.project_directory = t.projectRelative;
    else if (rel.split("/").some((part) => part === "..") || rel.replace(/^\.\/?|\/+$/g, "") === "") {
      found.project_directory = t.projectInside;
    }
    return { found, global: resolved?.path ?? "", project: rel };
  };

  const submit = async () => {
    if (submitting.current) return;
    const { found, global: globalPath, project: projectPath } = validate();
    setErrors(found);
    if (Object.keys(found).length) return;
    const input: CustomAgent = { name: name.trim(), global_directory: globalPath, project_directory: projectPath };
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
      const ok = agent ? await editAgent(agent, input, onInvalid) : await addAgent(input, onInvalid);
      if (ok) onClose();
    } finally {
      submitting.current = false;
      setWorking(false);
    }
  };

  const error = (field: AgentField) => errors[field] && <FieldError>{errors[field]}</FieldError>;
  return (
    <Modal
      title={agent ? t.editTitle(agent.name) : t.addTitle}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t.cancel}</Button>
          <Button variant="primary" loading={working} onClick={submit}>
            {agent ? t.save : t.add}
          </Button>
        </>
      }
    >
      <Field label={t.name}>
        {/* 图标按名字认，边输入边预览。 */}
        <div className="flex min-w-0 items-center gap-2">
          <AgentMark code={agent?.code} name={name} className="size-6 rounded-md" />
          <Input className="grow" autoFocus value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        {error("name")}
      </Field>
      <Field label={t.global}>
        <div className="flex min-w-0 items-center gap-2">
          <Input className="grow" placeholder={shortPath(s, [home, ".my-agent", "skills"].join(sep))} value={global} onChange={(e) => setGlobal(e.target.value)} />
          <Button
            onClick={async () => {
              const dir = await pickDirectory(t.chooseGlobal);
              if (dir) setGlobal(shortPath(s, dir));
            }}
          >
            {t.choose}
          </Button>
        </div>
        {error("global_directory")}
      </Field>
      <Field label={t.project}>
        <Input
          placeholder={projectDirectory(".my-agent/skills")}
          value={project}
          onChange={(e) => setProject(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
        {error("project_directory")}
      </Field>
    </Modal>
  );
}
