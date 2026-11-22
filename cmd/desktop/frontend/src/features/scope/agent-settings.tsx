// 设置页的 Agent 分区：列出已添加的 Agent,每行有显示勾选(应用设置，存在数据库的 config 表里，随推送、拉回走)、编辑与移除;
// 「添加 Agent」默认列出候选池(可以搜索、勾选几个一次添加),也能切到手填表单。显示勾选的说明在设置页分区标题的提示里。
// 编辑表单改名称、技能目录(第 1 个装新技能，其余是附加目录)和检测目录。
import { useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { AppService, LibraryService } from "@bindings/cmd/desktop";
import { ErrorCode, type Agent, type AgentInput } from "@bindings/internal/library";
import { addAgent, addAgentsFromPool, editAgent, removeAgent } from "./agents";
import {
  alreadyRead,
  detectedPicks,
  directoryOptions,
  filterDirectoryOptions,
  opensWhileTyping,
  poolMatches,
  sortPool,
  undetected,
  type DirectoryKind,
  type DirectoryOptions,
} from "./agents-rules";
import { describeError } from "@/lib/errors";
import { messages, useT } from "@/lib/i18n";
import { currentSnapshot, toLibraryError, useHasWSL, useSnapshot, type LibraryError, type Snapshot } from "@/lib/library";
import { agentShown, directoriesAt } from "@/lib/places";
import { insideHome, isWSLPath, resolveLocalPath, nativePath, shortPath, slashPath } from "@/lib/paths";
import { Modal, ui, useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FieldError, Hint } from "@/components/ui/field";
import { Icon } from "@/components/ui/icon";
import { Input } from "@/components/ui/input";
import { ListBox, ListRow } from "@/components/ui/list";
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import { pickDirectory } from "@/features/fetch/fetch-dialog";
import { AgentMark, PathWhere } from "@/components/app/parts";
import { SetRow } from "@/components/app/set-row";

// agentDirectories 是一行里写出的 Agent 的全部技能目录:text 写成一行，title 逐个目录一行。
function agentDirectories(s: Snapshot, agent: Agent, t: ReturnType<typeof useT>["agents"]) {
  const globals = directoriesAt(agent, 0).map((d) => shortPath(s, d));
  const projects = directoriesAt(agent, 1).map(nativePath);
  return { text: t.directories(globals, projects), title: t.directoriesTitle(globals, projects) };
}

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
      {s.agents.map((a) => {
        const on = agentShown(shown, a);
        const dirs = agentDirectories(s, a, t);
        return (
          <SetRow key={a.code} className="agent-row">
            <AgentMark name={a.name} />
            {/* 一行：名字、没检测到时标出，后面是它的全部技能目录(放不下就截断，悬停逐个看)。 */}
            <span className="shrink-0">{a.name}</span>
            {undetected(a) && <Badge title={t.notDetectedTitle}>{t.notDetected}</Badge>}
            <span className="min-w-0 grow truncate font-mono text-caption text-muted-foreground select-text" title={dirs.title}>
              {dirs.text}
            </span>
            <Button variant="ghost" onClick={() => void edit(a)}>
              {t.edit}
            </Button>
            <Button variant="ghost" danger onClick={() => void removeAgent(a)}>
              {t.remove}
            </Button>
            {shown !== undefined && (
              <Switch checked={on} aria-label={t.show(a.name)} title={on ? t.shown : t.hidden} onCheckedChange={(v) => onShown(a, v)} />
            )}
          </SetRow>
        );
      })}
    </>
  );
}

// AddAgentDialog 是「添加 Agent」:默认列出候选池，检测到的排在前面并标出，可以按名称、目录搜索、勾选几个一次添加;
// 独有的目录已经有已添加的 Agent 在读的标出「已有 Agent 读这个目录」,不阻止勾选。底部能切到手填表单。
// picks 是打开时已经勾上的，title、lead 换掉标题、在搜索框上面加一句说明(升级后提示添加时用)。
export function AddAgentDialog({ onClose, picks, title, lead }: { onClose: () => void; picks?: string[]; title?: string; lead?: string }) {
  const [manual, setManual] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(new Set(picks));
  if (manual) return <AgentDialog onClose={onClose} onBack={() => setManual(false)} />;
  return <AgentPoolDialog onClose={onClose} onManual={() => setManual(true)} picked={picked} setPicked={setPicked} title={title} lead={lead} />;
}

