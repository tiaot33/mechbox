// 一个文件的差异：旧内容和新内容逐行比较，所有文件(包括 SKILL.md)都按代码展示(等宽、带 +/-)。
// 可以切换左右对比和行内对比(新增、删除的文件只有一侧，铺满整宽),选过的布局存在 localStorage,各处的差异共用、下次打开沿用。
// 左右对比时拖选只选按下的那一栏。
// ChangedFiles 是改了哪些文件的列表，点文件看它的差异;更新计划和比较弹窗共用。
import { useEffect, useRef, useState, type ReactNode } from "react";
import type { CancellablePromise } from "@wailsio/runtime";
import { create } from "zustand";
import type { File } from "@bindings/internal/library";
import { decodeContent } from "@/lib/content";
import { useT } from "@/lib/i18n";
import { toLibraryError, type LibraryError } from "@/lib/library";
import { lineDiff, splitRows, type ChangeKind, type DiffLine, type SplitRow } from "@/lib/diff";
import { cn } from "@/lib/utils";
import { Notice } from "@/components/ui/notice";
import { Segmented } from "@/components/ui/segmented";
import { Spinner } from "@/components/ui/spinner";

type Layout = "split" | "unified";

// FileReaders 读改动前后的一个文件(如库里的版本、更新计划里来源的新内容)。
export interface FileReaders {
  before: (path: string) => CancellablePromise<File>;
  after: (path: string) => CancellablePromise<File>;
}

const layoutKey = "mechbox.diff-layout";
const useLayoutStore = create<{ layout: Layout }>(() => ({ layout: localStorage.getItem(layoutKey) === "unified" ? "unified" : "split" }));
const setLayout = (layout: Layout) => {
  localStorage.setItem(layoutKey, layout);
  useLayoutStore.setState({ layout });
};

const changeColor = { added: "text-ok-strong", changed: "text-warn-strong", deleted: "text-bad-strong" };

// ChangedFiles 列出改了的文件，选中的一项高亮;badge 给某个文件加标记(如更新计划里的风险)。
export function ChangedFiles({
  files,
  path,
  onPath,
  badge,
  className,
}: {
  files: { kind: ChangeKind; path: string }[];
  path: string;
  onPath: (path: string) => void;
  badge?: (path: string) => ReactNode;
  className?: string;
}) {
  const tt = useT();
  const t = tt.diff;
  return (
    <div className={cn("flex flex-col", className)}>
      {files.map((f) => (
        <button
          key={f.path}
          type="button"
          data-diff={f.kind}
          className={cn(
            "flex items-center gap-2 rounded-md px-1.5 py-0.5 text-left font-mono text-[11px] hover:bg-black/[0.04]",
            changeColor[f.kind],
            f.path === path && "bg-primary-soft hover:bg-primary-soft",
          )}
          onClick={() => onPath(f.path)}
        >
          <span className="w-7 shrink-0 font-sans [&:lang(en)]:w-14">{t[f.kind]}</span>
          <span className="min-w-0 grow truncate" title={f.path}>
            {f.path}
          </span>
          {badge?.(f.path)}
        </button>
      ))}
      {files.length === 0 && <span className="text-[11px] text-muted-foreground">{tt.common.sameContent}</span>}
    </div>
  );
}

