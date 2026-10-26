// 一次确认把作用域里不是库装的技能全部入库：第一次打开 app 时看各 Agent 的全局，登记一个项目之后看这个项目;
// 之后随时能从入库技能里对各 Agent 的全局和全部已登记项目再打开，也能从作用域里那一行淡淡的提示打开这一处。
// 磁盘上什么都不改;按认出的来源分组，同名不同内容的单列出来只选一份，一个技能失败不影响其余的，读不成来源的可以改链接或清空后重试。
import { useMemo, useState } from "react";
import { AppService, LibraryService } from "@bindings/cmd/desktop";
import {
  ErrorCode,
  type Agent,
  type FetchedUnmanagedSkill,
  type PlannedUnmanagedSkill,
  type RegisteredProject,
  type Scope,
} from "@bindings/internal/library";
import { describeError, unmanagedSourceError } from "@/lib/errors";
import { messages, useT } from "@/lib/i18n";
import { toLibraryError, useFoundSources, withFoundSource } from "@/lib/library";
import { fetchPlan } from "@/lib/consent";
import { agentNameIn, homePath, outsideLinkNote, placeOf, scopeLabel, scopeOf } from "@/lib/model";
import { Modal, ui, useUI } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { Diagnostics } from "@/components/app/parts";
import { LeftDialog } from "./fetch-unmanaged-left";
import {
  SameNameInLibrary,
  fetchedMessage,
  foldersOf,
  noSource,
  outsideTarget,
  sourceKey,
  sourceOf,
  type Failed,
  fetchInBatches,
} from "./fetch-unmanaged-parts";
import { ConflictRow, PlannedRow, type SourceEdit } from "./fetch-unmanaged-rows";

// Where 是这次看的是哪里，决定标题和开头那句话。
export type Where =
  | { kind: "first-open" }
  | { kind: "everywhere"; projects: RegisteredProject[] }
  | { kind: "project"; project: RegisteredProject }
  | { kind: "scope"; label: string };

// offerFirstOpen 在这台机器上第一次打开 app 时，把用户带到各 Agent 全局目录里不是库装的技能;没有就什么都不做。
export async function offerFirstOpen(agents: Agent[]) {
  if (!(await AppService.FirstOpen())) return;
  await offerFetchUnmanaged(agents, agents.map((a) => scopeOf(a.code, 0)), { kind: "first-open" });
}

// offerProject 在登记一个项目之后，把用户带到这个项目里各 Agent 目录下不是库装的技能;没有就什么都不做。
export async function offerProject(project: RegisteredProject) {
  const agents = (await LibraryService.ListAgents().catch(() => null)) ?? [];
  await offerFetchUnmanaged(agents, agents.map((a) => scopeOf(a.code, project.id)), { kind: "project", project });
}

// planEverywhere 为各 Agent 的全局目录和全部已登记项目生成整批入库的计划，返回打开确认的函数;
// 用户自己点的，计划为空或读不出时告诉一声并返回 null。signal 取消时停下读取，什么都不说，返回 null。
export function planEverywhere(agents: Agent[], projects: RegisteredProject[], signal: AbortSignal) {
  const scopes = [0, ...projects.map((p) => p.id)].flatMap((place) => agents.map((a) => scopeOf(a.code, place)));
  return planFetchUnmanaged(agents, scopes, { kind: "everywhere", projects }, false, signal);
}

// offerFetchUnmanaged 生成整批入库的计划并打开确认;quiet 为 false 时(用户自己点的)计划为空也告诉一声。
export async function offerFetchUnmanaged(agents: Agent[], scopes: Scope[], where: Where, quiet = true) {
  await (await planFetchUnmanaged(agents, scopes, where, quiet))?.();
}

async function planFetchUnmanaged(agents: Agent[], scopes: Scope[], where: Where, quiet: boolean, signal?: AbortSignal) {
  let plan: PlannedUnmanagedSkill[];
  let library: string[];
  let home: string;
  let agentsShown: Partial<Record<string, boolean>>;
  try {
    const call = LibraryService.PlanFetchUnmanaged({ scopes });
    signal?.addEventListener("abort", () => void call.cancel(), { once: true });
    plan = fetchPlan((await call) ?? []);
    library = ((await LibraryService.ListSkills()) ?? []).map((x) => x.name);
    home = (await LibraryService.Info()).home_dir;
    // 项目里合成的一处叫这台机器上显示的那几个 Agent,和右栏一样按应用设置里的显示勾选。
    agentsShown = (await AppService.Settings()).agents_shown ?? {};
  } catch (err) {
    const e = toLibraryError(err);
    if (e.code !== ErrorCode.Canceled && !quiet && !signal?.aborted)
      ui.toast(describeError(e), "error");
    return null;
  }
  if (signal?.aborted) return null;
  if (plan.length === 0) {
    if (!quiet) ui.toast(where.kind === "everywhere" ? messages().unmanaged.nothingAnywhere : messages().unmanaged.nothingHere);
    return null;
  }
  return () => ui.open((close) => (
    <FetchUnmanagedDialog
      agents={agents}
      agentsShown={agentsShown}
      plan={plan}
      library={new Set(library)}
      home={home}
      where={where}
      close={close}
    />
  ));
}

