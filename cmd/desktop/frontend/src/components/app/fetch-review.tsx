// 远程安装的第二步：浏览一次 Fetch 暂存的候选(含 skills.sh 审计与诊断),勾选后 InstallFetch。
// 安装方式(GitHub / URL / 商店 / 市场)都在拿到 Fetch 后进入这里。
import { useState } from "react";
import { Browser } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  AuditAvailability,
  AuditStatus,
  type Fetch,
  type FetchCandidates,
  type FetchSkill,
} from "@bindings/internal/library";
import { Modal, useUI } from "@/lib/ui";

export function FetchReview({ fetch, onClose, onBack }: { fetch: Fetch; onClose: () => void; onBack?: () => void }) {
  const ui = useUI();
  const [candidates, setCandidates] = useState<FetchCandidates>(fetch);
  const skills = candidates.skills ?? [];
  // 默认勾选尚未安装的候选。
  const [selected, setSelected] = useState<Set<string>>(
    () => new Set(skills.filter((s) => !s.installed).map((s) => s.name)),
  );
  const [installing, setInstalling] = useState(false);

  const toggle = (name: string) =>
    setSelected((set) => {
      const next = new Set(set);
      if (!next.delete(name)) next.add(name);
      return next;
    });

  // 重新查一遍库内安装状态(ListFetchSkills 每次现查)。
  const refresh = () =>
    ui.run(() => LibraryService.ListFetchSkills(fetch.id), { reload: false }).then((c) => c && setCandidates(c));

  const install = async () => {
    setInstalling(true);
    const names = [...selected];
    const installed = await ui.run(() => LibraryService.InstallFetch(fetch.id, names), {
      success: (list) => `已安装 ${list?.length ?? 0} 个技能`,
    });
    setInstalling(false);
    if (installed) onClose();
  };

  return (
    <Modal
      title="选择要安装的技能"
      wide
      onClose={onClose}
      footer={
        <>
          {onBack && (
            <button type="button" className="pill-btn" onClick={onBack}>
              返回
            </button>
          )}
          <button type="button" className="link" onClick={refresh}>
            刷新安装状态
          </button>
          <span className="grow" />
          <span className="tiny muted">
            已选 {selected.size} / {skills.length}
          </span>
          <button
            type="button"
            className="pill-btn accent"
            disabled={selected.size === 0 || installing}
            aria-busy={installing}
            onClick={install}
          >
            {installing ? <span className="spin" /> : null}
            安装所选
          </button>
        </>
      }
    >
      <div className="row tiny muted">
        <span>
          {skills.length} 个候选{fetch.ref ? ` · 来源版本 ${fetch.ref.slice(0, 12)}` : ""}
        </span>
        <span className="grow" />
        <button type="button" className="link" onClick={() => setSelected(new Set(skills.map((s) => s.name)))}>
          全选
        </button>
        <button type="button" className="link" onClick={() => setSelected(new Set())}>
          全不选
        </button>
      </div>
      {skills.length > 0 ? (
        <div className="list-box">
          {skills.map((skill) => (
            <label key={skill.path + skill.name} className={`list-row clickable ${selected.has(skill.name) ? "on" : ""}`}>
              <input type="checkbox" checked={selected.has(skill.name)} onChange={() => toggle(skill.name)} />
              <div className="grow">
                <div className="row">
                  <b>{skill.name}</b>
                  {skill.plugin_name && <span className="badge">{skill.plugin_name}</span>}
                  {skill.installed ? <span className="badge" title="同一来源的同名技能已在库中;要取新内容请在技能详情里点「更新」">已安装</span> : null}
                  <span className="grow" />
                  <AuditBadge skill={skill} />
                </div>
                {skill.description && <div className="desc">{skill.description}</div>}
                <div className="mono tiny muted">{skill.path || "/"}</div>
              </div>
            </label>
          ))}
        </div>
      ) : (
        <div className="notice warn">来源里没有找到可安装的技能。</div>
      )}
      {(candidates.diagnostics ?? []).length > 0 && (
        <details>
          <summary className="tiny muted">{candidates.diagnostics!.length} 个路径未成为候选</summary>
          <div className="list-box" style={{ marginTop: 6 }}>
            {candidates.diagnostics!.map((d) => (
              <div key={d.path} className="list-row">
                <span className="mono tiny">{d.path}</span>
                <span className="grow" />
                <span className="tiny muted">{d.reason}</span>
              </div>
            ))}
          </div>
        </details>
      )}
    </Modal>
  );
}

// AuditBadge 概括 skills.sh 的审计：只供参考，不影响安装。
function AuditBadge({ skill }: { skill: FetchSkill }) {
  const audit = skill.audit;
  if (!audit) return null;
  if (audit.availability === AuditAvailability.AuditNone) return <span className="badge">无审计</span>;
  if (audit.availability === AuditAvailability.AuditUnavailable) return <span className="badge">审计不可用</span>;
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
