// 历史：集合(或某个技能)的每次更新、跳过、回滚各一条，最新的在前。
// 更新记录可以回滚、重试没换上的地方;跳过的记录可以找回来再更新(打开更新计划，默认勾上那几个)。
import { useEffect, useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ReinstallOutcome,
  UpdateRecordKind,
  type RecordedSkill,
  type UpdateHistoryFilter,
  type UpdateRecord,
  type VersionDiff,
} from "@bindings/internal/library";
import { formatDate } from "@/lib/content";
import type { Snapshot } from "@/lib/library";
import { shortRevision } from "@/lib/model";
import { Modal, useUI } from "@/lib/ui";
import { openUpdate, rollbackOutcome, rollbackUpdate } from "@/lib/update";
import { cn } from "@/lib/utils";
import { Badge, type Tone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Spinner } from "@/components/ui/spinner";

const kindText: Record<string, { text: string; tone: Tone }> = {
  [UpdateRecordKind.UpdateRecordUpdate]: { text: "更新", tone: "accent" },
  [UpdateRecordKind.UpdateRecordSkip]: { text: "跳过", tone: "neutral" },
  [UpdateRecordKind.UpdateRecordRollback]: { text: "回滚", tone: "warn" },
};

export function UpdateHistory({ s, filter, title, onClose }: { s: Snapshot; filter: UpdateHistoryFilter; title: string; onClose: () => void }) {
  const [records, setRecords] = useState<UpdateRecord[] | null>(null);
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    const call = LibraryService.ListUpdateHistory(filter);
    call.then((r) => setRecords(r ?? []), () => setRecords([]));
    return () => void call.cancel();
  }, [filter, generation]);

  return (
    <Modal
      title={title}
      wide
      onClose={onClose}
      footer={
        <Button variant="primary" onClick={onClose}>
          关闭
        </Button>
      }
    >
      {records === null ? (
        <Hint className="flex items-center gap-1.5">
          <Spinner /> 正在读取历史…
        </Hint>
      ) : records.length === 0 ? (
        <Hint>还没有更新、跳过或回滚的记录。</Hint>
      ) : (
        <ListBox>
          {records.map((r) => (
            <Record key={r.id} s={s} record={r} all={records} onChange={() => setGeneration((n) => n + 1)} />
          ))}
        </ListBox>
      )}
    </Modal>
  );
}

