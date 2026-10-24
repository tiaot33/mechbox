// 用目录里的修改更新(issue #185):对巡检为被手动改过的那一处，把那里的内容作为技能在库里的新版本，并换到勾选的其他地方。
// 和从来源更新是同一种更新(流程见 lib/update.tsx):一次确认先换库、再逐处重新安装，留下记录、能回滚。
// 其他地方默认全勾，用户改过的地方默认不勾，项目不在这台机器上的不能勾。
import { useEffect, useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import type { Scope, Skill, SkillScopeInspection, UpdatePlan } from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import { useT } from "@/lib/i18n";
import type { LibraryError, Snapshot } from "@/lib/library";
import { folderKey, mergeProjectPlaces, scopeKey, scopeLabel, skillScopesOf } from "@/lib/model";
import { Modal, useUI } from "@/lib/ui";
import { canInstall, defaultInstallOn, reportUpdates, riskLabel, runUpdate, stalePlan } from "@/lib/update";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { Notice } from "@/components/ui/notice";
import { Spinner } from "@/components/ui/spinner";
import { Places, SkillDiff } from "./update-dialog";

const sectionClass = "mt-1 text-[13.5px] font-semibold";

export function ScopeUpdateDialog({ s, skill, scope, onClose }: { s: Snapshot; skill: Skill; scope: Scope; onClose: () => void }) {
  const ui = useUI();
  const t = useT().fromScope;
  const place = scopeLabel(s, scope);
  const title = t.title(place, skill.name);
  const [generation, setGeneration] = useState(0);
  const [plan, setPlan] = useState<UpdatePlan | null>(null);
  const [failed, setFailed] = useState<LibraryError | null>(null);
  const [installChoice, setInstallChoice] = useState<Map<string, boolean>>(new Map());
  const [path, setPath] = useState("");
  const [conflict, setConflict] = useState(false);
  const [working, setWorking] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setPlan(null);
    setFailed(null);
    const call = LibraryService.PlanUpdateFromScope(skill.id, scope);
    void ui.run(() => call, {
      reload: false,
      onError: (err) => {
        if (!cancelled) setFailed(err);
        return true;
      },
    }).then((p) => {
      if (cancelled || !p) return;
      const item = p.skills?.[0];
      const diff = item?.diff;
      setPlan(p);
      setInstallChoice(new Map());
      setConflict(false);
      setPath(item?.risks?.[0]?.path ?? [...(diff?.changed ?? []), ...(diff?.added ?? []), ...(diff?.deleted ?? [])].sort((a, b) => Number(b === "SKILL.md") - Number(a === "SKILL.md"))[0] ?? "");
    });
    return () => {
      cancelled = true;
      void call.cancel();
    };
    // ui 随忙碌状态变，不放进依赖，否则生成计划本身就会触发重新生成。
  }, [skill.id, scope.agent, scope.project_id, generation]);

  const replan = () => setGeneration((n) => n + 1);

  if (!plan) {
    return (
      <Modal title={title} wide onClose={onClose}>
        {failed ? (
          <>
            <Notice tone="bad" className="whitespace-pre-line">
              {t.readFailed}
              {"\n"}
              {describeError(failed)}
            </Notice>
            <div>
              <Button onClick={replan}>{t.retry}</Button>
            </div>
          </>
        ) : (
          <Hint className="flex items-center gap-1.5">
            <Spinner /> {t.reading(place)}
          </Hint>
        )}
      </Modal>
    );
  }

  const item = plan.skills![0];
  const places = plan.installs ?? [];
  // 计划里不列的其他地方和取内容的这一处是同一个文件夹(共用目录，或符号链接指向同一个文件夹):内容已经是这份修改。
  const same = skillScopesOf(s, skill.id).filter(
    (p) => scopeKey(p.scope) !== scopeKey(scope) && !places.some((x) => scopeKey(x.scope) === scopeKey(p.scope)),
  );
  const risks = item.risks ?? [];
  // 几个 Agent 共用一个文件夹时只能一起换，勾选按"技能 + 文件夹"算。
  const installOn = (p: SkillScopeInspection) => canInstall(p) && (installChoice.get(folderKey(p)) ?? defaultInstallOn(p));
  const locked = working || ui.busy;

  const onStale = (err: LibraryError) => {
    if (!stalePlan(err)) return false;
    setConflict(true);
    return true;
  };

  const run = async () => {
    setWorking(true);
    const o = await runUpdate(ui, plan, skill.name, new Set([skill.id]), installOn, place);
    setWorking(false);
    if (o === "conflict") return setConflict(true);
    if (!o) return;
    onClose();
    await reportUpdates(ui, s, [o]);
  };

  return (
    <Modal
      title={title}
      wide
      onClose={onClose}
      footer={
        <>
          <span className="grow" />
          <Button onClick={onClose}>{t.cancel}</Button>
          <Button variant="primary" disabled={conflict || (locked && !working)} loading={working} onClick={run}>
            {t.update}
          </Button>
        </>
      }
    >
      {conflict && (
        <Notice tone="warn" className="flex items-center gap-2">
          <span className="grow">{t.conflict}</span>
          <Button size="sm" onClick={replan}>
            {t.reread}
          </Button>
        </Notice>
      )}
      <Hint>{t.becomes(place, skill.name)}</Hint>
      {risks.length > 0 && (
        <Notice tone="warn" className="flex flex-col gap-0.5">
          {risks.map((r) => (
            <button key={r.path} type="button" className="update-risk flex items-center gap-2 text-left hover:underline" onClick={() => setPath(r.path)}>
              <span>{riskLabel(r)}</span>
              <span className="min-w-0 truncate font-mono text-[11px]">{r.path}</span>
            </button>
          ))}
        </Notice>
      )}
      <SkillDiff plan={plan} item={item} path={path} onPath={setPath} onError={onStale} />

      {same.length > 0 && (
        <Hint className="scope-update-same">
          {t.same(mergeProjectPlaces(s, same).map((p) => scopeLabel(s, p.scope)))}
        </Hint>
      )}
      {places.length > 0 ? (
        <>
          <div className={sectionClass}>{t.places}</div>
          <Places
            s={s}
            places={places}
            name={() => skill.name}
            on={installOn}
            onToggle={(p) => setInstallChoice((m) => new Map(m).set(folderKey(p), !installOn(p)))}
            modified={t.modifiedHere}
          />
          <Hint>{t.uncheckedNote}</Hint>
        </>
      ) : (
        same.length === 0 && <Hint>{t.nowhereElse(skill.name)}</Hint>
      )}
    </Modal>
  );
}
