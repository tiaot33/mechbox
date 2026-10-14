// 更新计划里一个文件的差异：左边是库里的当前版本，右边是来源里的新内容。
// SKILL.md 等 Markdown 按文档展示(正文字体、自动换行),脚本等其他文件按代码展示(等宽、带 +/-)。
import { useEffect, useState } from "react";
import type { CancellablePromise } from "@wailsio/runtime";
import type { File } from "@bindings/internal/library";
import { decodeContent } from "@/lib/content";
import { lineDiff, type DiffLine } from "@/lib/diff";
import { cn } from "@/lib/utils";
import { Notice } from "@/components/ui/notice";
import { Spinner } from "@/components/ui/spinner";

type Read = () => CancellablePromise<File>;

// before / after 为 null 表示这一侧没有这个文件(新增 / 删除)。
export function FileDiff({ path, before, after }: { path: string; before: Read | null; after: Read | null }) {
  const [texts, setTexts] = useState<[string | null, string | null] | "error" | undefined>(undefined);

  useEffect(() => {
    let cancelled = false;
    setTexts(undefined);
    const calls = [before?.(), after?.()];
    const text = (call: CancellablePromise<File> | undefined) => (call ? call.then((f) => decodeContent(f.content)) : Promise.resolve(""));
    Promise.all([text(calls[0]), text(calls[1])]).then(
      ([a, b]) => !cancelled && setTexts([a, b]),
      () => !cancelled && setTexts("error"),
    );
    return () => {
      cancelled = true;
      calls.forEach((c) => void c?.cancel());
    };
  }, [path, before, after]);

  if (texts === undefined) {
    return (
      <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
        <Spinner /> 正在读取 {path}…
      </div>
    );
  }
  if (texts === "error") return <Notice tone="bad">读取 {path} 失败。</Notice>;
  const [a, b] = texts;
  if (a === null || b === null) return <Notice>二进制文件，不显示内容。</Notice>;
  const doc = path.toLowerCase().endsWith(".md");
  return (
    <div
      data-slot="file-diff"
      className={cn(
        "max-h-[360px] overflow-auto rounded-lg border border-border-subtle bg-gray-50 py-1.5 select-text",
        doc ? "text-[12px] leading-[1.6]" : "font-mono text-[11.5px] leading-[1.55]",
      )}
    >
      {lineDiff(a, b).map((line, i) => (
        <Line key={i} line={line} doc={doc} />
      ))}
    </div>
  );
}

function Line({ line, doc }: { line: DiffLine; doc: boolean }) {
  if (line.kind === "skip") {
    return <div className="px-3 py-0.5 text-[11px] text-subtle">… {line.count} 行没变 …</div>;
  }
  return (
    <div
      data-diff={line.kind}
      className={cn(
        "flex gap-2 px-3 break-words whitespace-pre-wrap",
        line.kind === "add" && "bg-ok-soft text-ok-strong",
        line.kind === "del" && "bg-bad-soft text-bad-strong",
        line.kind === "del" && doc && "line-through decoration-bad-strong/40",
      )}
    >
      {!doc && <span className="w-2.5 shrink-0 text-subtle select-none">{line.kind === "add" ? "+" : line.kind === "del" ? "-" : " "}</span>}
      <span className="min-w-0 grow">{line.text || " "}</span>
    </div>
  );
}
