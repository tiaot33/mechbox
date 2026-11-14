// 一个文件的差异：旧内容和新内容逐行比较，所有文件(包括 SKILL.md)都按代码展示(等宽、带行号与 +/-,改掉的行里标出改了哪几个词)。
// 可以切换左右对比和行内对比(新增、删除的文件只有一侧，铺满整宽),选过的布局存在 localStorage,各处的差异共用、下次打开沿用。
// 左右对比时拖选只选按下的那一栏。
// ChangedFiles 是改了哪些文件的目录树，点文件看它的差异;更新计划和比较弹窗共用。
import { Fragment, useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import type { CancellablePromise } from "@wailsio/runtime";
import { create } from "zustand";
import type { File } from "@bindings/internal/library";
import { decodeContent } from "@/lib/content";
import { useT } from "@/lib/i18n";
import { toLibraryError, type LibraryError } from "@/lib/library";
import {
  changeTree,
  changedWords,
  lineDiff,
  modeChange,
  sameContent,
  splitRows,
  stepFile,
  unfold,
  type ChangeKind,
  type ChangeNode,
  type DiffLine,
  type Segment,
  type SplitRow,
} from "@/lib/diff";
import { cn, toggled } from "@/lib/utils";
import { Notice } from "@/components/ui/notice";
import { Segmented } from "@/components/ui/segmented";
import { Spinner } from "@/components/ui/spinner";
import { Icon } from "@/components/app/icons";

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

// 目录树里新增、删除的文件在行尾标 + / −(删除的文件名再划掉);修改是最常见的，不标。
const changeMark: Record<ChangeKind, { glyph: string; className: string } | undefined> = {
  added: { glyph: "+", className: "text-ok-strong" },
  changed: undefined,
  deleted: { glyph: "−", className: "text-bad-strong" },
};

// splitPath 把路径拆成所在目录(带结尾的 /,在根上为空)和文件名。
const splitPath = (path: string) => {
  const cut = path.lastIndexOf("/") + 1;
  return [path.slice(0, cut), path.slice(cut)] as const;
};

// ChangedFiles 把改了的文件排成目录树(排法和技能详情的文件树一样，目录在前、文件在后;另外只有一个子目录的连续几层合成一行),
// 目录默认展开，点目录行折叠 / 展开;点文件看它的差异，选中的一项高亮;焦点在列表里时上下键换到上一个 / 下一个看得见的文件。
// 选中的文件从外面换了(点风险、换技能)时，它所在的目录折着就展开;换了一批文件(换技能)时目录都回到展开。
// badge 给某个文件加标记(如更新计划里的风险)。
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
  const tree = useMemo(() => changeTree(files), [files]);
  const [closed, setClosed] = useState<Set<string>>(() => new Set());
  const list = useRef<HTMLDivElement>(null);
  const batch = files.map((f) => f.path).join("\n");
  useEffect(() => setClosed(new Set()), [batch]);
  useEffect(
    () =>
      setClosed((prev) => {
        const above = [...prev].filter((dir) => path.startsWith(`${dir}/`));
        if (above.length === 0) return prev;
        const next = new Set(prev);
        above.forEach((dir) => next.delete(dir));
        return next;
      }),
    [path],
  );
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const next = stepFile(tree, closed, path, e.key === "ArrowDown" ? 1 : -1);
    if (!next) return;
    onPath(next);
    list.current?.querySelector<HTMLElement>(`[data-path="${CSS.escape(next)}"]`)?.focus();
  };
  return (
    <div ref={list} className={cn("flex flex-col", className)} onKeyDown={onKeyDown}>
      <Tree
        nodes={tree}
        depth={0}
        path={path}
        closed={closed}
        onToggle={(dir) => setClosed((prev) => toggled(prev, dir))}
        onPath={onPath}
        badge={badge}
      />
      {files.length === 0 && <span className="px-2 text-[11px] text-muted-foreground">{tt.common.sameContent}</span>}
    </div>
  );
}

