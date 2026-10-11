// 右栏：作用域——全局与每个已登记项目下，直接列出装有技能的与检测到的 Agent,勾选了显示但没检测到的折进"其他 N 个 Agent";
// 项目行折叠时用小图标显示在用的 Agent。点选一行即选中该作用域(与顶栏胶囊同步)。
// 作用域行接受拖放：技能 = 直接安装，组 = 组安装;拖到折叠的项目或"其他 Agent"上停 0.5 秒自动展开。
import { useRef, useState } from "react";
import type { Agent, RegisteredProject } from "@bindings/internal/library";
import { registerProject, relocateProject, unregisterProject } from "@/lib/actions";
import { alreadyInScope, dropProps, useDnD } from "@/lib/dnd";
import type { Snapshot } from "@/lib/library";
import {
  agentsIn,
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
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FieldError, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Icon } from "./icons";
import { pickDirectory } from "./fetch-dialog";
import { rightClass } from "./layout";
import { NavCount, navItem } from "./nav";
import { AgentMark } from "./parts";

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
          className={cn(navItem({ kind: place ? "nested" : "tree" }), "sub")}
          data-active={scope === key}
          data-already={already || undefined}
          onClick={() => onScope(scope === key ? null : key)}
          data-drop-scope={key}
          {...dropProps(!!dnd.item && !already, () => dnd.dropOnScope(key))}
        >
          <AgentMark code={agent.code} name={agent.name} />
          <span className="grow truncate">{agent.name}</span>
          {issues > 0 && (
            <i className="size-1.5 shrink-0 rounded-full bg-warn" title={`${issues} 个技能文件夹与当前版本不一致，需要重新安装`} />
          )}
          <NavCount value={installed.length} accent />
        </button>
      </li>
    );
  };

  // 正被选中的作用域即使折在"其他"里也直接列出，否则看不到选中态。
  const agentRows = (place: Place) => {
    const { open, folded } = agentsIn(s, place);
    const isSelected = (a: Agent) => selected?.agent === a.code && placeOf(selected) === place;
    const shown = [...open, ...folded.filter(isSelected)];
    const rest = folded.filter((a) => !isSelected(a));
    const more = moreAgents.has(place);
    return (
      <>
        {shown.map((a) => scopeRow(a, place))}
        {more && rest.map((a) => scopeRow(a, place))}
        {rest.length > 0 && (
          <li>
            <button
              type="button"
              className={cn(navItem({ kind: place ? "nested" : "tree" }), "sub more text-xs text-muted-foreground")}
              onClick={() => onToggleMore(place)}
              {...springProps(!more, () => onToggleMore(place))}
            >
              <Icon name="chev" className={cn("size-2.5 transition-transform", more ? "-rotate-90" : "rotate-90")} />
              <span className="grow">{more ? "收起" : `其他 ${rest.length} 个 Agent`}</span>
            </button>
          </li>
        )}
        {shown.length === 0 && rest.length === 0 && (
          <li className="px-2 py-1 text-[11.5px] text-muted-foreground">没有可选的 Agent,可在设置页勾选显示</li>
        )}
      </>
    );
  };

  const register = () => ui.open((close) => <ProjectPathDialog home={s.info.home_dir} onClose={close} />);
  const relocate = (project: RegisteredProject) =>
    ui.open((close) => <ProjectPathDialog home={s.info.home_dir} project={project} onClose={close} />);

  return (
    <aside className={rightClass}>
      <RailHead icon="globe" label="全局" />
      <ul className={listClass}>{agentRows(0)}</ul>
      <RailHead icon="folder" label="已登记项目">
        <Button
          variant="ghost"
          size="icon-sm"
          className="opacity-0 group-hover/sec:opacity-100 focus-visible:opacity-100"
          title="登记项目"
          onClick={register}
        >
          <Icon name="plus" />
        </Button>
      </RailHead>
      <ul className={listClass}>
        {s.projects.map((p) => {
          const open = expanded.has(p.id);
          const count = s.skillScopes.filter((x) => placeOf(x.scope) === p.id).length;
          const current = !!selected && placeOf(selected) === p.id;
          return (
            <li key={p.id}>
              <div
                role="button"
                tabIndex={0}
                aria-expanded={open}
                className={cn(navItem({ kind: "tree" }), "proj gap-1.5", current && "font-semibold text-foreground")}
                onClick={() => onToggleProject(p.id)}
                title={p.missing ? `${shortPath(s, p.path)}\n不在这台机器上` : shortPath(s, p.path)}
                {...springProps(!open, () => onToggleProject(p.id))}
              >
                <Icon name="chev" className={cn("size-2.5 transition-transform", open && "rotate-90")} />
                <Icon name="folder" className="size-[13px]" />
                <span className="grow truncate">{projectName(s, p.id)}</span>
                {p.missing && (
                  <Badge tone="warn" title="项目目录在这台机器上不存在;可以改项目位置">
                    不在这台机器上
                  </Badge>
                )}
                {!open && (
                  <span className="inline-flex items-center gap-[3px] group-hover:hidden">
                    {agentsUsedIn(s, p.id).map((a) => (
                      <AgentMark key={a.code} code={a.code} name={a.name} className="size-3 text-[8px]" />
                    ))}
                  </span>
                )}
                <span className="row-actions hidden gap-0.5 group-hover:inline-flex">
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    title="改项目位置"
                    onClick={(e) => {
                      e.stopPropagation();
                      void relocate(p);
                    }}
                  >
                    <Icon name="edit" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    title="注销项目"
                    onClick={(e) => {
                      e.stopPropagation();
                      void unregisterProject(ui, s, p);
                    }}
                  >
                    <Icon name="close" />
                  </Button>
                </span>
                <span className="group-hover:hidden">
                  <NavCount value={count} />
                </span>
              </div>
              {open && <ul className="mt-0.5 flex flex-col gap-0.5 pl-[18px]">{agentRows(p.id)}</ul>}
            </li>
          );
        })}
      </ul>
      {s.projects.length === 0 && <Hint className="mt-1 px-6">还没有登记项目。登记后可以把技能安装到项目级目录。</Hint>}
    </aside>
  );
}