// offerAgentPool 在从旧版本升级上来的数据库升级后第一次打开时弹一次「添加 Agent」:升级时原有的 Agent 保持已添加，
// 其余收录的进了候选池，按检测结果添加只在第一次打开时做过。候选池里检测到的默认勾上(已有 Agent 读它独有目录的不勾),
// 用户确认后才添加;候选池里一个都没检测到时不弹。
export async function offerAgentPool() {
  if (!(await AppService.OfferAgentPool().catch(() => false))) return;
  const pool = await LibraryService.ListAgentPool().catch(() => null);
  if (!pool?.some((a) => a.detected)) return;
  const t = messages().agents;
  const picks = detectedPicks(pool, currentSnapshot().agents);
  await ui.open((close) => <AddAgentDialog onClose={close} picks={picks} title={t.upgradeTitle} lead={t.upgradeLead} />);
}

function AgentPoolDialog({
  onClose,
  onManual,
  picked,
  setPicked,
  title,
  lead,
}: {
  onClose: () => void;
  onManual: () => void;
  picked: Set<string>;
  setPicked: (update: (set: Set<string>) => Set<string>) => void;
  title?: string;
  lead?: string;
}) {
  const s = useSnapshot();
  const t = useT().agents;
  const pool = useQuery({ queryKey: ["agent-pool"], queryFn: () => LibraryService.ListAgentPool(), gcTime: 0, staleTime: 0 });
  const [search, setSearch] = useState("");
  const [working, setWorking] = useState(false);
  const all = sortPool(pool.data ?? []);
  const rows = all.filter((a) => poolMatches(a, search));
  const toggle = (code: string) =>
    setPicked((set) => {
      const next = new Set(set);
      if (!next.delete(code)) next.add(code);
      return next;
    });
  const add = async () => {
    const chosen = all.filter((a) => picked.has(a.code));
    if (!chosen.length) return;
    setWorking(true);
    try {
      if (await addAgentsFromPool(chosen)) onClose();
    } finally {
      setWorking(false);
    }
  };
  return (
    <Modal
      title={title ?? t.addTitle}
      wide
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" className="add-agent-manual mr-auto" onClick={onManual}>
            {t.manual}
          </Button>
          <Button onClick={onClose}>{t.cancel}</Button>
          <Button variant="primary" loading={working} disabled={!picked.size} onClick={add}>
            {picked.size ? t.addPicked(picked.size) : t.add}
          </Button>
        </>
      }
    >
      {lead && <Hint className="agent-pool-lead">{lead}</Hint>}
      <Input autoFocus placeholder={t.searchPool} value={search} onChange={(e) => setSearch(e.target.value)} />
      {pool.isPending ? (
        <Hint>{t.loadingPool}</Hint>
      ) : pool.isError ? (
        <FieldError>{describeError(toLibraryError(pool.error))}</FieldError>
      ) : !all.length ? (
        <Hint>{t.poolEmpty}</Hint>
      ) : !rows.length ? (
        <Hint>{t.poolNoMatch}</Hint>
      ) : (
        <ListBox scroll className="agent-pool max-h-[420px]">
          {rows.map((a) => {
            const dirs = agentDirectories(s, a, t);
            const read = alreadyRead(all, s.agents, a);
            return (
              <ListRow key={a.code} asChild compact clickable selected={picked.has(a.code)} className="pool-agent">
                <label data-code={a.code}>
                  <input type="checkbox" className="shrink-0 accent-primary" checked={picked.has(a.code)} onChange={() => toggle(a.code)} />
                  <AgentMark name={a.name} />
                  <b className="shrink-0 font-semibold whitespace-nowrap">{a.name}</b>
                  {a.detected && (
                    <Badge tone="info" className="pool-detected" title={t.detectedTitle}>
                      {t.detected}
                    </Badge>
                  )}
                  {read.length > 0 && (
                    <Badge
                      tone="warn"
                      className="pool-already-read shrink-0"
                      title={read.map((r) => t.alreadyReadTitle(r.project ? nativePath(r.dir) : shortPath(s, r.dir), r.project, r.by.map((x) => x.name))).join("\n")}
                    >
                      {t.alreadyRead}
                    </Badge>
                  )}
                  <span className="min-w-0 grow truncate text-right font-mono text-caption text-muted-foreground" title={dirs.title}>
                    {dirs.text}
                  </span>
                </label>
              </ListRow>
            );
          })}
        </ListBox>
      )}
    </Modal>
  );
}

type AgentField = keyof AgentInput;

