// Agent:从候选池添加、手填添加、编辑(去掉的目录里装着技能时可选把它们搬到新的第 1 个目录)、移除(回到候选池)。
import { LibraryService } from "@bindings/cmd/desktop";
import { FolderPolicy, MigrationOutcome, type Agent, type AgentInput } from "@bindings/internal/library";
import { detachedBy, discardedEdits, lostEdits } from "@/lib/consent";
import { messages } from "@/lib/i18n";
import { currentSnapshot, type LibraryError } from "@/lib/library";
import { folderPlaces } from "@/lib/installs";
import { projectName, skillName } from "@/lib/scope";
import { shortPath } from "@/lib/paths";
import { ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";
import { Notice } from "@/components/ui/notice";
import { DetachNotice, SharedFolders, StaleNotes } from "@/components/app/link-notes";
import { runDiscardingEdits } from "@/components/app/discard-edits";
import { showResults } from "@/components/app/results";
import { agentDelete, agentEdit } from "./agents-rules";

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
          {shared.length > 0 && (
            <>
              <Notice>{t.editAgentShared(shared.length)}</Notice>
              <SharedFolders places={shared} />
            </>
          )}
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
  const migrations = result.migrations ?? [];
  const skipped = migrations.filter((m) => m.outcome === MigrationOutcome.MigrationSkipped);
  if (skipped.length) {
    await showResults(
      t.agentSaved(name),
      skipped.map((m) => ({
        label: `${skillName(s, m.skill_id)} · ${m.scope.project_id ? projectName(s, m.scope.project_id) : messages().model.global}`,
        outcome: t.notMigrated,
        tone: "warn" as const,
        reason: t.notMigratedReason(shortPath(s, m.to), shortPath(s, m.from)),
      })),
      t.migratedPartly(migrations.length - skipped.length, skipped.length),
    );
  } else {
    ui.toast(migrations.length ? t.agentSavedMigrated(name, migrations.length) : t.agentSaved(name));
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
