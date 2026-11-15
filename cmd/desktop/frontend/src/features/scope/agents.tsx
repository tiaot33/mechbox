// 自定义 Agent:添加、编辑(改目录时可选把装着的技能搬过去)、删除。
import { LibraryService } from "@bindings/cmd/desktop";
import { FolderPolicy, MigrationOutcome, type Agent, type CustomAgent, type Scope } from "@bindings/internal/library";
import { detachedBy } from "@/lib/consent";
import { messages } from "@/lib/i18n";
import { currentSnapshot, type LibraryError } from "@/lib/library";
import { folderPlaces, placeOf, projectName, shortPath, skillName } from "@/lib/model";
import { ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";
import { Notice } from "@/components/ui/notice";
import { DetachNotice, SharedFolders } from "@/components/app/link-notes";
import { showResults } from "@/components/app/results";
import { lostEdits } from "@/features/skill/remove";

export async function addAgent(input: CustomAgent, onInvalid: (err: LibraryError) => boolean): Promise<boolean> {
  const added = await ui.run(() => LibraryService.AddAgent(input), {
    success: (a) => messages().actions.agentAdded(a.name),
    onError: onInvalid,
  });
  return !!added;
}

const cleanPath = (path: string) => path.replace(/^\.\//, "").replace(/\/+$/, "");

// editAgent 修改自定义 Agent。某一层级的目录改了、这个 Agent 在那里已装了技能(含因共用目录而算已装的)或有组安装时，
// 先问是否迁移(不迁移会连组安装一起去掉,空组的组安装也是);迁移后列出因新目录已有同名文件夹而没搬的。input.global_directory 是展开 ~ 后的绝对路径。
export async function editAgent(agent: Agent, input: CustomAgent, onInvalid: (err: LibraryError) => boolean): Promise<boolean> {
  const s = currentSnapshot();
  const t = messages().actions;
  const globalChanged = cleanPath(input.global_directory) !== cleanPath(agent.global_directory);
  const projectChanged = cleanPath(input.project_directory) !== cleanPath(agent.project_directory);
  const changedHere = (scope: Scope) => scope.agent === agent.code && (placeOf(scope) ? projectChanged : globalChanged);
  const affected = s.skillScopes.filter((p) => changedHere(p.scope));
  const groupInstalls = s.groupInstalls.filter((g) => changedHere(g.scope)).length;
  let migrate = false;
  if (affected.length || groupInstalls) {
    const shared = affected.filter((p) => p.shared_with?.length);
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
          <DetachNotice
            items={detachedBy(folderPlaces(s, affected.filter((p) => !p.shared_with?.length)))}
            when="move"
            prefix={t.ifMigrate}
          />
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

// deleteAgent 删除自定义 Agent,和注销项目一样让用户选文件夹的去留;还有别的 Agent 在读的文件夹不管怎么选都保留。
export async function deleteAgent(agent: Agent) {
  const s = currentSnapshot();
  const places = s.skillScopes.filter((p) => p.scope.agent === agent.code);
  const kept = places.filter((p) => p.shared_with?.length);
  const own = places.filter((p) => !p.shared_with?.length);
  const t = messages().actions;
  const choice = await ui.confirm({
    title: t.deleteAgentTitle(agent.name),
    body: (
      <>
        <Hint>{t.deleteAgentHint}</Hint>
        {kept.length > 0 && (
          <>
            <Notice>{t.deleteAgentShared(kept.length)}</Notice>
            <SharedFolders places={kept} />
          </>
        )}
        <DetachNotice items={detachedBy(folderPlaces(s, own))} when="delete" prefix={t.ifRemoveToo} />
      </>
    ),
    choices: own.length
      ? [
          {
            value: FolderPolicy.Remove,
            label: t.removeInstalled,
            desc: t.removeFolders(own.length, lostEdits(own)),
          },
          { value: FolderPolicy.Keep, label: t.keepInstalled, desc: t.keepAgentFoldersDesc },
        ]
      : undefined,
    confirm: t.delete,
    danger: true,
  });
  if (!choice) return;
  const policy = choice === FolderPolicy.Keep ? FolderPolicy.Keep : FolderPolicy.Remove;
  await ui.run(() => LibraryService.DeleteAgent(agent.code, { folders: policy }), {
    success: t.agentDeleted(agent.name),
  });
}
