// 渲染技能文件与更新说明里的 Markdown:react-markdown 加 GFM(表格、任务列表、删除线、自动链接),开头的 YAML frontmatter 不显示，
// 代码块按语言高亮,mermaid 代码块由 Pele 画成图,dot / graphviz 代码块由 Graphviz(@viz-js/viz)画成图，画不出来时显示源码。
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
import { useT } from "@/lib/i18n";
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

// Markdown 渲染 text。path 是正在看的技能文件、files 是这个版本的全部文件路径，给了两者时，
// 指向技能里其他文件的相对链接点了交给 onOpenFile;没给时只有外部网址是链接。
export const Markdown = memo(function Markdown({
  text,
  className,
  path,
  files,
  onOpenFile,
}: {
  text: string;
  className?: string;
  path?: string;
  files?: readonly string[];
  onOpenFile?: (path: string) => void;
}) {
  const components = useMemo<Components>(() => {
    const known = files && new Set(files);
    return {
      a: ({ href, children }) => {
        const target = linkTarget(href, path, onOpenFile && known);
        if (!target) return <span>{children}</span>;
        return (
          <a
            href={href}
            title={"url" in target ? target.url : undefined}
            onClick={(e) => {
              e.preventDefault();
              if ("url" in target) void Browser.OpenURL(target.url);
              else onOpenFile?.(target.file);
            }}
          >
            {children}
          </a>
        );
      },
      img: ({ alt }) => (alt ? <span className="md-image-alt">{alt}</span> : null),
      table: ({ children }) => (
        <div className="md-table">
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
  }, [path, files, onOpenFile]);
  return (
    <div className={cn("md", className)}>
      <ReactMarkdown remarkPlugins={remarkPlugins} rehypePlugins={rehypePlugins} components={components}>
        {text}
      </ReactMarkdown>
    </div>
  );
});

type HastNode = { type: string; value?: string; children?: HastNode[] };

function textOf(node: HastNode): string {
  return node.value ?? node.children?.map(textOf).join("") ?? "";
}

// 图的 SVG 插入之前都经 DOMPurify 清理，去掉链接:Pele 本身不输出脚本和 HTML 标签，Graphviz 的 URL / href 属性会生成 <a>。
const sanitizeSVG = (svg: string): Node =>
  DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true, svgFilters: true }, FORBID_TAGS: ["a"], RETURN_DOM_FRAGMENT: true });

let graphviz: Promise<import("@viz-js/viz").Viz> | undefined;

async function renderDot(source: string): Promise<string> {
  graphviz ??= import("@viz-js/viz").then((m) => m.instance());
  const viz = await graphviz;
  const font = { fontname: "Helvetica,Arial,sans-serif" };
  return viz.renderString(source, { format: "svg", graphAttributes: font, nodeAttributes: font, edgeAttributes: font });
}

// Diagram 把 mermaid / dot 代码块画成图;两个渲染器都很大，第一次遇到这种代码块时才加载。
// 画不出来(语法错误、超出 Pele 的大小上限等)时说明一句，并照代码块显示源码。
function Diagram({ language, source }: { language: DiagramLanguage; source: string }) {
  const t = useT().common;
  const ref = useRef<HTMLDivElement>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    const el = ref.current!;
    let cancelled = false;
    let destroy: (() => void) | undefined;
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
          const svg = await renderDot(source);
          if (!cancelled) el.replaceChildren(sanitizeSVG(svg));
        }
      } catch {
        fail();
      }
    })();
    return () => {
      cancelled = true;
      destroy?.();
      el.replaceChildren();
    };
  }, [language, source]);
  let fallback: ReactNode = null;
  if (failed)
    fallback = (
      <>
        <p className="md-diagram-failed">{t.diagramFailed}</p>
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
