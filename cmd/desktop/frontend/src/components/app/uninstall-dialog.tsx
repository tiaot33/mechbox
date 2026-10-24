// 卸载 Mechbox：设置页最后一个分区打开。先现查各处安装的状态，说清会删什么、已安装的技能文件夹怎么处理
// (默认保留;移到废纸篓(Windows、Linux 上是回收站)时可以留下被改过的几处),确认后由 Go 端删除系统钥匙串里的条目、移走文件夹，然后退出;
// 退出后删除数据库与设置,macOS 上把应用移到废纸篓。失败时 app 不退出，这里写清现在的状态，可以重试。
import { useEffect, useState } from "react";
import { AppService, LibraryService, UninstallFolders, type ConfigStatus } from "@bindings/cmd/desktop";
import { SkillScopeStatus, type SkillScopeInspection } from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import { useT } from "@/lib/i18n";
import type { LibraryError, Snapshot } from "@/lib/library";
import { mergeProjectPlaces, projectName, scopeLabel, shortPath } from "@/lib/model";
import { ChoiceList, Modal, useUI, type Choice } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { Spinner } from "@/components/ui/spinner";
import { SyncDialog } from "./sync-dialog";

type Phase =
  | { kind: "idle" }
  | { kind: "working" }
  | { kind: "keychain"; message: string }
  | { kind: "failed"; error: LibraryError; skippedKeychain: boolean; trashing: boolean }
  | { kind: "done" };

