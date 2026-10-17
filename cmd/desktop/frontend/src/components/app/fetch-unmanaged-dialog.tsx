// 一次确认把作用域里不是库装的技能全部入库：第一次打开 app 时看各 Agent 的全局，登记一个项目之后看这个项目，
// 之后也能从作用域里那一行淡淡的提示再打开。磁盘上什么都不改;按认出的来源分组，同名不同内容的单列出来只选一份，
// 一个技能失败不影响其余的，读不成来源的可以改链接或清空后重试。
import { useState } from "react";
import { AppService, LibraryService } from "@bindings/cmd/desktop";
import {
  ErrorCode,
  type Agent,
  type FetchedUnmanagedSkill,
  type PlannedUnmanagedSkill,
  type RegisteredProject,
  type Scope,
  type UnmanagedFetchItem,
} from "@bindings/internal/library";
import { describeError, unmanagedSourceError } from "@/lib/errors";
import { toLibraryError, type LibraryError } from "@/lib/library";
import { scopeOf } from "@/lib/model";
import { Modal, useUI } from "@/lib/ui";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { Diagnostics } from "./parts";

type UI = ReturnType<typeof useUI>;

// Where 是这次看的是哪里，决定标题和开头那句话。
export type Where =
  | { kind: "first-open" }
  | { kind: "project"; project: RegisteredProject }
  | { kind: "scope"; label: string };

// offerFirstOpen 在这台机器上第一次打开 app 时，把用户带到各 Agent 全局目录里不是库装的技能;没有就什么都不做。
export async function offerFirstOpen(ui: UI, agents: Agent[]) {
  if (!(await AppService.FirstOpen())) return;
  await offerFetchUnmanaged(
    ui,
    agents,
    agents.map((a) => scopeOf(a.code, 0)),
    { kind: "first-open" },
  );
}

// offerProject 在登记一个项目之后，把用户带到这个项目里各 Agent 目录下不是库装的技能;没有就什么都不做。
export async function offerProject(ui: UI, project: RegisteredProject) {
  const agents = (await LibraryService.ListAgents().catch(() => null)) ?? [];
  await offerFetchUnmanaged(
    ui,
    agents,
    agents.map((a) => scopeOf(a.code, project.id)),
    { kind: "project", project },
  );
}

// offerFetchUnmanaged 生成整批入库的计划并打开确认;quiet 为 false 时(用户自己点的)计划为空也告诉一声。
export async function offerFetchUnmanaged(
  ui: UI,
  agents: Agent[],
  scopes: Scope[],
  where: Where,
  quiet = true,
) {
  let plan: PlannedUnmanagedSkill[];
  try {
    plan = (await LibraryService.PlanFetchUnmanaged({ scopes })) ?? [];
  } catch (err) {
    const e = toLibraryError(err);
    if (e.code !== ErrorCode.Canceled && !quiet)
      ui.toast(describeError(e), "error");
    return;
  }
  if (plan.length === 0) {
    if (!quiet) ui.toast("这里没有不是库装的技能");
    return;
  }
  await ui.open((close) => (
    <FetchUnmanagedDialog
      agents={agents}
      plan={plan}
      where={where}
      close={close}
    />
  ));
}

const noSource = "";

// sourceKey 把来源与分支合成一个键：同一个来源只读一次，结果也按它分组。
function sourceKey(input?: string, ref?: string): string {
  return input ? `${input}\n${ref ?? ""}` : noSource;
}

function foldersOf(planned: PlannedUnmanagedSkill) {
  return planned.folders ?? [];
}

// keyOf 是计划里一条的稳定身份：第一处文件夹。
function keyOf(planned: PlannedUnmanagedSkill): string {
  return foldersOf(planned)[0]?.folder ?? planned.name;
}

// sourceOf 是一条最终用的来源：用户改过链接就用改过的(留空就是没有来源);来源记录里的分支只跟着它认出的那个来源走。
function sourceOf(
  planned: PlannedUnmanagedSkill,
  link?: string,
): { input?: string; ref?: string } {
  if (link === undefined)
    return { input: planned.source_input, ref: planned.source_ref };
  const input = link.trim();
  return {
    input: input || undefined,
    ref:
      input && input === planned.source_input ? planned.source_ref : undefined,
  };
}

function itemOf(
  planned: PlannedUnmanagedSkill,
  link?: string,
): UnmanagedFetchItem {
  const { input, ref } = sourceOf(planned, link);
  return {
    folders: foldersOf(planned).map((f) => ({ scope: f.scope, name: f.name })),
    source_input: input,
    source_ref: ref,
  };
}

