// 比较弹窗：铺满窗口，左边列出改了的文件，右边是选中文件的逐行差异(左右对比或行内对比)。
// 技能详情的版本历史(某个版本与当前比较)、更新历史(一次更新或回滚改了什么)和更新计划里的差异(放大查看)都打开它。
import { useState, type ReactNode } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import type { VersionDiff, VersionID } from "@bindings/internal/library";
import { changedFiles } from "@/lib/diff";
import type { LibraryError } from "@/lib/library";
import { Modal, ui } from "@/lib/ui";
import { Hint } from "@/components/ui/field";
import { ChangedFiles, FileDiff, type FileReaders } from "./file-diff";

// CompareOptions 是比较弹窗的内容：title 是标题，note 写在标题下面;read 读改动前后的一个文件，要保持不变。
// path 是打开时选中的文件(默认 SKILL.md);badge 给某个文件加标记(如更新计划里的风险)。
// 读取失败时先交给 onError:它返回 true 表示已处理(如更新计划已失效，由下面的更新计划提示重新生成),比较弹窗随之关掉。
export interface CompareOptions {
  title: string;
  note?: ReactNode;
  diff: VersionDiff;
  read: FileReaders;
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
// signal 中止(如发起的窗口已经关了)时取消比较、不再打开弹窗。
export async function compareVersions({
  from,
  to,
  signal,
  ...rest
}: {
  title: string;
  note?: ReactNode;
  from: VersionID;
  to: VersionID;
  origin?: Element | null;
  signal?: AbortSignal;
}): Promise<void> {
  const call = LibraryService.DiffVersions(from, to);
  signal?.addEventListener("abort", () => void call.cancel(), { once: true });
  const diff = await ui.run(() => call, { reload: false });
  if (!diff || signal?.aborted) return;
  await openCompare({
    ...rest,
    diff,
    read: { before: (path) => LibraryService.GetVersionFile(from, path), after: (path) => LibraryService.GetVersionFile(to, path) },
  });
}

function Compare({ opts, onClose }: { opts: CompareOptions; onClose: () => void }) {
  const files = changedFiles(opts.diff);
  const [path, setPath] = useState(opts.path || files[0]?.path || "");
  const kind = files.find((f) => f.path === path)?.kind;
  const onError = (err: LibraryError) => {
    const handled = opts.onError?.(err) ?? false;
    if (handled) onClose();
    return handled;
  };
  return (
    <Modal title={opts.title} full onClose={onClose}>
      {opts.note && <Hint>{opts.note}</Hint>}
      <div className="version-compare flex min-h-0 grow basis-0 gap-3">
        {/* 文件列表随窗口变宽，长路径少截断。 */}
        <ChangedFiles
          files={files}
          path={path}
          onPath={setPath}
          badge={opts.badge}
          className="w-[clamp(240px,26%,420px)] shrink-0 overflow-y-auto"
        />
        <div className="flex min-w-0 grow flex-col">{kind && <FileDiff fill path={path} kind={kind} read={opts.read} onError={onError} />}</div>
      </div>
    </Modal>
  );
}