// FileDiff 是 path 这个文件的差异，kind 是它改了什么：新增的只读改动后，删除的只读改动前;只有一侧的文件不分两栏，铺满整宽。
// read 要在调用方保持不变(换了才重读)。读取失败时先交给 onError,它返回 true 表示已处理(如更新计划已失效)。
// 布局切换只在两侧都能逐行比较时显示;fill 为 true 时差异最高到外面给的高度(比较弹窗),否则最高 360px;内容短时都按内容的高度。
export function FileDiff({
  path,
  kind,
  read,
  onError,
  fill = false,
}: {
  path: string;
  kind: ChangeKind;
  read: FileReaders;
  onError?: (err: LibraryError) => boolean;
  fill?: boolean;
}) {
  const tt = useT();
  const t = tt.diff;
  const chosen = useLayoutStore((s) => s.layout);
  const [texts, setTexts] = useState<[string | null, string | null] | "error" | undefined>(undefined);
  // 左右对比时拖选只选按下的那一栏，复制出来不会新旧交错。
  const [side, setSide] = useState<"left" | "right" | null>(null);
  const report = useRef(onError);
  report.current = onError;

  useEffect(() => {
    let cancelled = false;
    setTexts(undefined);
    const calls = [kind === "added" ? undefined : read.before(path), kind === "deleted" ? undefined : read.after(path)];
    const text = (call: CancellablePromise<File> | undefined) => (call ? call.then((f) => decodeContent(f.content)) : Promise.resolve(""));
    Promise.all([text(calls[0]), text(calls[1])]).then(
      ([a, b]) => !cancelled && setTexts([a, b]),
      (e) => {
        if (cancelled) return;
        setTexts("error");
        report.current?.(toLibraryError(e));
      },
    );
    return () => {
      cancelled = true;
      calls.forEach((c) => void c?.cancel());
    };
  }, [path, kind, read]);

  const comparable = Array.isArray(texts) && texts[0] !== null && texts[1] !== null;
  const switchable = comparable && kind === "changed";
  const layout: Layout = switchable ? chosen : "unified";

  let body: ReactNode;
  if (texts === undefined) {
    body = (
      <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
        <Spinner /> {t.reading(path)}
      </div>
    );
  } else if (texts === "error") {
    body = <Notice tone="bad">{t.failed(path)}</Notice>;
  } else if (texts[0] === null || texts[1] === null) {
    body = <Notice>{tt.common.binary}</Notice>;
  } else {
    const lines = lineDiff(texts[0], texts[1]);
    body = (
      <div
        data-slot="file-diff"
        data-layout={layout}
        className={cn(
          "overflow-auto rounded-lg border border-border-subtle bg-gray-50 py-1.5 font-mono text-[11.5px] leading-[1.55] select-text",
          fill ? "min-h-0" : "max-h-[360px]",
        )}
      >
        {layout === "unified" ? (
          lines.map((line, i) => <Line key={i} line={line} />)
        ) : (
          <div
            className={cn(
              "grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)]",
              side === "left" && "[&_[data-side=right]]:select-none",
              side === "right" && "[&_[data-side=left]]:select-none",
            )}
            onMouseDown={(e) => {
              const cell = (e.target as Element).closest("[data-side]");
              setSide(cell?.getAttribute("data-side") === "right" ? "right" : "left");
            }}
          >
            {splitRows(lines).map((row, i) => (
              <Row key={i} row={row} />
            ))}
          </div>
        )}
      </div>
    );
  }

  return (
    <div className={cn("flex flex-col gap-1.5", fill && "min-h-0 grow basis-0")}>
      {/* 标题行固定为布局切换的高度：换到不显示切换的文件时，下面的差异不上下跳。 */}
      <div className="flex min-h-7 items-center gap-2">
        <span className="min-w-0 grow truncate font-mono text-[11px] text-muted-foreground" title={path}>
          {path}
        </span>
        {switchable && (
          <Segmented
            value={layout}
            onChange={setLayout}
            options={[
              { value: "split", label: t.split },
              { value: "unified", label: t.unified },
            ]}
          />
        )}
      </div>
      {body}
    </div>
  );
}

function Skip({ count, className }: { count: number; className?: string }) {
  const t = useT().diff;
  return <div className={cn("px-3 py-0.5 font-sans text-[11px] text-subtle select-none", className)}>{t.unchanged(count)}</div>;
}

// Cell 是一行代码：增删着色，行首是 +/-,长行自动换行;side 标出左右对比里在哪一栏。
function Cell({ kind, text, side, className }: { kind: "same" | "add" | "del"; text: string; side?: "left" | "right"; className?: string }) {
  return (
    <div
      data-diff={kind}
      data-side={side}
      className={cn(
        "flex gap-2 px-3 break-words whitespace-pre-wrap",
        kind === "add" && "bg-ok-soft text-ok-strong",
        kind === "del" && "bg-bad-soft text-bad-strong",
        className,
      )}
    >
      <span className="w-2.5 shrink-0 text-subtle select-none">{kind === "add" ? "+" : kind === "del" ? "-" : " "}</span>
      <span className="min-w-0 grow">{text || " "}</span>
    </div>
  );
}

function Line({ line }: { line: DiffLine }) {
  if (line.kind === "skip") return <Skip count={line.count} />;
  return <Cell kind={line.kind} text={line.text} />;
}

// Row 是左右对比的一行，两格同高;一侧没有对应行时留一块灰底。
function Row({ row }: { row: SplitRow }) {
  if (row.kind === "skip") return <Skip count={row.count} className="col-span-2" />;
  const divider = "border-r border-border-subtle";
  const empty = "bg-black/[0.03]";
  return (
    <>
      {row.left ? (
        <Cell kind={row.left.kind} text={row.left.text} side="left" className={divider} />
      ) : (
        <div data-side="left" className={cn(empty, divider)} />
      )}
      {row.right ? <Cell kind={row.right.kind} text={row.right.text} side="right" /> : <div data-side="right" className={empty} />}
    </>
  );
}