// AgentDialog 手填添加一个 Agent,或给出 agent 时编辑它:名称、全局与项目里的技能目录(第 1 个装新技能，其余是附加目录)、检测目录。
// 全局技能目录与检测目录必须在用户主目录里面;项目里的技能目录相对项目根目录。onBack 给出时(从候选池切过来)底部能回到候选池。
export function AgentDialog({ agent, onClose, onBack }: { agent?: Agent; onClose: () => void; onBack?: () => void }) {
  const s = useSnapshot();
  const t = useT().agents;
  const home = s.info.home_dir;
  const wsl = useHasWSL();
  const [name, setName] = useState(agent?.name ?? "");
  const [dirs, setDirs] = useState<Record<DirectoryKind, string[]>>({
    global_directories: agent ? directoriesAt(agent, 0).map((d) => shortPath(s, d)) : [""],
    project_directories: agent ? directoriesAt(agent, 1).map(nativePath) : [""],
    detect_directories: agent ? (agent.detect_directories ?? []).map((d) => shortPath(s, d)) : [],
  });
  const [errors, setErrors] = useState<Partial<Record<AgentField, string>>>({});
  const [working, setWorking] = useState(false);
  // 目录下拉的候选来自应用内置的收录表(不是数据库里那一行):先列这个 Agent 的默认目录，再列其他 Agent 用到的。
  const defaults = useQuery({ queryKey: ["agent-defaults"], queryFn: () => LibraryService.ListAgentDefaults(), gcTime: 0, staleTime: 0 });
  const options = (kind: DirectoryKind) =>
    directoryOptions(defaults.data ?? [], agent?.code, kind, kind === "project_directories" ? nativePath : (d) => shortPath(s, d));
  // 提交进行中(含等迁移确认)时不再提交：按钮禁用挡不住输入框里的 Enter。
  const submitting = useRef(false);

  // homeDirectory 校验一个在用户主目录里面的目录(全局技能目录、检测目录),给出展开后的绝对路径或错误。
  const homeDirectory = (input: string): { path?: string; error?: string } => {
    const resolved = resolveLocalPath(input, home, wsl);
    // 全局目录在 Windows 本机时填了发行版里的路径：只能先去设置里把全局目录换到那个发行版。
    const wantsWSL = wsl && !isWSLPath(home) && (input.trim().startsWith("/") || (!!resolved?.path && isWSLPath(resolved.path)));
    if (!resolved) return { error: t.directoryRequired };
    if (wantsWSL) return { error: t.globalInWSL };
    if (resolved.path === undefined) return { error: resolved.error };
    if (!insideHome(resolved.path, home)) return { error: t.globalInHome };
    return { path: resolved.path };
  };
  const projectDirectory = (input: string): { path?: string; error?: string } => {
    const rel = slashPath(input.trim());
    if (rel.startsWith("/") || rel.startsWith("~")) return { error: t.projectRelative(nativePath(".agents/skills")) };
    if (rel.split("/").some((part) => part === "..") || rel.replace(/^\.\/?|\/+$/g, "") === "") return { error: t.projectInside };
    return { path: rel };
  };

  const validate = () => {
    const found: Partial<Record<AgentField, string>> = {};
    const out: Record<DirectoryKind, string[]> = { global_directories: [], project_directories: [], detect_directories: [] };
    if (!name.trim()) found.name = t.nameRequired;
    for (const kind of ["global_directories", "project_directories", "detect_directories"] as const) {
      // 空着的一行不算;技能目录至少要有一个。
      for (const input of dirs[kind].filter((d) => d.trim())) {
        const r = kind === "project_directories" ? projectDirectory(input) : homeDirectory(input);
        if (r.error) {
          found[kind] ??= `${input.trim()}：${r.error}`;
          continue;
        }
        out[kind].push(r.path!);
      }
      if (kind !== "detect_directories" && !found[kind] && !out[kind].length) {
        found[kind] = kind === "global_directories" ? t.globalRequired : t.projectRequired;
      }
    }
    return { found, out };
  };

  const submit = async () => {
    if (submitting.current) return;
    const { found, out } = validate();
    setErrors(found);
    if (Object.keys(found).length) return;
    const input: AgentInput = { name: name.trim(), ...out };
    // 技能库按字段报的不合法，显示在对应的那组目录下面。
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
  const list = (kind: DirectoryKind, label: string, hint: string, chooseTitle?: string) => (
    <DirectoryList
      kind={kind}
      label={label}
      hint={hint}
      values={dirs[kind]}
      onChange={(values) => setDirs((d) => ({ ...d, [kind]: values }))}
      placeholder={kind === "project_directories" ? nativePath(".my-agent/skills") : shortPath(s, home + nativePath("/.my-agent/skills"))}
      chooseTitle={chooseTitle}
      options={options(kind)}
      own={!!agent}
      first={kind !== "detect_directories"}
      onEnter={submit}
      error={error(kind)}
      where={kind === "project_directories" ? undefined : (v) => resolveLocalPath(v, home, wsl)?.path}
    />
  );
  return (
    <Modal
      title={agent ? t.editTitle(agent.name) : t.addManualTitle}
      wide
      onClose={onClose}
      footer={
        <>
          {onBack && (
            <Button variant="ghost" className="add-agent-pool mr-auto" onClick={onBack}>
              <Icon name="chevL" />
              {t.backToPool}
            </Button>
          )}
          <Button onClick={onClose}>{t.cancel}</Button>
          <Button variant="primary" loading={working} onClick={submit}>
            {agent ? t.save : t.add}
          </Button>
        </>
      }
    >
      <label data-slot="field" className="flex min-w-0 flex-col gap-1">
        <span className="text-body text-foreground-secondary">{t.name}</span>
        {/* 图标按名字认，边输入边预览。 */}
        <div className="flex min-w-0 items-center gap-2">
          <AgentMark name={name} className="size-6 rounded-control" />
          <Input className="grow" autoFocus value={name} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === "Enter" && submit()} />
        </div>
        {error("name")}
      </label>
      {list("global_directories", t.global, t.directoriesHint, t.chooseGlobal)}
      {list("project_directories", t.project, t.directoriesHint)}
      {list("detect_directories", t.detect, t.detectHint, t.chooseDetect)}
    </Modal>
  );
}

