// 筛选"更新"时的更新列表：每个有要处理的更新的集合一行，可以查看更新计划或按默认勾选直接更新。
import { useEffect, useRef, useState } from "react";
import type { CancellablePromise } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import type { Collection, CollectionID, Skill } from "@bindings/internal/library";
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
import { knownRisks, ReadRetries, rememberPlanRisks, snapshotRiskKey } from "@/lib/plan-risks";
import { useBusy, useUI } from "@/lib/ui";
import { riskSummary } from "@/features/update/risks";
import { openUpdate, updateWithDefaults } from "@/features/update/update";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { InfoTip } from "@/components/app/parts";
import { barClass, barRowClass } from "./heads";

// UpdatesHead 是筛选"更新"时的更新列表：每个有要处理的更新的集合一行——来源版本从哪到哪、哪些技能有变化、风险提示。
// 信得过的直接"更新"(按默认勾选),不放心的"查看"打开更新计划。集合里只有一个技能时直接显示技能名。
// 按钮的主次固定，不随集合个数、风险读没读完而变：每一行的「更新」都是绿胶囊(列表里反复出现的主操作),「查看」是普通按钮;
// 风险(改动了脚本等)用「注意」一级的标签紧跟在名字后面，读回来之前之后按钮都不变。
// 最上面一行是整页的：几个来源有更新、对整页都成立的说明(悬停看)和「全部更新」(强调按钮，和下面各行的「更新」分开);
// 没有能直接更新的集合时「全部更新」照样在、不能点，悬停说明为什么。来源版本和哪些技能有变化写在每个集合的第二行。
// 更新会换掉用户修改的技能(Skill.edited)在更新计划里默认不勾：这一行写明;都是这种时没有能直接更新的，"更新…"只打开更新计划。
export function UpdatesHead() {
  const s = useSnapshot();
  const busy = useBusy();
  const t = useT().list;
  const collections = updatableCollections(s);
  const risks = usePlanRisks(s, collections);
  const direct = collections.filter((c) => collectionSkills(s, c).some((x) => pending(x) && updateAvailable(x) && !x.edited));
  return (
    <div className={cn(barClass, "px-3")}>
      <div className={cn(barRowClass, "updates-top")}>
        <span className="flex min-w-0 items-center gap-1.5 text-body text-foreground-secondary">
          <span className="truncate">{t.updatesFrom(collections.length)}</span>
          <InfoTip tip={t.updatesFooter} />
        </span>
        <span className="grow" />
        <Button
          variant="accent"
          className="update-all"
          disabled={busy || !direct.length}
          title={direct.length ? undefined : t.updateAllNone}
          onClick={() => updateWithDefaults(direct.map((c) => c.id))}
        >
          {t.updateAll}
        </Button>
      </div>
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
          <div key={c.id} className="update-row flex min-w-0 flex-col gap-0.5 border-t border-row-line pt-2">
            <div className={barRowClass}>
              {/* 名字不跟风险标签抢位置：它的宽度只看自己(最多占一半),风险读回来前后不变。 */}
              <b className="max-w-1/2 min-w-0 shrink-0 truncate font-semibold">{single ? skills[0].name : sourceLabel(s, c.source)}</b>
              {/* 风险读回来之后才出现：只占名字后面的空白，放不下时自己截短(悬停看全),不挤名字和按钮。 */}
              {risk && (
                <Badge tone="warn" className="update-risk min-w-0 shrink" title={`${risk}\n${t.riskTitle}`}>
                  <span className="truncate">{risk}</span>
                </Badge>
              )}
              <span className="grow" />
              {plain.length === 0 && edited.length > 0 ? (
                <Button onClick={() => openUpdate(c.id)} title={t.updateEditedTitle}>
                  {t.updateEdited}
                </Button>
              ) : (
                <Button onClick={() => openUpdate(c.id)}>
                  {t.view}
                </Button>
              )}
              {plain.length > 0 && (
                <Button variant="pill" className="update-direct" disabled={busy} onClick={() => updateWithDefaults([c.id])}>
                  {t.update}
                </Button>
              )}
            </div>
            <span className="min-w-0 truncate text-caption text-muted-foreground" title={facts.join(" · ")}>
              {facts.join(" · ")}
            </span>
          </div>
        );
      })}
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

// 更新列表上的风险提示要读一次更新计划(要连来源),同一份差异只读一次(见 lib/plan-risks;打开过的更新计划读到的也算)。
// 读失败的(包括调用被取消、拿到空的计划)隔一会儿再读，最多再读两次;离开更新列表时取消还在读的。
// 生成更新计划会写下这次读到的检查结论：来源在检查更新之后又变了时，计划和快照对不上，重读快照让更新列表、计数跟上;
// 重读后快照仍停在旧的键上时不再反复读来源，快照换了键才重新算。
// 记的是风险本身，文案在渲染时按当前语言生成，切换语言后跟着变。
const retryDelay = 2000;
const maxTries = 3;

function usePlanRisks(s: Snapshot, collections: Collection[]): Map<CollectionID, string> {
  const ui = useUI();
  const keys = collections.map((c) => {
    const skills = collectionSkills(s, c);
    return [c.id, snapshotRiskKey(c.id, skills), skills] as const;
  });
  const [version, setVersion] = useState(0);
  const calls = useRef(new Map<string, CancellablePromise<unknown>>());
  const settled = useRef(new Set<string>());
  const retries = useRef(new ReadRetries(retryDelay, maxTries));
  const timers = useRef(new Set<number>());
  const wanted = keys.map(([, key]) => key).join("|");

  useEffect(() => {
    const running = calls.current;
    const waiting = timers.current;
    return () => {
      for (const call of running.values()) void call.cancel();
      running.clear();
      waiting.forEach((t) => window.clearTimeout(t));
      waiting.clear();
    };
  }, []);

  useEffect(() => {
    const current = new Set(keys.map(([, key]) => key));
    for (const key of settled.current) if (!current.has(key)) settled.current.delete(key);
    for (const [id, key, skills] of keys) {
      if (knownRisks(key, skills) || calls.current.has(key) || settled.current.has(key) || !retries.current.canRead(key, Date.now())) continue;
      retries.current.started(key);
      const call = LibraryService.PlanUpdate(id);
      calls.current.set(key, call);
      let failed = false;
      call
        .then(
          (plan) => {
            const actual = rememberPlanRisks(plan);
            if (!actual) {
              failed = true;
              return;
            }
            if (actual === key) return;
            settled.current.add(key);
            ui.reload();
          },
          () => (failed = true),
        )
        .finally(() => {
          if (calls.current.get(key) !== call) return;
          calls.current.delete(key);
          if (!failed) return setVersion((n) => n + 1);
          retries.current.failed(key, Date.now());
          const timer = window.setTimeout(() => {
            timers.current.delete(timer);
            setVersion((n) => n + 1);
          }, retryDelay);
          timers.current.add(timer);
        });
    }
  }, [wanted, version]);

  return new Map(keys.map(([id, key, skills]) => [id, riskSummary(knownRisks(key, skills) ?? [])]));
}
