// 渲染技能文件与更新说明里的 Markdown:react-markdown 加 GFM(表格、任务列表、删除线、自动链接),开头的 YAML frontmatter 不显示，
// 代码块按语言高亮,mermaid 代码块由 Pele 画成图,dot / graphviz 代码块由 Graphviz(@viz-js/viz,在 graphviz.worker.ts 里)画成图，画不出来时显示源码。
// 文中的 HTML 标签当成文字显示(见 remarkPlainHTML);图片只显示替代文字;链接的去向见 lib/markdown.ts 的 linkTarget。
import { memo, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkFrontmatter from "remark-frontmatter";
import rehypeHighlight from "rehype-highlight";
import { common } from "lowlight";
import powershell from "highlight.js/lib/languages/powershell";
import DOMPurify from "dompurify";
import { Browser } from "@wailsio/runtime";
import { linkTarget } from "@/lib/markdown";
import { messages, useT } from "@/lib/i18n";
import { ui } from "@/lib/ui";
import { cn } from "@/lib/utils";

type DiagramLanguage = "mermaid" | "dot";
const diagramLanguages: Record<string, DiagramLanguage> = { mermaid: "mermaid", dot: "dot", graphviz: "dot" };

// remarkPlainHTML 关掉 HTML 语法，标签一律是普通文字：技能里常用 <example>、<Good> 这类标签包住 Markdown,
// 按 HTML 块解析会把标签后面直到空行的内容(包括代码块)整段吞成原文。
function remarkPlainHTML(this: unknown) {
  const data = (this as { data(): { micromarkExtensions?: object[] } }).data();
  (data.micromarkExtensions ??= []).push({ disable: { null: ["htmlFlow", "htmlText"] } });
}

const remarkPlugins = [remarkGfm, remarkFrontmatter, remarkPlainHTML];
const rehypePlugins = [
  [rehypeHighlight, { languages: { ...common, powershell }, plainText: Object.keys(diagramLanguages) }],
] satisfies Parameters<typeof ReactMarkdown>[0]["rehypePlugins"];

// SkillFiles 是正在看的技能文件(current)与这个版本的全部文件路径(all):指向其中别的文件的相对链接，点了交给 open。
export type SkillFiles = { current: string; all: readonly string[]; open: (path: string) => void };

// Markdown 渲染 text。给了 files 时，指向技能里其他文件的相对链接可以点;没给时只有外部网址是链接。
// 标题降两级输出(# 是 h3,#### 往下都是 h6):文档在抽屉、弹窗的卡片里，上面已经有技能名、对话框标题。
export const Markdown = memo(function Markdown({ text, className, files }: { text: string; className?: string; files?: SkillFiles }) {
  const components = useMemo<Components>(() => {
    const known = files && new Set(files.all);
    return {
      h1: ({ children }) => <h3>{children}</h3>,
      h2: ({ children }) => <h4>{children}</h4>,
      h3: ({ children }) => <h5>{children}</h5>,
      h4: ({ children }) => <h6>{children}</h6>,
      h5: ({ children }) => <h6>{children}</h6>,
      h6: ({ children }) => <h6>{children}</h6>,
      a: ({ href, children }) => {
        const target = linkTarget(href, files?.current, known);
        if (!target) return <span>{children}</span>;
        return (
          <a
            href={href}
            title={"url" in target ? target.url : undefined}
            onClick={(e) => {
              e.preventDefault();
              if ("url" in target) void openURL(target.url);
              else files?.open(target.file);
            }}
          >
            {children}
          </a>
        );
      },
      img: ({ alt }) => (alt ? <span className="text-muted-foreground italic">{alt}</span> : null),
      table: ({ children }) => (
        <div className="my-1.5 overflow-x-auto">
          <table>{children}</table>
        </div>
      ),
      pre: ({ node, children }) => {
        const code = node?.children[0];
        const classes = code?.type === "element" && code.tagName === "code" ? code.properties.className : undefined;
        const lang = Array.isArray(classes)
          ? classes.map((c) => /^language-(.+)$/.exec(String(c))?.[1]).find((l) => l !== undefined)
          : undefined;
        const diagram = lang && diagramLanguages[lang.toLowerCase()];
        if (diagram && code) return <Diagram language={diagram} source={textOf(code).replace(/\n$/, "")} />;
        return <pre>{children}</pre>;
      },
    };
  }, [files]);
  return (
    <div className={cn("md", className)}>
      <ReactMarkdown remarkPlugins={remarkPlugins} rehypePlugins={rehypePlugins} components={components}>
        {text}
      </ReactMarkdown>
    </div>
  );
});

async function openURL(url: string) {
  try {
    await Browser.OpenURL(url);
  } catch {
    ui.toast(messages().common.openLinkFailed(url), "error");
  }
}

type HastNode = { type: string; value?: string; children?: HastNode[] };

function textOf(node: HastNode): string {
  return node.value ?? node.children?.map(textOf).join("") ?? "";
}

// 图的 SVG 插入之前都经 DOMPurify 清理，去掉链接:Pele 本身不输出脚本和 HTML 标签，Graphviz 的 URL / href 属性会生成 <a>。
const sanitizeSVG = (svg: string): Node =>
  DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true, svgFilters: true }, FORBID_TAGS: ["a"], RETURN_DOM_FRAGMENT: true });

