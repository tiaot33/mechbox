// 比较弹窗：铺满窗口(不透明的浅灰底),左边是改了的文件列表、右边是选中文件的差异，各是一块撑满到底的白底面板(文件列表可以用标题前的按钮收起，每次打开都展开;差异可以左右对比或行内对比);
// 两个版本内容相同时只说明内容相同。
// 技能详情的版本历史(某个版本与当前比较)、更新历史(一次更新或回滚改了什么)和更新计划里的差异(放大查看)都打开它。
import { useMemo, useState, type ReactNode } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import type { VersionDiff, VersionID } from "@bindings/internal/library";
import { changedFiles } from "@/lib/diff";
import { useT } from "@/lib/i18n";
import type { LibraryError } from "@/lib/library";
import { Modal, ui } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { Icon } from "@/components/app/icons";
import { Button } from "@/components/ui/button";
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
  const files = useMemo(() => changedFiles(opts.diff), [opts.diff]);
  const [path, setPath] = useState(opts.path || files[0]?.path || "");
  const kind = files.find((f) => f.path === path)?.kind;
  const onError = (err: LibraryError) => {
    const handled = opts.onError?.(err) ?? false;
    if (handled) onClose();
    return handled;
  };
  const tt = useT();
  // 文件列表每次打开都展开，不记上次收没收起：收起时看不到还改了哪些文件(新增、删除、风险标记),免得以后打开时漏看。
  const [shown, setShown] = useState(true);
  const empty = files.length === 0;
  return (
    <Modal
      title={opts.title}
      leading={
        !empty && (
          <Button
            variant="ghost"
            size="icon"
            className={cn("-ml-1.5", shown && "text-foreground")}
            aria-pressed={shown}
            title={tt.diff.toggleFiles}
            onClick={() => setShown(!shown)}
          >
            <Icon name="sidebarL" />
          </Button>
        )
      }
      full
      onClose={onClose}
    >
      {opts.note && <Hint className="px-1.5">{opts.note}</Hint>}
      <div className="version-compare flex min-h-0 grow basis-0 gap-1.5">
        {/* 文件列表和差异面板是并排的两块白底面板;文件列表随窗口略微变宽，长路径少截断。收起时差异面板铺满整宽。 */}
        {shown && !empty && (
          <ChangedFiles
            files={files}
            path={path}
            onPath={setPath}
            badge={opts.badge}
            className="w-[clamp(200px,20%,280px)] shrink-0 overflow-y-auto rounded-[10px] bg-white p-1.5 shadow-card"
          />
        )}
        <div className="flex min-w-0 grow flex-col">
          {/* 两个版本内容相同时没有文件可看，在这里直接说明。 */}
          {empty ? (
            <div className="flex grow items-center justify-center rounded-[10px] bg-white text-[12px] text-muted-foreground shadow-card">
              {tt.common.sameContent}
            </div>
          ) : (
            kind && <FileDiff fill path={path} kind={kind} read={opts.read} onError={onError} />
          )}
        </div>
      </div>
    </Modal>
  );
}
