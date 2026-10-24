// 一次确认入库未纳管技能时列表里的行：按来源分组的一条、同名冲突里的一份，以及改来源链接。
import type { PlannedUnmanagedSkill } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { ListRow } from "@/components/ui/list";
import { OutsideBadge, SameNameInLibrary, sourceOf } from "./fetch-unmanaged-parts";

// SourceEdit 是一条的来源链接怎么改：改过的链接(没改过时为 undefined)、是不是正在改，以及改的几个操作。
export interface SourceEdit {
  link?: string;
  editing: boolean;
  onEdit: () => void; // 点"改来源":第一次点时先填上认出的来源
  onLink: (link: string) => void;
  onDone: () => void;
}

// ChangedSource 是改过来源的那一条最终跟着谁，没改过时不显示。
function ChangedSource({ planned, link, className }: { planned: PlannedUnmanagedSkill; link?: string; className: string }) {
  const t = useT().unmanaged;
  if (link === undefined) return null;
  const { input } = sourceOf(planned, link);
  return <span className={className}>{input ? t.followsNow(input) : t.noSourceNow}</span>;
}

function EditSourceButton({ onEdit }: { onEdit: () => void }) {
  const t = useT().unmanaged;
  return (
    <button
      type="button"
      className="fetch-unmanaged-edit shrink-0 text-[11px] whitespace-nowrap text-subtle hover:text-primary"
      onClick={(e) => {
        e.preventDefault();
        onEdit();
      }}
    >
      {t.editSource}
    </button>
  );
}

// SourceEditor 是正在改来源时，这一条下面的链接输入框;回车收起。
export function SourceEditor({ edit }: { edit: SourceEdit }) {
  const t = useT().unmanaged;
  if (!edit.editing) return null;
  return (
    <ListRow compact className="pl-6">
      <Input
        className="fetch-unmanaged-source grow font-mono"
        autoFocus
        value={edit.link ?? ""}
        placeholder={t.linkPlaceholder}
        onChange={(e) => edit.onLink(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && edit.onDone()}
      />
    </ListRow>
  );
}

// PlannedRow 是按来源分组里的一条：勾选才入库;标出在项目仓库里的、项目 skills-lock.json 记录的、指向 Agent 目录以外的、
// 库里已有同名的，写明在哪几个 Agent 里，可以改来源。
export function PlannedRow({
  planned: p,
  picked,
  onToggle,
  home,
  inLibrary,
  agents,
  edit,
}: {
  planned: PlannedUnmanagedSkill;
  picked: boolean;
  onToggle: () => void;
  home: string;
  inLibrary: boolean;
  agents: string;
  edit: SourceEdit;
}) {
  const t = useT().unmanaged;
  return (
    <div>
      <ListRow asChild compact clickable selected={picked} className="pl-6">
        <label title={p.description || undefined}>
          <input type="checkbox" className="shrink-0 accent-primary" checked={picked} onChange={onToggle} />
          <b className="fetch-unmanaged-name shrink-0 font-semibold whitespace-nowrap">{p.name}</b>
          {p.in_repository && <Badge tone="warn">{t.inRepository}</Badge>}
          {!p.in_repository && p.in_project_lock && (
            <Badge tone="warn" title={t.inLockTitle}>
              {t.inLock}
            </Badge>
          )}
          {p.link_outside && <OutsideBadge planned={p} home={home} />}
          {inLibrary && <SameNameInLibrary name={p.name} sourced={!!sourceOf(p, edit.link).input} />}
          <span className="min-w-0 grow truncate text-[11px] text-muted-foreground">{agents}</span>
          <ChangedSource planned={p} link={edit.link} className="fetch-unmanaged-changed min-w-0 truncate text-[11px] text-primary" />
          <EditSourceButton onEdit={edit.onEdit} />
        </label>
      </ListRow>
      <SourceEditor edit={edit} />
    </div>
  );
}

// ConflictRow 是同名不同内容的几份里的一份：单选，选中这份就只入库这份;写明在哪几个 Agent 里、什么时候改的、认出的来源。
export function ConflictRow({
  planned: p,
  group,
  picked,
  onPick,
  home,
  agents,
  edit,
}: {
  planned: PlannedUnmanagedSkill;
  group: string; // 单选组的名字(技能名)
  picked: boolean;
  onPick: () => void;
  home: string;
  agents: string;
  edit: SourceEdit;
}) {
  const t = useT().unmanaged;
  return (
    <div>
      <ListRow asChild compact clickable selected={picked} className="pl-6">
        <label title={p.description || undefined}>
          <input type="radio" name={`conflict-${group}`} className="shrink-0 accent-primary" checked={picked} onChange={onPick} />
          <span className="min-w-0 truncate">{agents}</span>
          {p.in_repository && <Badge tone="warn">{t.inRepository}</Badge>}
          {!p.in_repository && p.in_project_lock && <Badge tone="warn">{t.inLock}</Badge>}
          {p.link_outside && <OutsideBadge planned={p} home={home} />}
          <span className="grow" />
          <span className="shrink-0 text-[11px] text-muted-foreground">
            {p.modified_at ? t.modifiedAt(p.modified_at) : ""}
            {edit.link === undefined && p.source_input ? t.from(p.source_input) : ""}
          </span>
          <ChangedSource planned={p} link={edit.link} className="min-w-0 truncate text-[11px] text-primary" />
          <EditSourceButton onEdit={edit.onEdit} />
        </label>
      </ListRow>
      <SourceEditor edit={edit} />
    </div>
  );
}
