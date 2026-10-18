// 卸载 Mechbox：设置页最后一个分区打开。先现查各处安装的状态，说清会删什么、已安装的技能文件夹怎么处理
// (默认保留;移到废纸篓时可以留下被改过的几处),确认后由 Go 端删除系统钥匙串里的条目、移走文件夹，然后退出;
// 退出后删除数据库与设置，把应用移到废纸篓。失败时 app 不退出，这里写清现在的状态，可以重试。
import { useEffect, useState } from "react";
import { AppService, LibraryService, UninstallFolders, type ConfigStatus } from "@bindings/cmd/desktop";
import { SkillScopeStatus, type SkillScopeInspection } from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import type { LibraryError, Snapshot } from "@/lib/library";
import { projectName, scopeLabel, shortPath } from "@/lib/model";
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
  | { kind: "failed"; error: LibraryError; skippedKeychain: boolean }
  | { kind: "done" };

export function UninstallDialog({ s, onClose }: { s: Snapshot; onClose: () => void }) {
  const ui = useUI();
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
    { value: UninstallFolders.FoldersKeep, label: "保留已安装的文件夹", desc: "留在原处，Agent 照常使用;重装 Mechbox 后可以再入库，来源可能要重新填" },
    ...(modified.length
      ? [
          {
            value: UninstallFolders.FoldersKeepModified,
            label: `移到废纸篓，但保留被改过的 ${modified.length} 处`,
            desc: "被改过的留在原处，其余移到废纸篓，Agent 不再能用",
          },
        ]
      : []),
    {
      value: UninstallFolders.FoldersTrash,
      label: "全部移到废纸篓",
      desc: `Agent 不再能用这些技能${modified.length ? `;被改过的 ${modified.length} 处也一起移走` : ""}`,
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
    setPhase(error ? { kind: "failed", error, skippedKeychain: skipKeychain } : { kind: "idle" });
  };

  const goSync = () => {
    onClose();
    if (config) void ui.open((close) => <SyncDialog config={config} onClose={close} />);
  };

  const working = phase.kind === "working" || phase.kind === "done";
  return (
    <Modal
      title="卸载 Mechbox"
      onClose={() => !working && onClose()}
      footer={
        <>
          <Button disabled={working} onClick={onClose}>
            取消
          </Button>
          {phase.kind === "keychain" ? (
            <>
              <Button onClick={() => void execute(false)}>重试</Button>
              <Button variant="danger" onClick={() => void execute(true)}>
                跳过并继续卸载
              </Button>
            </>
          ) : (
            <Button variant="danger" disabled={!places || phase.kind === "done"} loading={phase.kind === "working"} onClick={() => void execute(false)}>
              {phase.kind === "failed" ? "重试" : "卸载并退出"}
            </Button>
          )}
        </>
      }
    >
      <Hint>
        删除这台机器上的技能库(库里的技能、版本历史与更新历史、组、登记的项目与自定义 Agent)、设置页里的设置(包括 GitHub
        与同步),以及系统钥匙串里的令牌与密钥;然后退出，把 Mechbox 移到废纸篓，没能移到废纸篓时会在 Finder 中选中它，拖进废纸篓即可。
      </Hint>
      {config &&
        (config.sync ? (
          <Notice className="flex items-center gap-2">
            <span className="grow">S3 上的库不动。卸载前可以先推送，重装后填好同步设置就能拉回。</span>
            <Button size="sm" disabled={working} onClick={goSync}>
              先去同步…
            </Button>
          </Notice>
        ) : (
          <Notice tone="warn">没有配置同步：卸载后技能库里的内容无法恢复。</Notice>
        ))}
      {!places ? (
        <Hint>
          <Spinner /> 正在查看各处已安装的技能文件夹…
        </Hint>
      ) : (
        present.length > 0 && (
          <>
            <Hint>
              已安装的技能文件夹：全局 {inGlobal} 个
              {inProjects > 0 && `;项目里 ${inProjects} 个(${projects.map((id) => projectName(s, id)).join("、")})`}。
              {offMachine > 0 && `另有 ${offMachine} 处在不在这台机器上的项目里，不受影响。`}不是库装的技能不受影响。
            </Hint>
            <ChoiceList choices={choices} value={folders} onChange={setFolders} />
            {moving > 0 && (
              <Hint>
                也包括入库前就在那里、后来交给 Mechbox 管理的文件夹。
                {inProjects > 0 && "项目里的文件夹如果已提交到仓库，移走后会出现在 git 改动里。"}
              </Hint>
            )}
            {modified.length > 0 && (
              <>
                <Notice tone="warn">
                  下面 {modified.length} 处被改过{folders === UninstallFolders.FoldersTrash ? "，会和其余的一起移到废纸篓" : ""}:
                </Notice>
                <ListBox>
                  {modified.map((p) => (
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
      {phase.kind === "working" && <Hint>{moving > 0 ? `正在把 ${moving} 个技能文件夹移到废纸篓…` : "正在卸载…"}</Hint>}
      {phase.kind === "keychain" && (
        <Notice tone="warn" className="whitespace-pre-line">
          没能删除系统钥匙串里的令牌和密钥(可能是拒绝了授权):{phase.message}
          {"\n"}Mechbox 还没卸载，什么都没动。可以重试;也可以跳过，之后在「钥匙串访问」里搜索 com.tiaot33.mechbox 手动删除。
        </Notice>
      )}
      {phase.kind === "failed" && (
        <Notice tone="bad" className="whitespace-pre-line">
          Mechbox 没有卸载，技能库和设置都还在:{describeError(phase.error)}
          {"\n"}已经移到废纸篓的文件夹可以从废纸篓放回，重试时跳过它们。
          {!phase.skippedKeychain && "系统钥匙串里的令牌和密钥已经删除，继续使用要在设置页重新填。"}
        </Notice>
      )}
      {phase.kind === "done" && <Notice tone="ok">已卸载，正在退出…</Notice>}
    </Modal>
  );
}