function Tree({
  nodes,
  depth,
  path,
  closed,
  onToggle,
  onPath,
  badge,
}: {
  nodes: ChangeNode[];
  depth: number;
  path: string;
  closed: Set<string>;
  onToggle: (dir: string) => void;
  onPath: (path: string) => void;
  badge?: (path: string) => ReactNode;
}) {
  const t = useT().diff;
  // 焦点框和按钮一样;选中的文件行不画：上下键换文件时焦点就在它上面，选中的底色已经标出来了。
  const row =
    "flex h-7 w-full shrink-0 items-center gap-1.5 rounded-md pr-2 text-left text-[12.5px] outline-none hover:bg-fill-subtle focus-visible:ring-3 focus-visible:ring-primary-soft active:bg-fill-strong";
  // 每深一层缩进 14px;目录行的箭头占在文件图标前面那一格，同一层的目录名和文件名对齐。
  const indent = { paddingLeft: 6 + depth * 14 };
  return nodes.map((n) => {
    if (n.kind === "dir") {
      const open = !closed.has(n.path);
      return (
        <Fragment key={n.path}>
          <button
            type="button"
            title={n.path}
            aria-expanded={open}
            className={row}
            style={indent}
            onClick={(e) => {
              // WebKit 里点按钮不会让它拿到焦点，手动给：折起的目录里有选中的文件时，焦点留在这一行，上下键照样能换文件。
              e.currentTarget.focus();
              onToggle(n.path);
            }}
          >
            <Icon name="chev" className={cn("size-2.5 shrink-0 text-subtle transition-transform", open && "rotate-90")} />
            <Icon name="folder" className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 grow truncate text-foreground-secondary">{n.name}</span>
          </button>
          {open && (
            <Tree nodes={n.children} depth={depth + 1} path={path} closed={closed} onToggle={onToggle} onPath={onPath} badge={badge} />
          )}
        </Fragment>
      );
    }
    const mark = changeMark[n.change];
    const current = n.path === path;
    return (
      <button
        key={n.path}
        type="button"
        data-diff={n.change}
        data-path={n.path}
        aria-current={current || undefined}
        title={`${t[n.change]} ${n.path}`}
        className={cn(
          row,
          current && "bg-glass-row-selected hover:bg-glass-row-selected focus-visible:ring-0 active:bg-glass-row-selected",
        )}
        style={indent}
        onClick={(e) => {
          // 同上，手动给焦点，之后上下键才换得了文件。
          e.currentTarget.focus();
          onPath(n.path);
        }}
      >
        <span className="w-2.5 shrink-0" />
        <Icon name="file" className="size-3.5 shrink-0 text-muted-foreground" />
        <span
          className={cn("min-w-0 grow truncate", current && "font-medium", n.change === "deleted" && "text-muted-foreground line-through")}
        >
          {n.name}
        </span>
        <span className="sr-only">{t[n.change]}</span>
        {badge?.(n.path)}
        {mark && (
          <span aria-hidden className={cn("w-2.5 shrink-0 text-center font-mono font-semibold", mark.className)}>
            {mark.glyph}
          </span>
        )}
      </button>
    );
  });
}

