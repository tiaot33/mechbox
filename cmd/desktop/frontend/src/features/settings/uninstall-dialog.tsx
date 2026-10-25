// 卸载 Mechbox：设置页最后一个分区打开。先现查各处安装的状态，说清会删什么、已安装的技能文件夹怎么处理
// (默认保留;移到废纸篓(Windows、Linux 上是回收站)时可以留下被改过的几处),确认后由 Go 端移走文件夹，然后退出;
// 退出后删除数据库(设置都在里面),macOS 上把应用移到废纸篓。失败时 app 不退出，这里写清现在的状态，可以重试。
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AppService, LibraryService, UninstallFolders } from "@bindings/cmd/desktop";
import { SkillScopeStatus, type SkillScopeInspection } from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import { useT } from "@/lib/i18n";
import { useSnapshot, configQuery, type LibraryError } from "@/lib/library";
import { mergeProjectPlaces, projectName, scopeLabel, shortPath } from "@/lib/model";
import { isWSLPath } from "@/lib/paths";
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
  | { kind: "failed"; error: LibraryError; trashing: boolean }
  | { kind: "done" };

export function UninstallDialog({ onClose }: { onClose: () => void }) {
  const s = useSnapshot();
  const ui = useUI();
  const t = useT().uninstall;
  const [places, setPlaces] = useState<SkillScopeInspection[]>();
  const config = useQuery(configQuery).data;
  const [folders, setFolders] = useState<string>(UninstallFolders.FoldersKeep);
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });

  // 打开时现查磁盘：快照里的状态可能落后，用户刚改过的文件夹要算进被改过的几处。
  const inspect = () => ui.run(() => LibraryService.InspectSkillScopes({}), { reload: false }).then((p) => p && setPlaces(p));
  useEffect(() => void inspect(), []);

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
  const movingInDistro = present.filter(
    (p) => isWSLPath(p.folder) && (folders === UninstallFolders.FoldersTrash || (folders === UninstallFolders.FoldersKeepModified && p.status !== SkillScopeStatus.SkillScopeModified)),
  ).length;

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

  const execute = async () => {
    setPhase({ kind: "working" });
    let error: LibraryError | undefined;
    const result = await ui.run(() => AppService.UninstallApp({ folders: folders as UninstallFolders }), {
      reload: false,
      onError: (err) => {
        error = err;
        return true;
      },
    });
    if (result?.uninstalled) return setPhase({ kind: "done" });
    // 失败时 app 不退出：移走了一部分文件夹，界面与这里的计数都要跟上。
    ui.reload();
    void inspect();
    setPhase(error ? { kind: "failed", error, trashing: folders !== UninstallFolders.FoldersKeep } : { kind: "idle" });
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
          <Button variant="danger" disabled={!places || phase.kind === "done"} loading={phase.kind === "working"} onClick={() => void execute()}>
            {phase.kind === "failed" ? t.retry : t.uninstall}
          </Button>
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
            {movingInDistro > 0 && <Hint>{t.distroTrash(movingInDistro)}</Hint>}
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
      {phase.kind === "failed" && (
        <Notice tone="bad" className="whitespace-pre-line">
          {t.failed(describeError(phase.error), phase.trashing)}
        </Notice>
      )}
      {phase.kind === "done" && <Notice tone="ok">{t.done}</Notice>}
    </Modal>
  );
}
