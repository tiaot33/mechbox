// 更新确认框：对一个集合发起，一次确认同时完成更新和重新安装(issue #131,流程见 lib/update.tsx)。
// 从集合发起时有新版本的技能默认全勾;从技能发起时只勾这一个，其余折叠。
// 本地目录来源也走这里，按钮叫"重新读取":读出目录当前内容成为新版本，并换到装过它的地方。
import { useEffect, useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import type { Collection, SkillID, SkillScopeInspection } from "@bindings/internal/library";
import type { Snapshot } from "@/lib/library";
import { revisionSummary, scopeLabel, type ScopeKey } from "@/lib/model";
import { Modal, useUI } from "@/lib/ui";
import { collectionUpdate, defaultScopeOn, reportUpdates, runUpdate, scopeRows, type ScopeRow } from "@/lib/update";
import { toggled } from "./parts";

export function UpdateDialog({
  s,
  collection,
  focus,
  onClose,
}: {
  s: Snapshot;
  collection: Collection;
  focus?: SkillID; // 从技能发起时的那个技能
  onClose: () => void;
}) {
  const ui = useUI();
  const u = collectionUpdate(s, collection, focus);
  const { local, label, available, unchanged, deleted } = u;
  const revisions = revisionSummary(u.skills);

  const [checked, setChecked] = useState<Set<SkillID>>(() => new Set(focus ? [focus] : available.map((x) => x.id)));
  const [expanded, setExpanded] = useState(!focus);
  const [scopeChoice, setScopeChoice] = useState<Map<ScopeKey, boolean>>(new Map());
  const [inspections, setInspections] = useState<SkillScopeInspection[] | null>(null);
  const [working, setWorking] = useState(false);

  // 现查各处状态：快照可能已落后于磁盘，而"你在这里改过"必须按现在的磁盘说。
  useEffect(() => {
    const call = LibraryService.InspectSkillScopes({});
    call.then((list) => setInspections(list ?? []), () => setInspections([]));
    return () => void call.cancel();
  }, []);

  const shown = expanded ? available : available.filter((x) => x.id === focus);
  const folded = available.filter((x) => !shown.includes(x));
  const splitting = !local && u.skills.length > 1 && available.some((x) => !checked.has(x.id));
  const rows = scopeRows(inspections ?? [], checked);
  const scopeOn = (r: ScopeRow) => !r.rootMissing && (scopeChoice.get(r.key) ?? defaultScopeOn(r));
  const name = (id: SkillID) => s.skills.find((x) => x.id === id)?.name ?? `#${id}`;

  const run = async () => {
    setWorking(true);
    const outcome = await runUpdate(ui, u, checked, rows, scopeOn);
    setWorking(false);
    if (!outcome) return;
    onClose();
    await reportUpdates(ui, s, [outcome]);
  };

  const count = checked.size;
  return (
    <Modal
      title={local ? `重新读取「${label}」` : `更新 ${label}`}
      wide
      onClose={onClose}
      footer={
        <>
          <button type="button" className="pill-btn" onClick={onClose}>
            取消
          </button>
          <button type="button" className="pill-btn accent" disabled={!count || working || !inspections} onClick={run}>
            {working ? <span className="spin" /> : null}
            {local ? "重新读取" : `更新 ${count} 个`}
          </button>
        </>
      }
    >
      {local ? (
        <p className="hint">从目录读出「{label}」现在的内容成为新版本，并换到下面勾选的地方。</p>
      ) : (
        <>
          {revisions && <p className="hint">现在{revisions}。更新会换到来源的最新状态。</p>}
          <div className="list-box">
            {shown.map((x) => (
              <label key={x.id} className={`list-row compact clickable ${checked.has(x.id) ? "on" : ""}`}>
                <input type="checkbox" checked={checked.has(x.id)} onChange={() => setChecked((set) => toggled(set, x.id))} />
                <b className="name">{x.name}</b>
                <span className="status warn">有新版本</span>
              </label>
            ))}
            {folded.length > 0 && (
              <div className="list-row compact">
                <span className="grow muted">
                  同一来源还有 {folded.length} 个技能有新版本({folded.map((x) => x.name).join("、")})
                </span>
                <button type="button" className="link" onClick={() => setExpanded(true)}>
                  展开
                </button>
              </div>
            )}
            {deleted.map((x) => (
              <div key={x.id} className="list-row compact">
                <b className="name">{x.name}</b>
                <span className="status">来源里已删除</span>
                <span className="grow muted tiny">你的副本还在，可以继续用;要移除请在技能详情里操作</span>
              </div>
            ))}
            {unchanged.length > 0 && (
              <div className="list-row compact muted">没变化:{unchanged.map((x) => x.name).join("、")}</div>
            )}
          </div>
          {splitting && (
            <div className="notice warn" style={{ marginTop: 8 }}>
              只更新勾选的技能后,{label} 里的技能会不在同一个来源版本上。
            </div>
          )}
        </>
      )}

      <div className="card-h" style={{ marginTop: 12 }}>
        <span>装在这些地方，一起换成新版</span>
      </div>
      {!inspections ? (
        <p className="hint">
          <span className="spin" /> 正在查看各处的状态…
        </p>
      ) : rows.length === 0 ? (
        <p className="hint">勾选的技能还没有装到任何地方，只更新技能库。</p>
      ) : (
        <div className="list-box">
          {rows.map((r) => {
            const on = scopeOn(r);
            return (
              <label key={r.key} className={`list-row compact ${r.rootMissing ? "" : "clickable"} ${on ? "on" : ""}`}>
                <input
                  type="checkbox"
                  checked={on}
                  disabled={r.rootMissing}
                  onChange={() => setScopeChoice((m) => new Map(m).set(r.key, !on))}
                />
                <span className="name">{scopeLabel(s, r.places[0].scope)}</span>
                <span className="grow muted tiny">{r.places.map((p) => name(p.skill_id)).join("、")}</span>
                {r.rootMissing ? (
                  <span className="status bad">项目不在这台机器上，装不上</span>
                ) : r.modified.length > 0 ? (
                  <span className="status warn">
                    你在这里改过{r.modified.length < r.places.length ? ` ${r.modified.map((p) => name(p.skill_id)).join("、")}` : ""}
                    ,换新版会覆盖你的修改
                  </span>
                ) : null}
              </label>
            );
          })}
        </div>
      )}
      <p className="hint">没勾的地方保持原样，之后可以在那里单独重新安装。</p>
    </Modal>
  );
}