function FetchUnmanagedDialog({
  agents,
  agentsShown,
  plan: listed,
  library,
  home,
  where,
  close,
}: {
  agents: Agent[];
  agentsShown: Partial<Record<string, boolean>>;
  plan: PlannedUnmanagedSkill[];
  library: Set<string>; // 库里已有的技能名：同名的标出来，内容不同时入库后会有两个同名的
  home: string;
  where: Where;
  close: () => void;
}) {
  const ui = useUI();
  const t = useT().unmanaged;
  // 来源记录认不出来源的，在 skills.sh 上找到来源后补上，和来源记录认出的一样预填、分组。
  const sources = useFoundSources();
  const plan = useMemo(() => listed.map((p) => withFoundSource(sources, p)), [listed, sources]);
  const [picked, setPicked] = useState<Set<number>>(
    () => new Set(listed.flatMap((p, i) => (p.selected ? [i] : []))),
  );
  const [progress, setProgress] = useState<string>();
  const [left, setLeft] = useState<{ fetched: FetchedUnmanagedSkill[]; failed: Failed[] }>();
  // links 是确认前改过的来源链接，按计划里的下标;editing 是正在改的那一条。
  const [links, setLinks] = useState<Map<number, string>>(new Map());
  const [editing, setEditing] = useState<number | null>(null);

  // 各 Agent 的全局和各项目一起看时，每一处写成作用域(在全局还是哪个项目);只看一处时写 Agent 的名字。
  const placeName = (scope: Scope) =>
    where.kind === "everywhere"
      ? scopeLabel({ agents, agentsShown, projects: where.projects }, scope)
      : agentNameIn({ agents, agentsShown }, scope.agent, placeOf(scope));
  const agentNames = (planned: PlannedUnmanagedSkill) =>
    t.agents([...new Set(foldersOf(planned).map((f) => placeName(f.scope)))]);

  const usable = plan.flatMap((p, i) => (p.error ? [] : [i]));
  const unusable = plan.filter((p) => p.error);
  const conflicts = new Map<string, number[]>();
  const groups = new Map<string, number[]>();
  for (const i of usable) {
    const p = plan[i];
    if (p.conflict)
      conflicts.set(p.name, [...(conflicts.get(p.name) ?? []), i]);
    else
      groups.set(sourceKey(p.source_input, p.source_ref), [
        ...(groups.get(sourceKey(p.source_input, p.source_ref)) ?? []),
        i,
      ]);
  }
  // 有来源的组在前，没有来源的在最后。
  const ordered = [...groups].sort(([a], [b]) =>
    a === noSource ? 1 : b === noSource ? -1 : a.localeCompare(b),
  );

  const toggle = (i: number) =>
    setPicked((set) => {
      const next = new Set(set);
      if (next.has(i)) next.delete(i);
      else next.add(i);
      return next;
    });
  // 同名的几份只入库一份:选中一份就去掉其余的;null 表示都先不入库。
  const choose = (members: number[], i: number | null) =>
    setPicked((set) => {
      const next = new Set(set);
      for (const m of members) next.delete(m);
      if (i !== null) next.add(i);
      return next;
    });

  const chosen = [...picked].sort((a, b) => a - b);
  const team = chosen
    .map((i) => plan[i])
    .filter((p) => p.in_repository || p.in_project_lock);
  const outside = chosen.map((i) => plan[i]).filter((p) => p.link_outside);

  const submit = async () => {
    const { done, lost } = await fetchInBatches(
      chosen.map((i) => ({ planned: plan[i], link: links.get(i) })),
      (key) => setProgress(key === noSource ? t.fetchingNoSource : t.reading(key.split("\n")[0])),
    );
    setProgress(undefined);
    ui.reload();
    if (lost.length === 0) {
      close();
      ui.toast(fetchedMessage(done));
      return;
    }
    setLeft({ fetched: done, failed: lost });
  };

  if (left) return <LeftDialog fetched={left.fetched} failed={left.failed} agentNames={agentNames} close={close} />;

  // edit 是第 i 条的来源链接怎么改，见 SourceEdit。
  const edit = (i: number): SourceEdit => ({
    link: links.get(i),
    editing: editing === i,
    onEdit: () => {
      if (!links.has(i)) setLinks((map) => new Map(map).set(i, plan[i].source_input ?? ""));
      setEditing(editing === i ? null : i);
    },
    onLink: (link) => setLinks((map) => new Map(map).set(i, link)),
    onDone: () => setEditing(null),
  });

  const lead =
    where.kind === "first-open" || (where.kind === "everywhere" && where.projects.length === 0)
      ? t.leadFirstOpen(usable.length)
      : where.kind === "everywhere"
        ? t.leadEverywhere(where.projects.length, usable.length)
        : where.kind === "project"
          ? t.leadProject(where.project.name, usable.length)
          : t.leadScope(where.label, usable.length);

  return (
    <Modal
      title={t.title}
      wide
      onClose={close}
      footer={
        <>
          {progress && (
            <span className="fetch-unmanaged-progress text-[11px] text-muted-foreground">
              {progress}
            </span>
          )}
          <span className="grow" />
          <Button onClick={close} disabled={!!progress}>
            {t.notNow}
          </Button>
          <Button
            className="fetch-unmanaged-confirm"
            variant="primary"
            disabled={picked.size === 0}
            loading={!!progress}
            onClick={() => void submit()}
            autoFocus
          >
            {t.fetchN(picked.size)}
          </Button>
        </>
      }
    >
      <p className="fetch-unmanaged-lead text-[13px] font-semibold">
        {t.lead(lead)}
      </p>
      <Hint>{t.cost}</Hint>
      {where.kind === "first-open" && (
        <Hint>{t.projectsLater}</Hint>
      )}
      {ordered.length > 0 && (
        <ListBox scroll className="fetch-unmanaged-groups">
          {ordered.map(([key, members]) => {
            const first = plan[members[0]];
            return (
              <div key={key} className="fetch-unmanaged-group">
                <div className="sticky top-0 z-1 flex min-w-0 items-center gap-1.5 border-t border-border-subtle bg-gray-50/95 px-2.5 py-1 text-[11px] font-semibold text-foreground-secondary first:border-t-0">
                  <span className="min-w-0 truncate font-mono">
                    {key === noSource ? t.noSource : first.source_input}
                  </span>
                  {first.source_ref && (
                    <span className="shrink-0 font-normal text-muted-foreground">
                      {first.source_ref}
                    </span>
                  )}
                  <span className="shrink-0 font-normal text-muted-foreground">
                    {t.groupCount(members.length)}
                  </span>
                  <span className="grow" />
                  <span className="shrink-0 font-normal text-muted-foreground">
                    {key === noSource ? t.noSourceNote : t.sourceNote}
                  </span>
                </div>
                {members.map((i) => (
                  <PlannedRow
                    key={i}
                    planned={plan[i]}
                    picked={picked.has(i)}
                    onToggle={() => toggle(i)}
                    home={home}
                    inLibrary={library.has(plan[i].name)}
                    agents={agentNames(plan[i])}
                    edit={edit(i)}
                  />
                ))}
              </div>
            );
          })}
        </ListBox>
      )}
      {conflicts.size > 0 && (
        <div className="fetch-unmanaged-conflicts flex flex-col gap-1.5">
          <b className="text-xs">{t.conflicts}</b>
          <Hint>{t.conflictsHint}</Hint>
          <ListBox>
            {[...conflicts].map(([name, members]) => (
              <div key={name} className="fetch-unmanaged-conflict">
                <div className="flex items-center gap-1.5 border-t border-border-subtle bg-gray-50/95 px-2.5 py-1 text-[11px] font-semibold text-foreground-secondary first:border-t-0">
                  {name}
                  {library.has(name) && <SameNameInLibrary name={name} sourced={members.some((i) => !!sourceOf(plan[i], links.get(i)).input)} />}
                </div>
                {members.map((i) => (
                  <ConflictRow
                    key={i}
                    planned={plan[i]}
                    group={name}
                    picked={picked.has(i)}
                    onPick={() => choose(members, i)}
                    home={home}
                    agents={agentNames(plan[i])}
                    edit={edit(i)}
                  />
                ))}
                <ListRow asChild compact clickable className="pl-6">
                  <label>
                    <input
                      type="radio"
                      name={`conflict-${name}`}
                      className="shrink-0 accent-primary"
                      checked={!members.some((i) => picked.has(i))}
                      onChange={() => choose(members, null)}
                    />
                    <span className="text-muted-foreground">{t.noneOfThem}</span>
                  </label>
                </ListRow>
              </div>
            ))}
          </ListBox>
        </div>
      )}
      {team.length > 0 && (
        <Notice tone="warn" className="fetch-unmanaged-team">
          {t.team(team.map((p) => p.name))}
        </Notice>
      )}
      {outside.map((p) => (
        <Notice key={p.name} tone="warn" className="fetch-unmanaged-link-outside">
          {t.outsideNote(p.name, outsideLinkNote(homePath(home, outsideTarget(p))))}
        </Notice>
      ))}
      {unusable.length > 0 && (
        <Diagnostics
          summary={t.unusable(unusable.length)}
          items={unusable.map((p) => ({
            key: foldersOf(p)[0]?.folder ?? p.name,
            reason: t.unusableReason(p.name, unmanagedSourceError(p.error!, p.name)),
          }))}
        />
      )}
    </Modal>
  );
}
