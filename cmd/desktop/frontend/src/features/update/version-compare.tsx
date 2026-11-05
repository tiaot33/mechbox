// 比较弹窗：铺满窗口，左边列出改了的文件，右边是选中文件的逐行差异(左右对比或行内对比)。
// 技能详情的版本历史(某个版本与当前比较)、更新历史(一次更新或回滚改了什么)和更新计划里的差异(放大查看)都打开它。
import { useCallback, useState, type ReactNode } from "react";
import type { CancellablePromise } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import type { File, VersionDiff, VersionID } from "@bindings/internal/library";
import type { LibraryError } from "@/lib/library";
import { Modal, ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";
import { ChangedFiles, changedFiles, FileDiff } from "./file-diff";

// CompareOptions 是比较弹窗的内容：title 是标题，note 写在标题下面;before / after 读改动前后的一个文件。
// path 是打开时选中的文件(默认 SKILL.md);badge 给某个文件加标记(如更新计划里的风险);读取失败时先交给 onError。
export interface CompareOptions {
  title: string;
  note?: ReactNode;
  diff: VersionDiff;
  before: (path: string) => CancellablePromise<File>;
  after: (path: string) => CancellablePromise<File>;
  path?: string;
  badge?: (path: string) => ReactNode;
  onError?: (err: LibraryError) => boolean;
  origin?: Element | null;
}

// openCompare 打开比较弹窗。
export async function openCompare(opts: CompareOptions): Promise<void> {
  await ui.open<void>((close) => <Compare opts={opts} onClose={() => close()} />, { origin: opts.origin });
}

// compareVersions 比较一个技能的两个版本：先比较 from 和 to 改了哪些文件(失败时按错误码提示，不打开弹窗),再打开比较弹窗。
export async function compareVersions({
  from,
  to,
  ...rest
}: { title: string; note?: ReactNode; from: VersionID; to: VersionID; origin?: Element | null }): Promise<void> {
  const diff = await ui.run(() => LibraryService.DiffVersions(from, to), { reload: false });
  if (!diff) return;
  await openCompare({
    ...rest,
    diff,
    before: (path) => LibraryService.GetVersionFile(from, path),
    after: (path) => LibraryService.GetVersionFile(to, path),
  });
}

function Compare({ opts, onClose }: { opts: CompareOptions; onClose: () => void }) {
  const files = changedFiles(opts.diff);
  const [path, setPath] = useState(opts.path || files[0]?.path || "");
  const kind = files.find((f) => f.path === path)?.kind;
  const before = useCallback(() => opts.before(path), [opts, path]);
  const after = useCallback(() => opts.after(path), [opts, path]);
  return (
    <Modal title={opts.title} full onClose={onClose}>
      {opts.note && <Hint>{opts.note}</Hint>}
      <div className="version-compare flex min-h-0 grow basis-0 gap-3">
        <ChangedFiles files={files} path={path} onPath={setPath} badge={opts.badge} className="w-60 shrink-0 overflow-y-auto" />
        <div className="flex min-w-0 grow flex-col">
          {kind && (
            <FileDiff
              fill
              path={path}
              before={kind === "added" ? null : before}
              after={kind === "deleted" ? null : after}
              onError={opts.onError}
            />
          )}
        </div>
      </div>
    </Modal>
  );
}
