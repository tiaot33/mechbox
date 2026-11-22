// Agent:从候选池添加、手填添加、编辑(去掉的目录里装着技能时可选把它们搬到新的第 1 个目录)、移除(回到候选池)。
import { LibraryService } from "@bindings/cmd/desktop";
import { FolderPolicy, type Agent, type AgentInput } from "@bindings/internal/library";
import { detachedBy, discardedEdits, lostEdits } from "@/lib/consent";
import { messages, useT } from "@/lib/i18n";
import { currentSnapshot, useSnapshot, type LibraryError } from "@/lib/library";
import { folderPlaces } from "@/lib/installs";
import { scopeLabel } from "@/lib/places";
import { nativePath, shortPath, wslTitle } from "@/lib/paths";
import { placeOf, skillName } from "@/lib/scope";
import { ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";
import { Notice } from "@/components/ui/notice";
import { ListBox, ListRow } from "@/components/ui/list";
import { AgentMark } from "@/components/app/parts";
import { DetachNotice, SharedFolders, StaleNotes } from "@/components/app/link-notes";
import { runDiscardingEdits } from "@/components/app/discard-edits";
import { showResults } from "@/components/app/results";
import { agentDelete, agentEdit, agentReset, migrationReport, type AgentChange } from "./agents-rules";

// addAgentsFromPool 一次添加候选池里勾选的几个 Agent。
export async function addAgentsFromPool(agents: Agent[]): Promise<boolean> {
  const t = messages().actions;
  const added = await ui.run(() => LibraryService.AddAgentsFromPool(agents.map((a) => a.code)), {
    success: () => t.agentsAdded(agents.map((a) => a.name)),
  });
  return added !== undefined;
}

export async function addAgent(input: AgentInput, onInvalid: (err: LibraryError) => boolean): Promise<boolean> {
  const added = await ui.run(() => LibraryService.AddAgent(input), {
    success: (a) => messages().actions.agentAdded(a.name),
    onError: onInvalid,
  });
  return !!added;
}

// editAgent 修改 Agent。有安装所在的目录被去掉(含因共用目录而算已装的),或原来的第 1 个目录被去掉、那一层级有组安装时，
// 先问是否迁移(不迁移会连组安装一起去掉,空组的组安装也是);所在目录还在新列表里的磁盘不动，不问。
// 迁移后列出因新目录已有同名文件夹而没搬的。input 的全局目录是展开 ~ 后的绝对路径。
export async function editAgent(agent: Agent, input: AgentInput, onInvalid: (err: LibraryError) => boolean): Promise<boolean> {
  const s = currentSnapshot();
  const t = messages().actions;
  const { affected, groupInstalls, asks, shared, detached } = agentEdit(s, agent, input);
  let migrate = false;
  if (asks) {
    const choice = await ui.confirm({
      title: t.editAgentTitle(agent.name),
      body: (
        <>
          <Hint>{t.editAgentHint(affected.length, groupInstalls)}</Hint>
          {shared.length > 0 && <Notice>{t.editAgentShared(shared.length)}</Notice>}
          {affected.length > 0 && <SharedFolders places={affected} />}
          <DetachNotice items={detached} when="move" prefix={t.ifMigrate} />
        </>
      ),
      choices: [
        { value: "migrate", label: t.migrate, desc: t.migrateDesc },
        { value: "keep", label: t.noMigrate, desc: t.noMigrateDesc },
      ],
      confirm: t.save,
    });
    if (!choice) return false;
    migrate = choice === "migrate";
  }
  const result = await ui.run(() => LibraryService.EditAgent(agent.code, input, { migrate }), { onError: onInvalid });
  if (!result) return false;
  const name = result.agent.name;
  const report = migrationReport(s, result.migrations ?? []);
  if (report.listed) {
    await showResults(t.agentSaved(name), report.rows, t.migratedPartly(report.migrated, report.skipped));
  } else {
    ui.toast(report.migrated ? t.agentSavedMigrated(name, report.migrated) : t.agentSaved(name));
  }
  return true;
}

// removeAgent 移除 Agent:它回到候选池。和注销项目一样让用户选文件夹的去留，默认保留(从 Mechbox 移除不等于卸载这个 Agent);
// 被改过的几处按界面上已经读到的状态说，确认之后才被改过的由技能库删之前核对出来再问一次;
// 还有别的 Agent 在读的文件夹不管怎么选都保留，选删除时写出会一并删掉的旧链接。
export async function removeAgent(agent: Agent) {
  const s = currentSnapshot();
  const { kept, own } = agentDelete(s, agent.code);
  const t = messages().actions;
  const choice = await ui.confirm({
    title: t.removeAgentTitle(agent.name),
    body: (
      <>
        <Hint>{t.removeAgentHint}</Hint>
        {kept.length > 0 && (
          <>
            <Notice>{t.removeAgentShared(kept.length)}</Notice>
            <SharedFolders places={kept} />
          </>
        )}
        <DetachNotice items={detachedBy(folderPlaces(s, own))} when="delete" prefix={t.ifRemoveToo} />
        <StaleNotes deleted={own} prefix={t.ifRemoveToo} copies={false} />
      </>
    ),
    choices: own.length
      ? [
          { value: FolderPolicy.Keep, label: t.keepInstalled, desc: t.keepAgentFoldersDesc },
          {
            value: FolderPolicy.Remove,
            label: t.removeInstalled,
            desc: t.removeFolders(own.length, lostEdits(own).length),
          },
        ]
      : undefined,
    confirm: t.removeAgent,
    danger: true,
  });
  if (!choice) return;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  await runDiscardingEdits(discardedEdits(own), (discard) => LibraryService.DeleteAgent(agent.code, { folders: policy, discard_edits: discard }), {
    success: t.agentRemoved(agent.name),
  });
}

// resetAgents 是设置页 Agent 分区的「恢复默认」:先说清会改哪些 Agent、移除哪些手填的 Agent、哪些技能要迁移、哪些文件夹删除或保留，
// 用户确认后才动磁盘和数据库(技能库的 ResetAgents)。手填 Agent 只有它读的文件夹和移除 Agent 一样让用户选，默认保留;
// 迁移之后没迁移成的列出来。已经和内置的默认一样时只提示。
export async function resetAgents() {
  const s = currentSnapshot();
  const t = messages().agents;
  const a = messages().actions;
  const read = await ui.read(() => Promise.all([LibraryService.ListAgentDefaults(), LibraryService.ListAgentPool()]));
  if (!read) return;
  const plan = agentReset(s, read[0] ?? [], read[1] ?? []);
  if (plan.nothing) {
    ui.toast(t.resetNothing);
    return;
  }
  const choice = await ui.confirm({
    title: t.resetTitle,
    body: <ResetBody plan={plan} />,
    choices: plan.own.length
      ? [
          { value: FolderPolicy.Keep, label: a.keepInstalled, desc: t.resetKeepDesc },
          { value: FolderPolicy.Remove, label: a.removeInstalled, desc: a.removeFolders(plan.own.length, lostEdits(plan.own).length) },
        ]
      : undefined,
    confirm: t.resetAgents,
    danger: plan.removed.length > 0,
  });
  if (!choice) return;
  const policy = choice === FolderPolicy.Remove ? FolderPolicy.Remove : FolderPolicy.Keep;
  const result = await runDiscardingEdits(discardedEdits(plan.own), (discard) => LibraryService.ResetAgents({ folders: policy, discard_edits: discard }));
  if (!result) return;
  const done = t.resetDone((result.reset ?? []).length, (result.removed ?? []).length);
  const report = migrationReport(currentSnapshot(), result.migrations ?? []);
  if (report.listed) await showResults(done, report.rows, a.migratedPartly(report.migrated, report.skipped));
  else ui.toast(done);
}

type ResetPlan = ReturnType<typeof agentReset>;

function ResetBody({ plan }: { plan: ResetPlan }) {
  const s = useSnapshot();
  const t = useT().agents;
  const a = useT().actions;
  const dirs = (list: string[] | null | undefined, project: boolean) => (list ?? []).map((d) => (project ? nativePath(d) : shortPath(s, d))).join("、");
  const field = (c: AgentChange, f: AgentChange["fields"][number]) =>
    f === "name" ? t.resetField(f, c.agent.name, c.defaults.name) : t.resetField(f, dirs(c.agent[f], f === "project_directories"), dirs(c.defaults[f], f === "project_directories"));
  const heading = (text: string) => <div className="mt-1 text-body font-semibold">{text}</div>;
  return (
    <>
      <Hint>{t.resetLead}</Hint>
      {plan.changed.length > 0 && (
        <>
          {heading(t.resetChanged(plan.changed.length))}
          <ListBox className="reset-changed">
            {plan.changed.map((c) => (
              <ListRow key={c.agent.code} className="items-start">
                <AgentMark name={c.agent.name} />
                <div className="min-w-0 grow">
                  <div>{c.agent.name}</div>
                  {c.fields.map((f) => (
                    <div key={f} className="font-mono text-caption break-all text-muted-foreground">
                      {field(c, f)}
                    </div>
                  ))}
                </div>
              </ListRow>
            ))}
          </ListBox>
        </>
      )}
      {plan.changedPooled.length > 0 && <Hint>{t.resetChangedPooled(plan.changedPooled.map((c) => c.agent.name))}</Hint>}
      {plan.removed.length > 0 && (
        <>
          {heading(t.resetRemoved(plan.removed.length))}
          <ListBox className="reset-removed">
            {plan.removed.map((agent) => (
              <ListRow key={agent.code}>
                <AgentMark name={agent.name} />
                <div className="min-w-0 grow">
                  <div>{agent.name}</div>
                  <div className="truncate font-mono text-caption text-muted-foreground">{dirs(agent.global_directories, false)}</div>
                </div>
              </ListRow>
            ))}
          </ListBox>
        </>
      )}
      {plan.removedPooled.length > 0 && <Hint>{t.resetRemovedPooled(plan.removedPooled.map((x) => x.name))}</Hint>}
      {plan.migrations.length > 0 && (
        <>
          {heading(t.resetMigrations(plan.migrations.length))}
          <ListBox className="reset-migrations">
            {plan.migrations.map(({ place, to }) => (
              <ListRow key={`${place.skill_id}:${place.scope.agent}:${place.scope.project_id ?? 0}`}>
                <div className="min-w-0 grow">
                  <div>
                    {skillName(s, place.skill_id)} · {scopeLabel(s, place.scope)}
                  </div>
                  <div className="truncate font-mono text-caption text-muted-foreground" title={wslTitle(place.folder)}>
                    {t.resetMigrateTo(shortPath(s, place.folder), placeOf(place.scope) ? nativePath(to) : shortPath(s, to))}
                  </div>
                </div>
              </ListRow>
            ))}
          </ListBox>
          <DetachNotice items={plan.detached} when="move" />
        </>
      )}
      {plan.kept.length > 0 && (
        <>
          <Notice>{a.removeAgentShared(plan.kept.length)}</Notice>
          <SharedFolders places={plan.kept} />
        </>
      )}
      <DetachNotice items={detachedBy(folderPlaces(s, plan.own))} when="delete" prefix={a.ifRemoveToo} />
      <StaleNotes deleted={plan.own} prefix={a.ifRemoveToo} copies={false} />
    </>
  );
}