// DirectoryList 是一组目录的输入：每个目录一行，可以添加、去掉;first 时至少留一行，有几行时第 1 行标「装新技能」。
// chooseTitle 给出时每行有「选择…」(系统目录选择框);where 给出时在 WSL 可用的 Windows 上写出这一行在本机还是哪个发行版里。
function DirectoryList({
  kind,
  label,
  hint,
  values,
  onChange,
  placeholder,
  chooseTitle,
  options,
  own,
  first,
  onEnter,
  error,
  where,
}: {
  kind: DirectoryKind;
  label: string;
  hint: string;
  values: string[];
  onChange: (values: string[]) => void;
  placeholder: string;
  chooseTitle?: string;
  options: DirectoryOptions;
  own: boolean; // 编辑时:defaults 那一组叫「这个 Agent 的默认目录」
  first: boolean;
  onEnter: () => void;
  error: ReactNode;
  where?: (value: string) => string | undefined;
}) {
  const s = useSnapshot();
  const t = useT().agents;
  const set = (i: number, value: string) => onChange(values.map((v, j) => (j === i ? value : v)));
  return (
    <div data-slot="field" className={`agent-directories ${kind} flex min-w-0 flex-col gap-1`}>
      <span className="text-body text-foreground-secondary">{label}</span>
      {values.map((value, i) => (
        <div key={i} className="flex min-w-0 flex-col gap-0.5">
          <div className="flex min-w-0 items-center gap-2">
            <DirectoryInput
              label={label}
              placeholder={placeholder}
              value={value}
              onChange={(v) => set(i, v)}
              onEnter={onEnter}
              options={options}
              own={own}
              taken={values.filter((_, j) => j !== i)}
            />
            {/* 有几行时第 1 行标「装新技能」;每行留同样宽的位置，输入框对齐。 */}
            {first && values.length > 1 && (
              <span className="w-[4.5em] shrink-0 text-caption text-muted-foreground">{i === 0 ? t.installsNew : ""}</span>
            )}
            {chooseTitle && (
              <Button
                onClick={async () => {
                  const dir = await pickDirectory(chooseTitle);
                  if (dir) set(i, isWSLPath(dir) ? dir : shortPath(s, dir));
                }}
              >
                {t.choose}
              </Button>
            )}
            {(!first || values.length > 1) && (
              <Button variant="ghost" size="icon" className="remove-directory" title={t.removeDirectory} onClick={() => onChange(values.filter((_, j) => j !== i))}>
                <Icon name="close" />
              </Button>
            )}
          </div>
          {where && <PathWhere path={where(value)} />}
        </div>
      ))}
      <div className="flex min-w-0 items-center gap-2">
        <Button variant="ghost" size="sm" className="add-directory" onClick={() => onChange([...values, ""])}>
          <Icon name="plus" />
          {t.addDirectory}
        </Button>
        <Hint className="min-w-0 grow">{hint}</Hint>
      </div>
      {error}
    </div>
  );
}