function Record({ s, record, all, onChange }: { s: Snapshot; record: UpdateRecord; all: UpdateRecord[]; onChange: () => void }) {
  const ui = useUI();
  const kind = kindText[record.kind] ?? { text: record.kind, tone: "neutral" as Tone };
  const skills = record.skills ?? [];
  const installs = record.installs ?? [];
  const update = record.kind === UpdateRecordKind.UpdateRecordUpdate;
  const rollback = record.kind === UpdateRecordKind.UpdateRecordRollback;
  const failed = update ? installs.filter((i) => i.outcome === ReinstallOutcome.ReinstallFailed) : [];
  const rolledBack = !!record.rolled_back_by;
  const target = rollback ? all.find((r) => r.id === record.rolls_back) : undefined;

  const facts = [
    // 回滚记录的来源版本是被回滚那次更新换到的，不是回滚后的，所以只说回滚了哪次。
    record.revision && !rollback && `${update ? "换到" : "来源版本"} ${shortRevision(record.revision)}`,
    record.whole_revision && "整个来源版本",
    rollback && (target ? `回滚了 ${formatDate(target.created_at)} 的更新` : "回滚了一次更新"),
    update && installs.length > 0 && `重新安装了 ${installs.length - failed.length} 个地方${failed.length ? `,${failed.length} 个没换上` : ""}`,
    rollback &&
      installs.length > 0 &&
      `装回了 ${installs.filter((i) => rollbackOutcome(i.outcome).tone === "ok").length} / ${installs.length} 个地方`,
  ].filter(Boolean);

  const retry = async () => {
    const results = await ui.run(() =>
      LibraryService.RetryUpdateInstalls(
        record.id,
        failed.map((i) => ({ skill_id: i.skill_id, scope: i.scope, overwrite: false })),
      ),
    );
    if (!results) return;
    const left = results.filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed).length;
    ui.toast(left ? `还有 ${left} 个地方没换上` : "没换上的地方都换上了", left ? "error" : "ok");
    onChange();
  };

  return (
    <ListRow className={cn("update-record items-start", rolledBack && "opacity-70")}>
      <Badge tone={kind.tone} className="mt-px">
        {kind.text}
      </Badge>
      <div className="flex min-w-0 grow flex-col gap-1">
        <div className="flex flex-wrap items-center gap-x-2 text-[11px] text-muted-foreground">
          <span className="text-foreground">{formatDate(record.created_at)}</span>
          {facts.map((f) => (
            <span key={String(f)}>{f}</span>
          ))}
          {rolledBack && <Badge>已回滚</Badge>}
        </div>
        <div className="flex flex-col gap-0.5">
          {skills.map((x) => (
            <SkillLine key={x.skill_id} skill={x} diffable={update || rollback} />
          ))}
        </div>
        {(record.commits?.length ?? 0) > 0 && (
          <details>
            <summary className="cursor-pointer text-[11px] text-muted-foreground">提交说明 · {record.commits!.length} 条</summary>
            <div className="mt-1 flex flex-col gap-0.5">
              {record.commits!.map((c) => (
                <div key={c.sha} className="flex gap-2 text-[11px]" title={c.message}>
                  <span className="shrink-0 font-mono text-muted-foreground">{c.sha.slice(0, 7)}</span>
                  <span className="min-w-0 truncate">{c.message.split("\n")[0]}</span>
                </div>
              ))}
            </div>
          </details>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        {update && !rolledBack && failed.length > 0 && (
          <Button size="sm" onClick={retry}>
            重试没换上的
          </Button>
        )}
        {update && !rolledBack && (
          <Button
            size="sm"
            onClick={async () => {
              await rollbackUpdate(ui, s, record.id);
              onChange();
            }}
          >
            回滚
          </Button>
        )}
        {record.kind === UpdateRecordKind.UpdateRecordSkip && s.collections.some((c) => c.id === record.collection_id) && (
          <Button
            size="sm"
            onClick={() => openUpdate(ui, s, record.collection_id, { select: skills.map((x) => x.skill_id) })}
            title="打开更新计划，默认勾上这次跳过的技能;来源之后又变了时按最新的内容算"
          >
            再看更新计划
          </Button>
        )}
      </div>
    </ListRow>
  );
}

// SkillLine 是记录里的一个技能;更新和回滚可以展开看从哪个版本换到哪个版本改了哪些文件(技能已移除时版本也不在了)。
function SkillLine({ skill, diffable }: { skill: RecordedSkill; diffable: boolean }) {
  const ui = useUI();
  const [diff, setDiff] = useState<VersionDiff | null>(null);
  const toggle = async () => {
    if (diff) return setDiff(null);
    const d = await ui.run(() => LibraryService.DiffVersions(skill.from_version_id, skill.to_version_id), { reload: false });
    if (d) setDiff(d);
  };
  const files = diff
    ? [
        ...(diff.added ?? []).map((p) => ["added", "新增", p] as const),
        ...(diff.changed ?? []).map((p) => ["changed", "修改", p] as const),
        ...(diff.deleted ?? []).map((p) => ["deleted", "删除", p] as const),
      ]
    : [];
  const color = { added: "text-ok-strong", changed: "text-warn-strong", deleted: "text-bad-strong" };
  return (
    <div>
      <div className="flex items-center gap-2">
        <b className="font-semibold">{skill.from_name ? `${skill.from_name} → ${skill.name}` : skill.name}</b>
        {diffable && skill.from_version_id !== skill.to_version_id && (
          <Button variant="link" onClick={toggle}>
            {diff ? "收起" : "改了什么"}
          </Button>
        )}
      </div>
      {diff && (
        <div className="pl-2">
          {files.map(([k, text, p]) => (
            <div key={p} data-diff={k} className={cn("font-mono text-[11px]", color[k])}>
              {text} {p}
            </div>
          ))}
          {files.length === 0 && <div className="text-[11px] text-muted-foreground">文件内容相同</div>}
        </div>
      )}
    </div>
  );
}