// Failed 是没入库的一个技能：留着原来的那一项，用户可以改来源链接后重试。
interface Failed {
  key: string;
  planned: PlannedUnmanagedSkill;
  error: LibraryError;
  link: string;
  busy: boolean;
}

function FetchUnmanagedDialog({
  agents,
  plan,
  where,
  close,
}: {
  agents: Agent[];
  plan: PlannedUnmanagedSkill[];
  where: Where;
  close: () => void;
}) {
  const ui = useUI();
  const [picked, setPicked] = useState<Set<number>>(
    () => new Set(plan.flatMap((p, i) => (p.selected ? [i] : []))),
  );
  const [progress, setProgress] = useState<string>();
  const [fetched, setFetched] = useState<FetchedUnmanagedSkill[]>();
  const [failed, setFailed] = useState<Failed[]>([]);
  // links 是确认前改过的来源链接，按计划里的下标;editing 是正在改的那一条。
  const [links, setLinks] = useState<Map<number, string>>(new Map());
  const [editing, setEditing] = useState<number | null>(null);

  const agentNames = (planned: PlannedUnmanagedSkill) =>
    [
      ...new Set(
        foldersOf(planned).map(
          (f) =>
            agents.find((a) => a.code === f.scope.agent)?.name ?? f.scope.agent,
        ),
      ),
    ].join("、");

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

  const submit = async () => {
    // 按最终用的来源分批：同一个来源只读一次，读的时候告诉用户在等什么;没有来源的最后一批。
    const batches = new Map<string, number[]>();
    for (const i of chosen) {
      const { input, ref } = sourceOf(plan[i], links.get(i));
      const key = sourceKey(input, ref);
      batches.set(key, [...(batches.get(key) ?? []), i]);
    }
    const done: FetchedUnmanagedSkill[] = [];
    const lost: Failed[] = [];
    const fail = (i: number, error: LibraryError) =>
      lost.push({
        key: keyOf(plan[i]),
        planned: plan[i],
        error,
        link: sourceOf(plan[i], links.get(i)).input ?? "",
        busy: false,
      });
    for (const [key, members] of [...batches].sort(([a], [b]) =>
      a === noSource ? 1 : b === noSource ? -1 : 0,
    )) {
      setProgress(
        key === noSource
          ? "正在入库没有来源的技能…"
          : `正在读取 ${key.split("\n")[0]}…`,
      );
      try {
        const results =
          (await LibraryService.FetchUnmanagedSkills({
            skills: members.map((i) => itemOf(plan[i], links.get(i))),
          })) ?? [];
        results.forEach((r, n) =>
          r.error ? fail(members[n], r.error) : done.push(r),
        );
      } catch (err) {
        const error = toLibraryError(err);
        for (const i of members) fail(i, error);
      }
    }
    setProgress(undefined);
    ui.reload();
    if (lost.length === 0) {
      close();
      ui.toast(fetchedMessage(done));
      return;
    }
    setFetched(done);
    setFailed(lost);
  };

  // retry 用改过的来源链接重试一个没入库的技能;清空链接就作为没有来源的技能入库。按 key 找条目，几条一起重试也不会对错。
  const update = (key: string, change: Partial<Failed>) =>
    setFailed((list) =>
      list.map((x) => (x.key === key ? { ...x, ...change } : x)),
    );
  const retry = async (f: Failed) => {
    if (f.busy) return;
    update(f.key, { busy: true });
    try {
      const [r] =
        (await LibraryService.FetchUnmanagedSkills({
          skills: [itemOf(f.planned, f.link)],
        })) ?? [];
      if (r?.error) {
        update(f.key, { busy: false, error: r.error });
        return;
      }
      setFetched((list) => [...(list ?? []), r]);
      setFailed((list) => list.filter((x) => x.key !== f.key));
      ui.reload();
    } catch (err) {
      update(f.key, { busy: false, error: toLibraryError(err) });
    }
  };

  if (fetched) {
    return (
      <Modal
        title="还有几个没入库"
        wide
        onClose={close}
        footer={
          <Button variant="primary" onClick={close} autoFocus>
            完成
          </Button>
        }
      >
        {fetched.length > 0 && (
          <Notice tone="ok">{fetchedMessage(fetched)}</Notice>
        )}
        <Hint>
          下面 {failed.length} 个没入库，仍是未纳管技能，Agent
          照常在用。可以改来源链接重试，或清空链接作为没有来源的技能入库;也可以先放着，之后在作用域里逐个入库。
        </Hint>
        <ListBox scroll className="fetch-unmanaged-failed">
          {failed.map((f) => (
            <ListRow key={f.key} className="flex-col items-stretch gap-1.5">
              <div className="flex min-w-0 items-center gap-2">
                <b className="font-semibold">{f.planned.name}</b>
                <span className="min-w-0 truncate text-[11px] text-muted-foreground">
                  {agentNames(f.planned)}
                </span>
              </div>
              <p className="fetch-unmanaged-error text-[11.5px] whitespace-pre-line text-bad">
                {unmanagedSourceError(f.error, f.planned.name)}
              </p>
              <div className="flex min-w-0 items-center gap-2">
                <Input
                  className="fetch-unmanaged-link grow font-mono"
                  value={f.link}
                  placeholder="owner/repo、Git 地址或链接;留空就是没有来源"
                  onChange={(e) => update(f.key, { link: e.target.value })}
                  onKeyDown={(e) => e.key === "Enter" && void retry(f)}
                />
                <Button
                  className="fetch-unmanaged-retry"
                  loading={f.busy}
                  onClick={() => void retry(f)}
                >
                  {f.link.trim() ? "重试" : "不带来源入库"}
                </Button>
              </div>
            </ListRow>
          ))}
        </ListBox>
      </Modal>
    );
  }

  // changedSource 是改过来源的那一条最终跟着谁，没改过时为空。
  const changedSource = (i: number) => {
    if (!links.has(i)) return undefined;
    const { input } = sourceOf(plan[i], links.get(i));
    return input ? `改为跟着 ${input}` : "改为没有来源";
  };
  const sourceButton = (i: number) => (
    <button
      type="button"
      className="fetch-unmanaged-edit shrink-0 text-[11px] whitespace-nowrap text-subtle hover:text-primary"
      onClick={(e) => {
        e.preventDefault();
        if (!links.has(i))
          setLinks((map) => new Map(map).set(i, plan[i].source_input ?? ""));
        setEditing(editing === i ? null : i);
      }}
    >
      改来源
    </button>
  );
  const sourceEditor = (i: number) =>
    editing === i && (
      <ListRow compact className="pl-6">
        <Input
          className="fetch-unmanaged-source grow font-mono"
          autoFocus
          value={links.get(i) ?? ""}
          placeholder="owner/repo、Git 地址或链接;留空就是没有来源"
          onChange={(e) =>
            setLinks((map) => new Map(map).set(i, e.target.value))
          }
          onKeyDown={(e) => e.key === "Enter" && setEditing(null)}
        />
      </ListRow>
    );

  const row = (i: number) => {
    const p = plan[i];
    return (
      <div key={i}>
        <ListRow
          asChild
          compact
          clickable
          selected={picked.has(i)}
          className="pl-6"
        >
          <label title={p.description || undefined}>
            <input
              type="checkbox"
              className="shrink-0 accent-primary"
              checked={picked.has(i)}
              onChange={() => toggle(i)}
            />
            <b className="fetch-unmanaged-name shrink-0 font-semibold whitespace-nowrap">
              {p.name}
            </b>
            {p.in_repository && <Badge tone="warn">在项目仓库里</Badge>}
            {!p.in_repository && p.in_project_lock && (
              <Badge
                tone="warn"
                title="项目的 skills-lock.json 记了它，是团队约定装的"
              >
                记在 skills-lock.json 里
              </Badge>
            )}
            <span className="min-w-0 grow truncate text-[11px] text-muted-foreground">
              {agentNames(p)}
            </span>
            {changedSource(i) && (
              <span className="fetch-unmanaged-changed min-w-0 truncate text-[11px] text-primary">
                {changedSource(i)}
              </span>
            )}
            {sourceButton(i)}
          </label>
        </ListRow>
        {sourceEditor(i)}
      </div>
    );
  };

  const lead =
    where.kind === "first-open"
      ? `在各 Agent 的全局目录里发现 ${usable.length} 个不是库装的技能。`
      : where.kind === "project"
        ? `在项目「${where.project.name}」里发现 ${usable.length} 个不是库装的技能。`
        : `在 ${where.label} 里发现 ${usable.length} 个不是库装的技能。`;

  return (
    <Modal
      title="把你在用的技能交给 app 管"
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
            先不入库
          </Button>
          <Button
            className="fetch-unmanaged-confirm"
            variant="primary"
            disabled={picked.size === 0}
            loading={!!progress}
            onClick={() => void submit()}
            autoFocus
          >
            入库 {picked.size} 个
          </Button>
        </>
      }
    >
      <p className="fetch-unmanaged-lead text-[13px] font-semibold">
        {lead}入库时磁盘上什么都不改，Agent 用的和现在一样。
      </p>
      <Hint>
        入库就是交给 app 管：之后如果还用命令行工具更新这些技能，app
        只能把它们报为被手动改过。
      </Hint>
      {where.kind === "first-open" && (
        <Hint>项目里的技能，登记项目时再接。</Hint>
      )}
      {ordered.length > 0 && (
        <ListBox scroll className="fetch-unmanaged-groups">
          {ordered.map(([key, members]) => {
            const first = plan[members[0]];
            return (
              <div key={key} className="fetch-unmanaged-group">
                <div className="sticky top-0 z-1 flex min-w-0 items-center gap-1.5 border-t border-border-subtle bg-gray-50/95 px-2.5 py-1 text-[11px] font-semibold text-foreground-secondary first:border-t-0">
                  <span className="min-w-0 truncate font-mono">
                    {key === noSource ? "没有来源" : first.source_input}
                  </span>
                  {first.source_ref && (
                    <span className="shrink-0 font-normal text-muted-foreground">
                      {first.source_ref}
                    </span>
                  )}
                  <span className="shrink-0 font-normal text-muted-foreground">
                    · {members.length} 个
                  </span>
                  <span className="grow" />
                  <span className="shrink-0 font-normal text-muted-foreground">
                    {key === noSource
                      ? "不检查更新，在 Agent 目录里改了用那里的修改更新"
                      : "以后跟着它检查更新"}
                  </span>
                </div>
                {members.map(row)}
              </div>
            );
          })}
        </ListBox>
      )}
      {conflicts.size > 0 && (
        <div className="fetch-unmanaged-conflicts flex flex-col gap-1.5">
          <b className="text-xs">同名但内容不一样，每个只入库一份</b>
          <Hint>
            默认选最近修改的那份。没选的仍是未纳管技能，照样在原处、不被覆盖，之后想要哪份再入库。
          </Hint>
          <ListBox>
            {[...conflicts].map(([name, members]) => (
              <div key={name} className="fetch-unmanaged-conflict">
                <div className="border-t border-border-subtle bg-gray-50/95 px-2.5 py-1 text-[11px] font-semibold text-foreground-secondary first:border-t-0">
                  {name}
                </div>
                {members.map((i) => {
                  const p = plan[i];
                  return (
                    <div key={i}>
                      <ListRow
                        asChild
                        compact
                        clickable
                        selected={picked.has(i)}
                        className="pl-6"
                      >
                        <label title={p.description || undefined}>
                          <input
                            type="radio"
                            name={`conflict-${name}`}
                            className="shrink-0 accent-primary"
                            checked={picked.has(i)}
                            onChange={() => choose(members, i)}
                          />
                          <span className="min-w-0 truncate">
                            {agentNames(p)}
                          </span>
                          {p.in_repository && (
                            <Badge tone="warn">在项目仓库里</Badge>
                          )}
                          {!p.in_repository && p.in_project_lock && (
                            <Badge tone="warn">记在 skills-lock.json 里</Badge>
                          )}
                          <span className="grow" />
                          <span className="shrink-0 text-[11px] text-muted-foreground">
                            {p.modified_at
                              ? `${new Date(p.modified_at).toLocaleString()} 修改`
                              : ""}
                            {!links.has(i) && p.source_input
                              ? ` · 来自 ${p.source_input}`
                              : ""}
                          </span>
                          {changedSource(i) && (
                            <span className="min-w-0 truncate text-[11px] text-primary">
                              {changedSource(i)}
                            </span>
                          )}
                          {sourceButton(i)}
                        </label>
                      </ListRow>
                      {sourceEditor(i)}
                    </div>
                  );
                })}
                <ListRow asChild compact clickable className="pl-6">
                  <label>
                    <input
                      type="radio"
                      name={`conflict-${name}`}
                      className="shrink-0 accent-primary"
                      checked={!members.some((i) => picked.has(i))}
                      onChange={() => choose(members, null)}
                    />
                    <span className="text-muted-foreground">都先不入库</span>
                  </label>
                </ListRow>
              </div>
            ))}
          </ListBox>
        </div>
      )}
      {team.length > 0 && (
        <Notice tone="warn" className="fetch-unmanaged-team">
          {`勾上的${team.map((p) => `「${p.name}」`).join("")}归项目仓库管。`}
          之后在 app 里更新它们，会改动项目仓库里的文件。
        </Notice>
      )}
      {unusable.length > 0 && (
        <Diagnostics
          summary={`${unusable.length} 个不能入库`}
          items={unusable.map((p) => ({
            key: foldersOf(p)[0]?.folder ?? p.name,
            reason: `${p.name}:${unmanagedSourceError(p.error!, p.name)}`,
          }))}
        />
      )}
    </Modal>
  );
}

function fetchedMessage(done: FetchedUnmanagedSkill[]): string {
  const matched = done.filter((r) => r.matched).length;
  return (
    `已入库 ${done.length} 个技能，交给 app 管了` +
    (matched ? `;其中 ${matched} 个和库里已有的一样，只记下了装在这里` : "")
  );
}