// renderDot 每张图开一个 Worker,画完、10 秒还没画完或组件不再需要(signal)时结束它：一张复杂的图只拖住它自己。
const dotTimeout = 10_000;

function renderDot(source: string, signal: AbortSignal): Promise<string | null> {
  return new Promise((resolve) => {
    const worker = new Worker(new URL("./graphviz.worker.ts", import.meta.url), { type: "module" });
    const finish = (svg: string | null) => {
      clearTimeout(timer);
      signal.removeEventListener("abort", stop);
      worker.terminate();
      resolve(svg);
    };
    const stop = () => finish(null);
    const timer = setTimeout(stop, dotTimeout);
    signal.addEventListener("abort", stop);
    worker.onmessage = ({ data }: MessageEvent<string | null>) => finish(data);
    worker.onerror = stop;
    worker.postMessage(source);
  });
}

// fitDot 让 Graphviz 的图按正文的字号显示(默认 14pt 的标签约等于正文的 12.5px),不跟着抽屉宽度缩小;放不下时横向滚动，和表格一样。
function fitDot(svg: Node): Node {
  const el = svg instanceof DocumentFragment ? svg.querySelector("svg") : null;
  const width = el?.viewBox.baseVal?.width;
  if (el && width) {
    el.style.width = `${(width * 12.5) / 14}px`;
    el.style.maxWidth = "none";
  }
  return svg;
}

// Diagram 把 mermaid / dot 代码块画成图;两个渲染器都很大,Pele 第一次遇到 mermaid 代码块时才加载,Graphviz 在 Worker 里加载。
// 画不出来(语法错误、超出 Pele 的大小上限、Graphviz 超时等)时说明一句，并照代码块显示源码。
function Diagram({ language, source }: { language: DiagramLanguage; source: string }) {
  const t = useT().common;
  const ref = useRef<HTMLDivElement>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    const el = ref.current!;
    let cancelled = false;
    let destroy: (() => void) | undefined;
    const abort = new AbortController();
    const fail = () => !cancelled && setFailed(true);
    setFailed(false);
    void (async () => {
      try {
        if (language === "mermaid") {
          const { mount } = await import("pele");
          if (cancelled) return;
          const mounted = mount(el, source, { fontSize: 13, links: false, images: false, zoom: false, sanitize: sanitizeSVG, onError: fail });
          destroy = () => mounted.destroy();
          if (mounted.error) fail();
        } else {
          const svg = await renderDot(source, abort.signal);
          if (cancelled) return;
          if (svg === null) fail();
          else el.replaceChildren(fitDot(sanitizeSVG(svg)));
        }
      } catch {
        fail();
      }
    })();
    return () => {
      cancelled = true;
      abort.abort();
      destroy?.();
      el.replaceChildren();
    };
  }, [language, source]);
  let fallback: ReactNode = null;
  if (failed)
    fallback = (
      <>
        <p className="text-muted-foreground">{t.diagramFailed}</p>
        <pre>
          <code>{source}</code>
        </pre>
      </>
    );
  return (
    <>
      <div ref={ref} className="md-diagram" hidden={failed} />
      {fallback}
    </>
  );
}
