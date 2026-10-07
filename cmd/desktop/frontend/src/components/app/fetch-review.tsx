// 入库的第二步：浏览一次入库预览的候选与诊断，勾选后 Fetch 入库。
// 入库技能(本地目录 / GitHub / URL)与技能市场都在拿到入库预览后进入这里。
import { useState } from "react";
import { Browser } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  AuditAvailability,
  AuditStatus,
  type Candidate,
  type FetchPreview,
  type FetchPreviewID,
  type SkillAudit,
} from "@bindings/internal/library";
import { Modal, useUI } from "@/lib/ui";
import { Diagnostics, toggled } from "./parts";

interface FetchReviewProps {
  preview: FetchPreview;
  audits: boolean; // 只有 GitHub 来源在 skills.sh 收录范围内，才提供审计查询
  onClose: () => void;
  onBack: () => void;
}

export function FetchReview({ preview, audits, onClose, onBack }: FetchReviewProps) {
  const ui = useUI();
  const candidates = preview.candidates ?? [];
  const fresh = candidates.filter((c) => !c.fetched);
  // 默认勾选尚未入库的候选;已入库的不能再选(整批会因 AlreadyExists 失败)。
  const [selected, setSelected] = useState<Set<string>>(() => new Set(fresh.map((c) => c.name)));
  const [fetching, setFetching] = useState(false);

  const toggle = (name: string) => setSelected((set) => toggled(set, name));

  const fetchSelected = async () => {
    setFetching(true);
    const names = [...selected];
    const fetched = await ui.run(() => LibraryService.Fetch(preview.id, names), {
      success: (list) => `已入库 ${list?.length ?? 0} 个技能`,
    });
    setFetching(false);
    if (fetched) onClose();
  };

  return (
    <Modal
      title="入库预览"
      wide
      onClose={onClose}
      footer={
        <>
          <button type="button" className="pill-btn" onClick={onBack}>
            返回
          </button>
          <span className="grow" />
          <span className="tiny muted">
            已选 {selected.size} / {fresh.length}
          </span>
          <button
            type="button"
            className="pill-btn accent"
            disabled={selected.size === 0 || fetching}
            aria-busy={fetching}
            onClick={fetchSelected}
          >
            {fetching ? <span className="spin" /> : null}
            入库所选
          </button>
        </>
      }
    >
      <div className="row tiny muted">
        <span>
          {candidates.length} 个候选{preview.ref ? ` · ${preview.ref}` : ""}
          {preview.revision ? ` · ${preview.revision.slice(0, 12)}` : ""}
        </span>
        <span className="grow" />
        <button type="button" className="link" onClick={() => setSelected(new Set(fresh.map((c) => c.name)))}>
          全选
        </button>
        <button type="button" className="link" onClick={() => setSelected(new Set())}>
          全不选
        </button>
      </div>
      {candidates.length > 0 ? (
        <div className="list-box scroll">
          {candidates.map((c) => (
            <label
              key={c.path + c.name}
              className={`list-row compact clickable ${selected.has(c.name) ? "on" : ""}`}
              title={c.description || undefined}
            >
              {/* 每条一行，描述放在悬停提示里，二十来个候选一屏看完。 */}
              <input
                type="checkbox"
                checked={selected.has(c.name)}
                disabled={!!c.fetched}
                onChange={() => toggle(c.name)}
              />
              <b className="name">{c.name}</b>
              {c.plugin_name && <span className="badge">{c.plugin_name}</span>}
              {c.fetched ? (
                <span className="badge" title="同一来源的同名技能已在库中;要取新内容请在技能详情里点「更新」">
                  已入库
                </span>
              ) : null}
              <span className="mono tiny muted path grow">{c.path || "/"}</span>
              {audits && <Audit previewID={preview.id} candidate={c} />}
            </label>
          ))}
        </div>
      ) : (
        <div className="notice warn">来源里没有找到可入库的技能。</div>
      )}
      <Diagnostics
        summary={`${preview.diagnostics?.length} 个路径未成为候选`}
        items={(preview.diagnostics ?? []).map((d) => ({ key: d.path, reason: d.reason }))}
      />
    </Modal>
  );
}

// Audit 按需查询 skills.sh 的审计(点击才联网),查到后概括成一个标记：只供参考，不影响入库。
function Audit({ previewID, candidate }: { previewID: FetchPreviewID; candidate: Candidate }) {
  const [audit, setAudit] = useState<SkillAudit | null | "loading" | undefined>(undefined);
  const load = (e: React.MouseEvent) => {
    e.preventDefault();
    setAudit("loading");
    LibraryService.GetSkillAudit(previewID, candidate.name).then(
      (a) => setAudit(a),
      () => setAudit(undefined),
    );
  };
  if (audit === undefined) {
    return (
      <button type="button" className="link tiny" onClick={load}>
        查看审计
      </button>
    );
  }
  if (audit === "loading") return <span className="spin" />;
  if (!audit || audit.availability === AuditAvailability.AuditNone) return <span className="badge">无审计</span>;
  if (audit.availability === AuditAvailability.AuditUnavailable) {
    return (
      <button type="button" className="badge" title="点击重试" onClick={load}>
        审计不可用
      </button>
    );
  }
  const entries = audit.entries ?? [];
  const fail = entries.filter((e) => e.status === AuditStatus.AuditFail).length;
  const warn = entries.filter((e) => e.status === AuditStatus.AuditWarn).length;
  const tone = fail ? "bad" : warn ? "warn" : "ok";
  const text = fail ? `审计 ${fail} 项未通过` : warn ? `审计 ${warn} 项警告` : `审计通过 ${entries.length}`;
  const title = entries.map((e) => `${e.provider}:${e.status} ${e.summary}`).join("\n");
  const url = entries[0]?.detail_url;
  return (
    <button
      type="button"
      className={`status ${tone}`}
      title={title}
      onClick={(e) => {
        e.preventDefault();
        if (url) void Browser.OpenURL(url);
      }}
    >
      {text}
    </button>
  );
}
