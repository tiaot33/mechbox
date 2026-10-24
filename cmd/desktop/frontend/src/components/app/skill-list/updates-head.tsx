// 筛选"更新"时的更新列表：每个有要处理的更新的集合一行，可以查看更新计划或按默认勾选直接更新。
import { useEffect, useRef, useState } from "react";
import type { CancellablePromise } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import { PlannedChange, type Collection, type CollectionID, type Skill, type UpdateRisk } from "@bindings/internal/library";
import { messages, useT } from "@/lib/i18n";
import { useSnapshot, type Snapshot } from "@/lib/library";
import {
  collectionSkills,
  deletedInSource,
  pending,
  renamedTo,
  revisionSummary,
  revisionText,
  shortRevision,
  sourceLabel,
  updatableCollections,
  updateAvailable,
} from "@/lib/model";
import { useBusy, useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { riskSummary } from "@/features/update/risks";
import { openUpdate, updateWithDefaults } from "@/features/update/update";
import { Button } from "@/components/ui/button";
import { Icon } from "@/components/app/icons";
import { barClass, barRowClass } from "./heads";

// UpdatesHead 是筛选"更新"时的更新列表：每个有要处理的更新的集合一行——来源版本从哪到哪、哪些技能有变化、风险提示。
// 信得过的直接"更新"(按默认勾选),不放心的"查看"打开更新计划。集合里只有一个技能时直接显示技能名。
// 更新会换掉用户修改的技能(Skill.edited)在更新计划里默认不勾：这一行写明;都是这种时没有能直接更新的，"更新…"只打开更新计划。
export function UpdatesHead() {
  const s = useSnapshot();
  const busy = useBusy();
  const t = useT().list;
  const collections = updatableCollections(s);
  const risks = usePlanRisks(s, collections);
  const direct = collections.filter((c) => collectionSkills(s, c).some((x) => pending(x) && updateAvailable(x) && !x.edited));
  return (
    <div className={barClass}>
      {collections.map((c) => {
        const skills = collectionSkills(s, c);
        const single = skills.length === 1;
        const todo = skills.filter(pending);
        const available = todo.filter(updateAvailable);
        const plain = available.filter((x) => !x.edited);
        const edited = available.filter((x) => x.edited);
        const to = todo.map((x) => x.last_update_check?.revision).find(Boolean);
        const risk = risks.get(c.id);
        const names = (list: Skill[]) => list.map((x) => x.name);
        const facts = [
          single ? t.from(sourceLabel(s, c.source)) : "",
          revisionMove(skills, to),
          plain.length ? (single ? t.newVersion : t.changedSkills(names(plain))) : "",
          edited.length ? (single ? t.newVersionEdited : t.changedSkillsEdited(names(edited))) : "",
          ...todo.filter(renamedTo).map((x) => t.renamedSkill(x.name, renamedTo(x))),
          todo.some(deletedInSource) ? t.deletedSkills(names(todo.filter(deletedInSource))) : "",
        ].filter(Boolean);
        return (
          <div key={c.id} className={cn(barRowClass, "update-row")}>
            <b className="truncate [&:lang(en)]:shrink-0">{single ? skills[0].name : sourceLabel(s, c.source)}</b>
            <span className="min-w-0 grow truncate text-[11px] text-muted-foreground" title={facts.join(" · ")}>
              {facts.join(" · ")}
            </span>
            {risk && (
              <span className="shrink-0 text-[11px] text-warn-strong" title={t.riskTitle}>
                ⚠ {risk}
              </span>
            )}
            {plain.length === 0 && edited.length > 0 ? (
              <Button size="sm" onClick={() => openUpdate(c.id)} title={t.updateEditedTitle}>
                <Icon name="up" />
                {t.updateEdited}
              </Button>
            ) : (
              <Button size="sm" onClick={() => openUpdate(c.id)}>
                {t.view}
              </Button>
            )}
            {plain.length > 0 && (
              <Button size="sm" variant="primary" disabled={busy} onClick={() => updateWithDefaults([c.id])}>
                <Icon name="up" />
                {t.update}
              </Button>
            )}
          </div>
        );
      })}
      <div className={barRowClass}>
        <span className="grow text-[11px] text-muted-foreground">{t.updatesFooter}</span>
        {direct.length > 1 && (
          <Button size="sm" variant="primary" disabled={busy} onClick={() => updateWithDefaults(direct.map((c) => c.id))}>
            <Icon name="up" />
            {t.updateAll}
          </Button>
        )}
      </div>
    </div>
  );
}

// revisionMove 是"a1b2c3 → d4e5f6";集合里的技能不在同一个来源版本上时如实写出各在哪里。
function revisionMove(skills: Skill[], to: string | undefined): string {
  const from = revisionSummary(skills);
  if (!from || !to) return revisionText(from);
  const t = messages().list;
  return from.kind === "same" ? t.revisionMove(from.revision, shortRevision(to)) : t.revisionLatest(revisionText(from), shortRevision(to));
}

// 更新列表上的风险提示要读一次更新计划(要连来源);按"集合 + 来源最新版本 + 库里各技能的当前版本"记下来，
// 同一份差异只读一次。读失败的不记，下次再读;离开更新列表时取消还在读的。
// 生成更新计划会写下这次读到的检查结论：来源在检查更新之后又变了时，计划和快照对不上，重读快照让更新列表、计数跟上，
// 风险只按计划实际读到的来源版本记。重读后快照仍停在旧的键上时不再反复读来源，快照换了键才重新算。
// 记的是风险本身，文案在渲染时按当前语言生成，切换语言后跟着变。
const riskCache = new Map<string, UpdateRisk[]>();

const riskKey = (id: CollectionID, revision: string | undefined, versions: number[]) =>
  `${id}:${revision ?? ""}:${[...versions].sort((a, b) => a - b).join(",")}`;

function usePlanRisks(s: Snapshot, collections: Collection[]): Map<CollectionID, string> {
  const ui = useUI();
  const keys = collections.map((c) => {
    const skills = collectionSkills(s, c);
    const to = skills.map((x) => x.last_update_check?.revision).find(Boolean);
    return [c.id, riskKey(c.id, to, skills.map((x) => x.version_id))] as const;
  });
  const [, setVersion] = useState(0);
  const calls = useRef(new Map<string, CancellablePromise<unknown>>());
  const settled = useRef(new Set<string>());
  const wanted = keys.map(([, key]) => key).join("|");

  useEffect(() => {
    const running = calls.current;
    return () => {
      for (const call of running.values()) void call.cancel();
      running.clear();
    };
  }, []);

  useEffect(() => {
    const current = new Set(keys.map(([, key]) => key));
    for (const key of settled.current) if (!current.has(key)) settled.current.delete(key);
    for (const [id, key] of keys) {
      if (riskCache.has(key) || calls.current.has(key) || settled.current.has(key)) continue;
      const call = LibraryService.PlanUpdate(id);
      calls.current.set(key, call);
      call
        .then(
          (plan) => {
            const skills = plan.skills ?? [];
            const movable = skills.filter((p) => p.change === PlannedChange.PlannedChanged || p.renamed_to);
            const actual = riskKey(id, plan.revision, skills.map((p) => p.skill.version_id));
            riskCache.set(actual, movable.flatMap((p) => p.risks ?? []));
            if (actual === key) return;
            settled.current.add(key);
            ui.reload();
          },
          () => {},
        )
        .finally(() => {
          if (calls.current.get(key) === call) calls.current.delete(key);
          setVersion((n) => n + 1);
        });
    }
  }, [wanted]);

  return new Map(keys.map(([id, key]) => [id, riskSummary(riskCache.get(key) ?? [])]));
}