// FileDiff 是 path 这个文件的差异，kind 是它改了什么：新增的只读改动后，删除的只读改动前;只有一侧的文件不分两栏，铺满整宽。
// 外面是一块白底的面板：上面一条写文件、增删了几行和布局切换，下面是带行号的逐行差异，改掉的行里再标出改了哪几个词。
// 修改的文件可执行权限变了时(加上或去掉),在差异上面单独说明;内容一字没变、只改了权限时也说清楚，免得看起来像什么都没改(issue #324)。
// read 要在调用方保持不变(换了才重读)。读取失败时先交给 onError,它返回 true 表示已处理(如更新计划已失效)。
// 布局切换只在两侧都能逐行比较时显示;fill 为 true 时面板撑满外面给的高度(比较弹窗),否则差异最高 360px、内容短时按内容的高度。
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
  // 读到的改动前后两侧;新增的文件没有前一侧，删除的没有后一侧。
  const [files, setFiles] = useState<[File | undefined, File | undefined] | "error" | undefined>(undefined);
  // 左右对比时拖选只选按下的那一栏，复制出来不会新旧交错。
  const [side, setSide] = useState<"left" | "right" | null>(null);
  const report = useRef(onError);
  report.current = onError;

  useEffect(() => {
    let cancelled = false;
    setFiles(undefined);
    const calls = [kind === "added" ? undefined : read.before(path), kind === "deleted" ? undefined : read.after(path)];
    const file = (call: CancellablePromise<File> | undefined) => call ?? Promise.resolve(undefined);
    Promise.all([file(calls[0]), file(calls[1])]).then(
      ([a, b]) => !cancelled && setFiles([a, b]),
      (e) => {
        if (cancelled) return;
        setFiles("error");
        report.current?.(toLibraryError(e));
      },
    );
    return () => {
      cancelled = true;
      calls.forEach((c) => void c?.cancel());
    };
  }, [path, kind, read]);

  // 两侧的文本;没有的那一侧是空文本，二进制文件为 null。
  const texts = useMemo((): [string | null, string | null] | "error" | undefined => {
    if (!Array.isArray(files)) return files;
    const text = (f: File | undefined) => (f ? decodeContent(f.content) : "");
    return [text(files[0]), text(files[1])];
  }, [files]);

  const comparable = Array.isArray(texts) && texts[0] !== null && texts[1] !== null;
  const switchable = comparable && kind === "changed";
  const layout: Layout = switchable ? chosen : "unified";
  const lines = useMemo(() => (comparable ? lineDiff(texts[0]!, texts[1]!) : null), [comparable, texts]);
  const marks = useMemo(() => (lines ? changedWords(lines) : new Map<DiffLine, Segment[]>()), [lines]);
  // 点开的几段折起来的没变行;换了文件(行重新算过)就都收回去。
  const [unfolded, setUnfolded] = useState<Set<DiffLine>>(() => new Set());
  useEffect(() => setUnfolded(new Set()), [lines]);
  const shown = useMemo(() => (lines ? unfold(lines, texts![0]!, unfolded) : null), [lines, texts, unfolded]);
  const expand = (skip: DiffLine) => setUnfolded((prev) => new Set(prev).add(skip));
  const [dir, name] = splitPath(path);

  let body: ReactNode;
  if (texts === undefined) {
    body = (
      <div className="flex items-center gap-1.5 p-3 text-[11px] text-muted-foreground">
        <Spinner /> {t.reading(path)}
      </div>
    );
  } else if (texts === "error") {
    body = (
      <div className="p-3">
        <Notice tone="bad">{t.failed(path)}</Notice>
      </div>
    );
  } else if (!lines) {
    body = (
      <div className="p-3">
        <Notice>{tt.common.binary}</Notice>
      </div>
    );
  } else {
    // 一侧没有内容(新增、删除的文件)时只留有内容那一侧的行号。
    const gutters = { old: kind !== "added", new: kind !== "deleted" };
    body = (
      <div
        data-slot="file-diff"
        data-layout={layout}
        className={cn(
          "overflow-auto font-mono text-[12px] leading-5 select-text",
          fill ? "min-h-0 grow" : "max-h-[360px]",
          layout === "split" && side === "left" && "[&_[data-side=right]]:select-none",
          layout === "split" && side === "right" && "[&_[data-side=left]]:select-none",
        )}
        onMouseDown={(e) => {
          const cell = (e.target as Element).closest("[data-side]");
          setSide(cell?.getAttribute("data-side") === "right" ? "right" : "left");
        }}
      >
        {layout === "unified" ? (
          <div
            className={cn("grid", gutters.old && gutters.new ? "grid-cols-[auto_auto_minmax(0,1fr)]" : "grid-cols-[auto_minmax(0,1fr)]")}
          >
            {shown!.map((line, i) =>
              line.kind === "skip" ? (
                <Skip key={i} count={line.count} onExpand={() => expand(line)} />
              ) : (
                <Fragment key={i}>
                  {gutters.old && <Num kind={line.kind} n={line.kind === "add" ? undefined : line.oldLine} />}
                  {gutters.new && <Num kind={line.kind} n={line.kind === "del" ? undefined : line.newLine} />}
                  <Cell kind={line.kind} text={line.text} segments={marks.get(line)} />
                </Fragment>
              ),
            )}
          </div>
        ) : (
          <div className="grid grid-cols-[auto_minmax(0,1fr)_auto_minmax(0,1fr)]">
            {splitRows(shown!).map((row, i) => (
              <Row key={i} row={row} marks={marks} onExpand={expand} />
            ))}
          </div>
        )}
      </div>
    );
  }

  const added = lines?.filter((l) => l.kind === "add").length ?? 0;
  const deleted = lines?.filter((l) => l.kind === "del").length ?? 0;
  const [before, after] = Array.isArray(files) ? files : [undefined, undefined];
  const mode = modeChange(before, after);
  const onlyMode = before !== undefined && after !== undefined && sameContent(before, after);

  return (
    <div
      data-slot="file-diff-panel"
      className={cn("flex flex-col overflow-hidden rounded-[10px] bg-panel shadow-card", fill && "min-h-0 grow basis-0")}
    >
      {/* 标题条固定高度：换到不显示切换的文件时，下面的差异不上下跳。 */}
      <div className="flex h-10 shrink-0 items-center gap-2.5 border-b border-hairline pr-1.5 pl-3">
        <span className="flex min-w-0 grow items-baseline text-[12.5px]" title={path}>
          {dir && <span className="min-w-0 shrink truncate text-muted-foreground">{dir}</span>}
          <span className="min-w-0 shrink-0 truncate font-semibold">{name}</span>
        </span>
        {(added > 0 || deleted > 0) && (
          <span className="shrink-0 font-mono text-[11px] tabular-nums">
            {added > 0 && <span className="text-ok-strong">+{added}</span>}
            {added > 0 && deleted > 0 && " "}
            {deleted > 0 && <span className="text-bad-strong">−{deleted}</span>}
          </span>
        )}
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
      {mode && (
        <Notice tone={mode === "executable-added" ? "warn" : "neutral"} className="mode-change m-2 mb-0 shrink-0">
          {t.modeChanged(mode, onlyMode)}
        </Notice>
      )}
      {body}
    </div>
  );
}

