// 删技能文件夹的写操作：确认框按界面上已经读到的状态写，弹出前不现查磁盘;技能库删之前核对要删的每个文件夹，
// 确认之后才被改过的一个都不删，这里写出是哪几处、修改会丢掉，再问一次。确认框里说过的那几处见 lib/consent.ts 的 discardedEdits。
import { CancelError } from "@wailsio/runtime";
import { ErrorCode } from "@bindings/internal/library";
import { messages, useT } from "@/lib/i18n";
import { toLibraryError, useSnapshot } from "@/lib/library";
import { scopeLabel } from "@/lib/places";
import { skillName } from "@/lib/scope";
import { shortPath, wslTitle } from "@/lib/paths";
import { ui, type RunOptions } from "@/lib/ui";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";

// runDiscardingEdits 经 ui.run 执行会删技能文件夹的写操作 call,把 discard(确认里说过修改会丢掉的文件夹)交给技能库。
// 确认框按界面上已经读到的状态写，弹出前不现查磁盘;技能库在删之前核对要删的每个文件夹，有确认之后才被改过的就一个都不删(Conflict),
// 这里写出是哪几处、修改会丢掉，再问一次：同意就带上它们重新调用，不同意就和取消一样什么都不做(返回 undefined,不提示)。
// 核对失败时技能库不删，ui.run 照常提示原因。
export function runDiscardingEdits<T>(discard: string[], call: (discard: string[]) => Promise<T>, opts?: RunOptions<T>): Promise<T | undefined> {
  return ui.run(async () => {
    let agreed = discard;
    for (;;) {
      try {
        return await call(agreed);
      } catch (e) {
        const err = toLibraryError(e);
        const edited = err.code === ErrorCode.Conflict ? (err.details?.paths ?? []) : [];
        if (!edited.length) throw e;
        if (!(await confirmJustEdited(edited))) throw new CancelError("kept the folders edited after the confirmation");
        agreed = [...agreed, ...edited];
      }
    }
  }, opts);
}

async function confirmJustEdited(folders: string[]) {
  const t = messages().actions;
  return !!(await ui.confirm({ title: t.justEditedTitle, body: <JustEdited folders={folders} />, confirm: t.deleteAnyway, danger: true }));
}

// JustEdited 列出确认之后才被改过的几个文件夹：技能 · 作用域与路径(共用一个文件夹的几处只写一处)。
function JustEdited({ folders }: { folders: string[] }) {
  const s = useSnapshot();
  const t = useT();
  return (
    <>
      <Notice alert>{t.actions.justEdited(folders.length)}</Notice>
      <ListBox>
        {folders.map((folder) => {
          const p = s.skillScopes.find((x) => x.folder === folder);
          return (
            <ListRow key={folder} compact className="just-edited">
              <div className="min-w-0 grow">
                {p && (
                  <div>
                    {skillName(s, p.skill_id)} · {scopeLabel(s, p.scope)}
                  </div>
                )}
                <div className="truncate font-mono text-caption text-muted-foreground" title={wslTitle(folder)}>
                  {shortPath(s, folder)}
                </div>
              </div>
            </ListRow>
          );
        })}
      </ListBox>
    </>
  );
}