export function UninstallDialog({ s, onClose }: { s: Snapshot; onClose: () => void }) {
  const ui = useUI();
  const t = useT().uninstall;
  const [places, setPlaces] = useState<SkillScopeInspection[]>();
  const [config, setConfig] = useState<ConfigStatus>();
  const [folders, setFolders] = useState<string>(UninstallFolders.FoldersKeep);
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });

  // 打开时现查磁盘：快照里的状态可能落后，用户刚改过的文件夹要算进被改过的几处。
  const inspect = () => ui.run(() => LibraryService.InspectSkillScopes({}), { reload: false }).then((p) => p && setPlaces(p));
  useEffect(() => {
    void inspect();
    const call = LibraryService.Config();
    call.then(setConfig, () => {});
    return () => void call.cancel();
  }, []);

  const byFolder = new Map<string, SkillScopeInspection>();
  for (const p of places ?? []) if (!byFolder.has(p.folder)) byFolder.set(p.folder, p);
  const unique = [...byFolder.values()];
  const present = unique.filter((p) => p.status !== SkillScopeStatus.SkillScopeMissing && p.status !== SkillScopeStatus.SkillScopeRootMissing);
  const offMachine = unique.filter((p) => p.status === SkillScopeStatus.SkillScopeRootMissing).length;
  const modified = present.filter((p) => p.status === SkillScopeStatus.SkillScopeModified);
  const inGlobal = present.filter((p) => !p.scope.project_id).length;
  const projects = [...new Set(present.flatMap((p) => (p.scope.project_id ? [p.scope.project_id] : [])))];
  const inProjects = present.length - inGlobal;
  const moving = folders === UninstallFolders.FoldersTrash ? present.length : folders === UninstallFolders.FoldersKeepModified ? present.length - modified.length : 0;

  const choices: Choice[] = [
    { value: UninstallFolders.FoldersKeep, label: t.keep, desc: t.keepDesc },
    ...(modified.length
      ? [
          {
            value: UninstallFolders.FoldersKeepModified,
            label: t.keepModified(modified.length),
            desc: t.keepModifiedDesc(),
          },
        ]
      : []),
    {
      value: UninstallFolders.FoldersTrash,
      label: t.trash(),
      desc: t.trashDesc(modified.length),
    },
  ];

  const execute = async (skipKeychain: boolean) => {
    setPhase({ kind: "working" });
    let error: LibraryError | undefined;
    const result = await ui.run(() => AppService.UninstallApp({ folders: folders as UninstallFolders, skip_keychain: skipKeychain }), {
      reload: false,
      onError: (err) => {
        error = err;
        return true;
      },
    });
    if (result?.uninstalled) return setPhase({ kind: "done" });
    if (result?.keychain_error) return setPhase({ kind: "keychain", message: result.keychain_error });
    // 失败时 app 不退出：移走了一部分文件夹，界面与这里的计数都要跟上。
    ui.reload();
    void inspect();
    setPhase(error ? { kind: "failed", error, skippedKeychain: skipKeychain, trashing: folders !== UninstallFolders.FoldersKeep } : { kind: "idle" });
  };

  const goSync = () => {
    onClose();
    if (config) void ui.open((close) => <SyncDialog config={config} onClose={close} />);
  };

  const working = phase.kind === "working" || phase.kind === "done";
  return (
    <Modal
      title={t.title}
      onClose={() => !working && onClose()}
      footer={
        <>
          <Button disabled={working} onClick={onClose}>
            {t.cancel}
          </Button>
          {phase.kind === "keychain" ? (
            <>
              <Button onClick={() => void execute(false)}>{t.retry}</Button>
              <Button variant="danger" onClick={() => void execute(true)}>
                {t.skipKeychain}
              </Button>
            </>
          ) : (
            <Button variant="danger" disabled={!places || phase.kind === "done"} loading={phase.kind === "working"} onClick={() => void execute(false)}>
              {phase.kind === "failed" ? t.retry : t.uninstall}
            </Button>
          )}
        </>
      }
    >
      <Hint>{t.what()}</Hint>
      {config &&
        (config.sync ? (
          <Notice className="flex items-center gap-2">
            <span className="grow">{t.syncKept}</span>
            <Button size="sm" disabled={working} onClick={goSync}>
              {t.goSync}
            </Button>
          </Notice>
        ) : (
          <Notice tone="warn">{t.noSync}</Notice>
        ))}
      {!places ? (
        <Hint>
          <Spinner /> {t.inspecting}
        </Hint>
      ) : (
        present.length > 0 && (
          <>
            <Hint>
              {t.folders(
                inGlobal,
                inProjects,
                projects.map((id) => projectName(s, id)),
                offMachine,
              )}
            </Hint>
            <ChoiceList choices={choices} value={folders} onChange={setFolders} />
            {moving > 0 && (
              <Hint>{t.adopted(inProjects > 0)}</Hint>
            )}
            {modified.length > 0 && (
              <>
                <Notice tone="warn">
                  {t.modified(modified.length, folders === UninstallFolders.FoldersTrash)}
                </Notice>
                <ListBox>
                  {mergeProjectPlaces(s, modified).map((p) => (
                    <ListRow key={p.folder}>
                      <div className="min-w-0 grow">
                        <div>
                          {s.skills.find((x) => x.id === p.skill_id)?.name ?? `#${p.skill_id}`} · {scopeLabel(s, p.scope)}
                        </div>
                        <div className="truncate font-mono text-[11px] text-muted-foreground">{shortPath(s, p.folder)}</div>
                      </div>
                    </ListRow>
                  ))}
                </ListBox>
              </>
            )}
          </>
        )
      )}
      {phase.kind === "working" && <Hint>{moving > 0 ? t.trashing(moving) : t.uninstalling}</Hint>}
      {phase.kind === "keychain" && (
        <Notice tone="warn" className="whitespace-pre-line">
          {t.keychainFailed(phase.message)}
        </Notice>
      )}
      {phase.kind === "failed" && (
        <Notice tone="bad" className="whitespace-pre-line">
          {t.failed(describeError(phase.error), !phase.skippedKeychain, phase.trashing)}
        </Notice>
      )}
      {phase.kind === "done" && <Notice tone="ok">{t.done}</Notice>}
    </Modal>
  );
}