// 行的左缘对齐分节标题的文字：标题是 图标 + 名称，行从名称下方开始。
const listClass = "mt-0.5 flex flex-col gap-0.5 pl-4";

function RailHead({ icon, label, children }: { icon: "globe" | "folder"; label: string; children?: React.ReactNode }) {
  return (
    <div className="nav-sec group/sec flex h-[26px] items-center justify-between pr-0.5 pl-2 text-[11px] font-semibold text-muted-foreground not-first:mt-2.5 first:mt-1">
      <span className="inline-flex items-center gap-[5px]">
        <Icon name={icon} className="size-[11px]" />
        {label}
      </span>
      {children}
    </div>
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
    if (!resolved?.path || working) return;
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
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" disabled={!resolved?.path} loading={working} onClick={submit}>
            {project ? "改位置" : "登记"}
          </Button>
        </>
      }
    >
      <Hint>
        {project
          ? "把项目指到本机上的目录(从别的机器同步过来，或项目目录被移动、改名之后)。直接安装与组安装都保留;旧位置的技能文件夹不搬动，技能库从此不再管。"
          : "登记后，这个项目与每个 Agent 组成项目级作用域，技能可以安装到项目里的 Agent 目录。"}
      </Hint>
      <div className="flex min-w-0 items-center gap-2">
        <Input
          className="grow"
          autoFocus
          placeholder="/path/to/project 或 ~/path/to/project"
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
        <Button
          onClick={async () => {
            const dir = await pickDirectory(project ? "选择项目在这台机器上的目录" : "选择要登记的项目目录");
            if (dir) setPath(dir);
          }}
        >
          选择…
        </Button>
      </div>
      {resolved?.error && <FieldError>{resolved.error}</FieldError>}
    </Modal>
  );
}
