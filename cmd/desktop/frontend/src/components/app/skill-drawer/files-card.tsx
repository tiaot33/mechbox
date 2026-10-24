// 技能详情里的文件卡片：当前版本的文件树，选中后读取内容，SKILL.md 渲染成文档。
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FileMode, type FileMetadata, type VersionID } from "@bindings/internal/library";
import { decodeContent, formatSize, renderMarkdown } from "@/lib/content";
import { useT } from "@/lib/i18n";
import { versionFileQuery } from "@/lib/library";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Notice } from "@/components/ui/notice";
import { Icon } from "@/components/app/icons";

// useFileContent 读某个版本里一个文件的内容：还没读到(或没选文件)时为 undefined,二进制或读取失败时为 null。
export function useFileContent(versionID: VersionID | undefined, path: string): string | null | undefined {
  const q = useQuery({ ...versionFileQuery(versionID ?? 0, path), enabled: versionID !== undefined && !!path });
  if (versionID === undefined || !path) return undefined;
  if (q.isError) return null;
  return q.data && decodeContent(q.data.content);
}

// FilesCard 列出当前版本的文件;选中后按需读取内容。SKILL.md 默认展开并渲染。
export function FilesCard({ versionID, files }: { versionID: VersionID; files: FileMetadata[] }) {
  const tt = useT();
  const t = tt.drawer;
  const [path, setPath] = useState("SKILL.md");
  const [raw, setRaw] = useState(false);
  const content = useFileContent(versionID, files.some((f) => f.path === path) ? path : "");
  const markdown = path.toLowerCase().endsWith(".md") && !raw;
  return (
    <Card>
      <CardHeader title={t.files} note={t.filesNote(files.length)} />
      <div className="mb-2">
        <FileTree nodes={fileTree(files)} selected={path} onSelect={setPath} />
      </div>
      {content === undefined ? null : content === null ? (
        <Notice>{tt.common.binary}</Notice>
      ) : markdown ? (
        <>
          <div className="md" dangerouslySetInnerHTML={{ __html: renderMarkdown(content) }} />
          <Button variant="link" onClick={() => setRaw(true)}>
            {t.viewSource}
          </Button>
        </>
      ) : (
        <>
          <Code>{content}</Code>
          {path.toLowerCase().endsWith(".md") && (
            <Button variant="link" onClick={() => setRaw(false)}>
              {t.viewRendered}
            </Button>
          )}
        </>
      )}
    </Card>
  );
}

type FileNode = { name: string; path: string; file?: FileMetadata; children: FileNode[] };

// fileTree 按路径把文件排成目录树;同一层目录在前、文件在后，各自按名字排序。
function fileTree(files: FileMetadata[]): FileNode[] {
  const root: FileNode = { name: "", path: "", children: [] };
  for (const f of files) {
    const parts = f.path.split("/");
    let dir = root;
    parts.forEach((name, i) => {
      const path = parts.slice(0, i + 1).join("/");
      if (i === parts.length - 1) {
        dir.children.push({ name, path, file: f, children: [] });
        return;
      }
      let next = dir.children.find((c) => !c.file && c.name === name);
      if (!next) {
        next = { name, path, children: [] };
        dir.children.push(next);
      }
      dir = next;
    });
  }
  const sort = (nodes: FileNode[]) => {
    nodes.sort((a, b) => Number(!!a.file) - Number(!!b.file) || a.name.localeCompare(b.name));
    nodes.forEach((n) => sort(n.children));
  };
  sort(root.children);
  return root.children;
}

// FileTree 渲染一层目录树;目录默认展开，点目录行折叠 / 展开，点文件行选中。
function FileTree({ nodes, selected, onSelect }: { nodes: FileNode[]; selected: string; onSelect: (path: string) => void }) {
  const t = useT().drawer;
  return (
    <div className="flex flex-col">
      {nodes.map((n) =>
        n.file ? (
          <button
            key={n.path}
            type="button"
            title={n.path}
            className={cn(
              "file-row flex w-full items-center gap-1.5 rounded-md py-1 pr-1.5 pl-[22px] text-left hover:bg-black/[0.04]",
              n.path === selected && "bg-primary-soft text-primary hover:bg-primary-soft",
            )}
            onClick={() => onSelect(n.path)}
          >
            <Icon name="file" className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="grow truncate font-mono text-[11.5px]">{n.name}</span>
            {n.file.mode === FileMode.ModeExecutable && <Badge>{t.executable}</Badge>}
            <span className="text-[11px] text-muted-foreground">{formatSize(n.file.size)}</span>
          </button>
        ) : (
          <FileTreeDir key={n.path} node={n} selected={selected} onSelect={onSelect} />
        ),
      )}
    </div>
  );
}

function FileTreeDir({ node, selected, onSelect }: { node: FileNode; selected: string; onSelect: (path: string) => void }) {
  const [open, setOpen] = useState(true);
  return (
    <div>
      <button
        type="button"
        title={node.path}
        aria-expanded={open}
        className="dir-row flex w-full items-center gap-1.5 rounded-md py-1 pr-1.5 pl-1.5 text-left hover:bg-black/[0.04]"
        onClick={() => setOpen(!open)}
      >
        <Icon name="chev" className={cn("size-2.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")} />
        <Icon name="folder" className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="grow truncate font-mono text-[11.5px]">{node.name}</span>
      </button>
      {open && (
        <div className="ml-[11px] border-l border-border-subtle pl-1">
          <FileTree nodes={node.children} selected={selected} onSelect={onSelect} />
        </div>
      )}
    </div>
  );
}

export function Code({ children }: { children: string }) {
  return (
    <pre
      data-slot="code"
      className="max-h-[360px] overflow-auto rounded-lg border border-border-subtle bg-[#f8f9fb] px-3 py-2.5 font-mono text-[11.5px] leading-[1.55] break-words whitespace-pre-wrap select-text"
    >
      {children}
    </pre>
  );
}
