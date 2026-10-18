// 入库的第二步：浏览一次入库预览的候选与诊断，勾选后 Fetch 入库。
// 入库技能、技能市场与集合上的"看来源里的其他技能"都在拿到入库预览后进入这里;顶部显示技能库识别出的来源。
import { useEffect, useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  type Candidate,
  type FetchPreview,
  type FetchPreviewID,
  type SkillAudit,
  SourceType,
} from "@bindings/internal/library";
import { Modal, useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { Spinner } from "@/components/ui/spinner";
import { AuditBadge } from "./audit";
import { Diagnostics, toggled } from "./parts";

interface FetchReviewProps {
  preview: FetchPreview;
  onlyNew?: boolean; // 从集合上的"新发布了 N 个技能"进来：只默认勾上来源里的新技能
  onClose: () => void;
  onBack: () => void;
}

// parentDir 是候选路径的上级目录;候选分在不同目录下时按它分组列出。
function parentDir(path: string): string {
  const i = path.lastIndexOf("/");
  return i < 0 ? "" : path.slice(0, i);
}

// sourceKindName 是识别结果里来源类型的说法。
const sourceKindName: Record<SourceType, string> = {
  [SourceType.SourceGitHub]: "GitHub 仓库",
  [SourceType.SourceGit]: "Git 仓库",
  [SourceType.SourceURL]: "直接下载",
  [SourceType.SourceWellKnown]: "well-known 索引",
  [SourceType.SourceLocal]: "本地目录",
  [SourceType.$zero]: "未知来源",
};

export function FetchReview({ preview, onlyNew = false, onClose, onBack }: FetchReviewProps) {
  const ui = useUI();
  // 只有 GitHub 仓库在 skills.sh 收录范围内，才提供审计查询，也只有它走 GitHub API、没有令牌时容易被限流。
  const github = preview.source.type === SourceType.SourceGitHub;
  const [tokenMissing, setTokenMissing] = useState(false);
  useEffect(() => {
    if (!github) return;
    const p = LibraryService.Config();
    p.then((c) => setTokenMissing(!c.github_token), () => {});
    return () => void p.cancel();
  }, [github]);
  const candidates = preview.candidates ?? [];
  const fresh = candidates.filter((c) => !c.fetched);
  // 默认勾选尚未入库的候选(从新技能提示进来时只勾新技能);已入库的不能再选(整批会因 AlreadyExists 失败)。
  const [selected, setSelected] = useState<Set<string>>(() => new Set(fresh.filter((c) => !onlyNew || c.new).map((c) => c.name)));
  const [fetching, setFetching] = useState(false);

  const toggle = (name: string) => setSelected((set) => toggled(set, name));
  // 分组的勾选：组里没入库的候选都勾上时取消全组，否则全组勾上。
  const toggleGroup = (members: Candidate[]) =>
    setSelected((set) => {
      const names = members.filter((c) => !c.fetched).map((c) => c.name);
      const next = new Set(set);
      const all = names.every((n) => set.has(n));
      for (const n of names) {
        if (all) next.delete(n);
        else next.add(n);
      }
      return next;
    });

  const fetchSelected = async () => {
    setFetching(true);
    const names = [...selected];
    const fetched = await ui.run(() => LibraryService.Fetch(preview.id, names), {
      success: (list) => `已入库 ${list?.length ?? 0} 个技能`,
    });
    setFetching(false);
    if (fetched) onClose();
  };

  const groups = new Map<string, Candidate[]>();
  for (const c of candidates) groups.set(parentDir(c.path), [...(groups.get(parentDir(c.path)) ?? []), c]);
  const grouped = groups.size > 1;

  const row = (c: Candidate, dir: string) => {
    // 分组时路径只写组目录以下的部分，和技能名一样就不重复写;来源根目录(单个 SKILL.md 是 ".")写作 "/"。
    const rel = grouped && dir ? c.path.slice(dir.length + 1) : c.path && c.path !== "." ? c.path : "/";
    return (
      <ListRow
        key={c.path + c.name}
        asChild
        compact
        clickable
        selected={selected.has(c.name)}
        className={cn(grouped && "pl-6")}
      >
        <label title={c.description || undefined}>
          {/* 每条一行，描述放在悬停提示里，几十个候选也能一屏看完。 */}
          <input
            type="checkbox"
            className="shrink-0 accent-primary"
            checked={selected.has(c.name)}
            disabled={!!c.fetched}
            onChange={() => toggle(c.name)}
          />
          <b className="shrink-0 font-semibold whitespace-nowrap">{c.name}</b>
          {c.fetched ? (
            <Badge title="同一来源的同名技能已在库中;要取新内容请在技能详情里点「更新」">已入库</Badge>
          ) : c.new ? (
            <Badge tone="accent" title="上次从这个来源入库之后才出现的技能">新技能</Badge>
          ) : null}
          <span className="min-w-0 grow truncate font-mono text-[11px] text-muted-foreground">{rel !== c.name ? rel : ""}</span>
          {c.plugin_name && <span className="shrink-0 text-[11px] text-subtle">{c.plugin_name}</span>}
          {github && <Audit previewID={preview.id} candidate={c} />}
        </label>
      </ListRow>
    );
  };

  return (
    <Modal
      title="入库预览"
      wide
      onClose={onClose}
      footer={
        <>
          <Button onClick={onBack}>返回</Button>
          <span className="grow" />
          <span className="text-[11px] text-muted-foreground">
            已选 {selected.size} / {fresh.length}
          </span>
          <Button variant="primary" disabled={selected.size === 0} loading={fetching} onClick={fetchSelected}>
            入库所选
          </Button>
        </>
      }
    >
      <div className="fetch-source flex min-w-0 items-center gap-2 text-xs">
        <span className="shrink-0 text-muted-foreground">识别为</span>
        <Badge tone="accent" className="shrink-0">
          {sourceKindName[preview.source.type] ?? preview.source.type}
        </Badge>
        <span className="min-w-0 truncate font-mono" title={preview.source.label}>
          {preview.source.label}
        </span>
        {preview.source.ref && <span className="shrink-0 text-muted-foreground">分支 {preview.source.ref}</span>}
      </div>
      {tokenMissing && (
        <Notice tone="warn">设置页没有填 GitHub 令牌，也没有设置 GITHUB_TOKEN / GH_TOKEN,匿名访问 GitHub API 容易被限流。</Notice>
      )}
      <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
        <span>
          {candidates.length} 个候选
          {preview.revision ? ` · ${preview.revision.slice(0, 12)}` : ""}
        </span>
        <span className="grow" />
        <Button variant="link" onClick={() => setSelected(new Set(fresh.map((c) => c.name)))}>
          全选
        </Button>
        <Button variant="link" onClick={() => setSelected(new Set())}>
          全不选
        </Button>
      </div>
      {candidates.length > 0 ? (
        <ListBox scroll>
          {[...groups].map(([dir, members]) =>
            grouped ? (
              <div key={dir}>
                <GroupHeader dir={dir} members={members} selected={selected} onToggle={() => toggleGroup(members)} />
                {members.map((c) => row(c, dir))}
              </div>
            ) : (
              members.map((c) => row(c, dir))
            ),
          )}
        </ListBox>
      ) : (
        <Notice tone="warn">来源里没有找到可入库的技能。</Notice>
      )}
      <Diagnostics
        summary={`${preview.diagnostics?.length} 个路径未成为候选`}
        items={(preview.diagnostics ?? []).map((d) => ({ key: d.path, reason: d.reason }))}
      />
    </Modal>
  );
}

// GroupHeader 是一个目录的分组标题：勾选框勾上 / 取消组里所有没入库的候选，部分勾选时显示为半选。
function GroupHeader({
  dir,
  members,
  selected,
  onToggle,
}: {
  dir: string;
  members: Candidate[];
  selected: Set<string>;
  onToggle: () => void;
}) {
  const fresh = members.filter((c) => !c.fetched);
  const picked = fresh.filter((c) => selected.has(c.name)).length;
  return (
    <label className="sticky top-0 z-1 flex cursor-pointer items-center gap-1.5 border-t border-border-subtle bg-gray-50/95 px-2.5 py-1 text-[11px] font-semibold text-foreground-secondary first:border-t-0">
      <input
        type="checkbox"
        className="accent-primary"
        checked={fresh.length > 0 && picked === fresh.length}
        disabled={fresh.length === 0}
        ref={(el) => void (el && (el.indeterminate = picked > 0 && picked < fresh.length))}
        onChange={onToggle}
      />
      <span className="font-mono">{dir || "/"}</span>
      <span className="font-normal text-muted-foreground">
        {picked} / {fresh.length}
      </span>
    </label>
  );
}

// Audit 按需查询 skills.sh 的审计(点击才联网),查到后概括成一个标记：只供参考，不影响入库。
function Audit({ previewID, candidate }: { previewID: FetchPreviewID; candidate: Candidate }) {
  const [audit, setAudit] = useState<SkillAudit | null | "loading" | undefined>(undefined);
  const load = (e: React.MouseEvent) => {
    e.preventDefault();
    setAudit("loading");
    LibraryService.GetCandidateAudit(previewID, candidate.name).then(
      (a) => setAudit(a),
      () => setAudit(undefined),
    );
  };
  if (audit === undefined) {
    return (
      <button type="button" className="shrink-0 text-[11px] whitespace-nowrap text-subtle hover:text-primary" onClick={load}>
        查看审计
      </button>
    );
  }
  if (audit === "loading") return <Spinner className="text-muted-foreground" />;
  return <AuditBadge audit={audit} onRetry={load} />;
}