// DirectoryInput 是一个目录的输入框，带下拉：点右边的箭头(或按 ↓)列出候选，输入时按文字过滤，也能直接填自己的路径。
// 候选先列这个 Agent 的默认目录，再列其他 Agent 用到的(见 directoryOptions);这组里别的行已经填了的不列。
// 下拉开着时 ↑↓ 选、Enter 填进去、Esc 收起;没选中候选时 Enter 照常提交表单。
function DirectoryInput({
  label,
  placeholder,
  value,
  onChange,
  onEnter,
  options,
  own,
  taken,
}: {
  label: string;
  placeholder: string;
  value: string;
  onChange: (value: string) => void;
  onEnter: () => void;
  options: DirectoryOptions;
  own: boolean;
  taken: string[];
}) {
  const t = useT().agents;
  const [open, setOpen] = useState(false);
  // typed 是打开下拉之后输入过的文字：点箭头打开时列出全部，输入了才过滤。
  const [typed, setTyped] = useState("");
  const [active, setActive] = useState(-1);
  const anchor = useRef<HTMLDivElement>(null);
  const shown = filterDirectoryOptions(options, typed, taken);
  const items = [...shown.defaults, ...shown.others];
  const show = (on: boolean) => {
    setOpen(on);
    setTyped("");
    setActive(-1);
  };
  const pick = (dir: string) => {
    onChange(dir);
    show(false);
  };
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (!open) return show(true);
      if (items.length) setActive((i) => (e.key === "ArrowDown" ? (i + 1) % items.length : (i <= 0 ? items.length : i) - 1));
    } else if (e.key === "Enter") {
      if (open && active >= 0 && items[active]) {
        e.preventDefault();
        pick(items[active]);
      } else {
        show(false);
        onEnter();
      }
    } else if (e.key === "Escape" && open) {
      // 只收起下拉，不关掉整个弹窗。
      e.preventDefault();
      e.stopPropagation();
      show(false);
    }
  };
  const group = (heading: string, dirs: string[], offset: number) =>
    dirs.length > 0 && (
      <div role="group" aria-label={heading}>
        <div className="px-2 pt-1.5 pb-1 text-caption font-semibold text-muted-foreground">{heading}</div>
        {dirs.map((dir, i) => (
          <div
            key={dir}
            role="option"
            aria-selected={active === offset + i}
            className={cn(
              "directory-option flex h-7 cursor-default items-center truncate rounded-control px-2 font-mono text-body select-none hover:bg-fill",
              active === offset + i && "bg-fill",
            )}
            // 按下时就填进去：不让输入框先失去焦点。
            onMouseDown={(e) => {
              e.preventDefault();
              pick(dir);
            }}
          >
            {dir}
          </div>
        ))}
      </div>
    );
  return (
    <Popover open={open} onOpenChange={(on) => !on && show(false)}>
      <PopoverAnchor asChild>
        <div ref={anchor} className="relative flex min-w-0 grow">
          <Input
            className="grow pr-6 font-mono"
            aria-label={label}
            role="combobox"
            aria-expanded={open}
            aria-autocomplete="list"
            placeholder={placeholder}
            value={value}
            onChange={(e) => {
              onChange(e.target.value);
              setTyped(e.target.value);
              setActive(-1);
              if (!open && opensWhileTyping(options, e.target.value, taken)) setOpen(true);
            }}
            onKeyDown={onKeyDown}
          />
          <button
            type="button"
            tabIndex={-1}
            className="directory-options absolute inset-y-0 right-0 flex w-6 items-center justify-center text-muted-foreground hover:text-foreground"
            title={t.knownDirectories}
            aria-label={t.knownDirectories}
            onMouseDown={(e) => {
              // 不让输入框失去焦点;再点一次收起。
              e.preventDefault();
              anchor.current?.querySelector("input")?.focus();
              show(!open);
            }}
          >
            <Icon name="down" className="size-3" />
          </button>
        </div>
      </PopoverAnchor>
      <PopoverContent
        align="start"
        sideOffset={4}
        className="directory-menu z-[60] max-h-64 w-[var(--radix-popover-trigger-width)] overflow-y-auto"
        role="listbox"
        onOpenAutoFocus={(e) => e.preventDefault()}
        onCloseAutoFocus={(e) => e.preventDefault()}
        onInteractOutside={(e) => anchor.current?.contains(e.target as Node) && e.preventDefault()}
      >
        {items.length ? (
          <>
            {group(own ? t.defaultDirectories : t.knownDirectories, shown.defaults, 0)}
            {group(t.otherDirectories, shown.others, shown.defaults.length)}
          </>
        ) : (
          <div className="px-2 py-3 text-center text-body text-muted-foreground">{t.noDirectoryMatch}</div>
        )}
      </PopoverContent>
    </Popover>
  );
}
