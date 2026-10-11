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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { Spinner } from "@/components/ui/spinner";
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
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" disabled={!count || !inspections} loading={working} onClick={run}>
            {local ? "重新读取" : `更新 ${count} 个`}
          </Button>
        </>
      }
    >
      {local ? (
        <Hint>从目录读出「{label}」现在的内容成为新版本，并换到下面勾选的地方。</Hint>
      ) : (
        <>
          {revisions && <Hint>现在{revisions}。更新会换到来源的最新状态。</Hint>}
          <ListBox>
            {shown.map((x) => (
              <ListRow key={x.id} asChild compact clickable selected={checked.has(x.id)}>
                <label>
                  <input
                    type="checkbox"
                    className="shrink-0 accent-primary"
                    checked={checked.has(x.id)}
                    onChange={() => setChecked((set) => toggled(set, x.id))}
                  />
                  <b className="font-semibold whitespace-nowrap">{x.name}</b>
                  <Badge tone="warn">有新版本</Badge>
                </label>
              </ListRow>
            ))}
            {folded.length > 0 && (
              <ListRow compact>
                <span className="grow text-muted-foreground">
                  同一来源还有 {folded.length} 个技能有新版本({folded.map((x) => x.name).join("、")})
                </span>
                <Button variant="link" onClick={() => setExpanded(true)}>
                  展开
                </Button>
              </ListRow>
            )}
            {deleted.map((x) => (
              <ListRow key={x.id} compact>
                <b className="font-semibold whitespace-nowrap">{x.name}</b>
                <Badge>来源里已删除</Badge>
                <span className="grow text-[11px] text-muted-foreground">你的副本还在，可以继续用;要移除请在技能详情里操作</span>
              </ListRow>
            ))}
            {unchanged.length > 0 && (
              <ListRow compact className="text-muted-foreground">
                没变化:{unchanged.map((x) => x.name).join("、")}
              </ListRow>
            )}
          </ListBox>
          {splitting && <Notice tone="warn">只更新勾选的技能后,{label} 里的技能会不在同一个来源版本上。</Notice>}
        </>
      )}

      <div className="mt-1 text-[13.5px] font-semibold">装在这些地方，一起换成新版</div>
      {!inspections ? (
        <Hint className="flex items-center gap-1.5">
          <Spinner /> 正在查看各处的状态…
        </Hint>
      ) : rows.length === 0 ? (
        <Hint>勾选的技能还没有装到任何地方，只更新技能库。</Hint>
      ) : (
        <ListBox>
          {rows.map((r) => {
            const on = scopeOn(r);
            return (
              <ListRow key={r.key} asChild compact clickable={!r.rootMissing} selected={on}>
                <label>
                  <input
                    type="checkbox"
                    className="shrink-0 accent-primary"
                    checked={on}
                    disabled={r.rootMissing}
                    onChange={() => setScopeChoice((m) => new Map(m).set(r.key, !on))}
                  />
                  <span className="whitespace-nowrap">{scopeLabel(s, r.places[0].scope)}</span>
                  <span className="min-w-0 grow truncate text-[11px] text-muted-foreground">
                    {r.places.map((p) => name(p.skill_id)).join("、")}
                  </span>
                  {r.rootMissing ? (
                    <Badge tone="bad">项目不在这台机器上，装不上</Badge>
                  ) : r.modified.length > 0 ? (
                    <Badge tone="warn">
                      你在这里改过{r.modified.length < r.places.length ? ` ${r.modified.map((p) => name(p.skill_id)).join("、")}` : ""}
                      ,换新版会覆盖你的修改
                    </Badge>
                  ) : null}
                </label>
              </ListRow>
            );
          })}
        </ListBox>
      )}
      <Hint>没勾的地方保持原样，之后可以在那里单独重新安装。</Hint>
    </Modal>
  );
}