// Skip 是折起来的一段没变的行，横跨整行;点了展开这几行。
function Skip({ count, onExpand }: { count: number; onExpand: () => void }) {
  const t = useT().diff;
  return (
    <button
      type="button"
      title={t.expand(count)}
      className="col-span-full flex h-7 items-center gap-2 border-y border-hairline bg-surface px-3 text-left font-sans text-[11px] text-muted-foreground select-none first:border-t-0 last:border-b-0 hover:bg-primary-soft hover:text-primary-ink active:bg-primary-soft/70"
      onClick={onExpand}
    >
      <Icon name="expand" className="size-3 shrink-0" />
      {t.unchanged(count)}
    </button>
  );
}

const lineTone = { same: "", add: "bg-ok/[0.08]", del: "bg-bad/[0.07]" };
const gutterTone = {
  same: "text-subtle",
  add: "bg-ok/[0.14] text-ok-strong/70",
  del: "bg-bad/[0.12] text-bad-strong/70",
};
const wordTone = { same: "bg-transparent", add: "bg-ok/25", del: "bg-bad/20" };

// Num 是一格行号;n 为 undefined 时留空(如行内对比里新增的行没有旧行号)。
function Num({ kind, n, side, className }: { kind: "same" | "add" | "del"; n?: number; side?: "left" | "right"; className?: string }) {
  return (
    <div data-side={side} className={cn("min-w-[4ch] px-2 text-right text-[11px] tabular-nums select-none", gutterTone[kind], className)}>
      {n}
    </div>
  );
}

// Cell 是一行代码：增删的行底色淡淡标出，行首是 +/-,改掉的词再深一层;字一律是正文颜色，长行自动换行。side 标出左右对比里在哪一栏。
function Cell({
  kind,
  text,
  segments,
  side,
}: {
  kind: "same" | "add" | "del";
  text: string;
  segments?: Segment[];
  side?: "left" | "right";
}) {
  return (
    <div data-diff={kind} data-side={side} className={cn("flex gap-1.5 pr-3 break-words whitespace-pre-wrap", lineTone[kind])}>
      <span
        className={cn(
          "w-3 shrink-0 text-center select-none",
          kind === "add" ? "text-ok-strong" : kind === "del" ? "text-bad-strong" : "text-transparent",
        )}
      >
        {kind === "add" ? "+" : kind === "del" ? "-" : " "}
      </span>
      <span className="min-w-0 grow">
        {segments
          ? segments.map((seg, i) =>
              seg.changed ? (
                <mark key={i} className={cn("rounded-[3px] box-decoration-clone text-inherit", wordTone[kind])}>
                  {seg.text}
                </mark>
              ) : (
                seg.text
              ),
            )
          : text || " "}
      </span>
    </div>
  );
}

// Row 是左右对比的一行：两侧各是行号与代码，同高;一侧没有对应行时那一侧留一块淡灰。
function Row({ row, marks, onExpand }: { row: SplitRow; marks: Map<DiffLine, Segment[]>; onExpand: (skip: DiffLine) => void }) {
  if (row.kind === "skip") return <Skip count={row.count} onExpand={() => onExpand(row)} />;
  const empty = "bg-fill-faint";
  const divider = "border-l border-hairline";
  return (
    <>
      {row.left ? (
        <>
          <Num kind={row.left.kind} n={row.left.oldLine} side="left" />
          <Cell kind={row.left.kind} text={row.left.text} segments={marks.get(row.left)} side="left" />
        </>
      ) : (
        <div data-side="left" className={cn("col-span-2", empty)} />
      )}
      {row.right ? (
        <>
          <Num kind={row.right.kind} n={row.right.newLine} side="right" className={divider} />
          <Cell kind={row.right.kind} text={row.right.text} segments={marks.get(row.right)} side="right" />
        </>
      ) : (
        <div data-side="right" className={cn("col-span-2", empty, divider)} />
      )}
    </>
  );
}
