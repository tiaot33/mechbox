// 重新安装：先现查各处状态，只换不是当前版本的几处;被改过的逐处由用户勾选是否覆盖，
// 文件夹不在了的逐处由用户选不动、装回去还是卸载(默认勾哪些见 lib/consent.ts)。结果逐条列出，没换上的写明原因。
import { useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ReinstallOutcome,
  SkillScopeStatus,
  type SkillScopeFilter,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import {
  chooseMissing,
  missingChoice,
  missingFolders,
  noReinstallChoices,
  overwriteCount,
  overwriting,
  planReinstall,
  putBackAll,
  reinstallsNothing,
  reinstallTargets,
  staleLinksOf,
  toggleOverwrite,
  type ReinstallChoices,
} from "@/lib/consent";
import { messages, useT } from "@/lib/i18n";
import { currentSnapshot, useSnapshot } from "@/lib/library";
import { agentName, groupName, skillName } from "@/lib/scope";
import { canInstall, linkNote, placeKey, sourceLatestHere } from "@/lib/installs";
import { eachPlace, placeCount, placeNames, scopeLabel, sharedWithNote } from "@/lib/places";
import { shortPath, wslTitle } from "@/lib/paths";
import { Modal, ui } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { ListBox, ListRow } from "@/components/ui/list";
import { Segmented } from "@/components/ui/segmented";
import { Notice } from "@/components/ui/notice";
import { eachCall, showResults } from "@/components/app/results";
import { failedRow, failedAttemptRows } from "@/lib/result-rows";
import { confirmLinkWrites } from "@/components/app/link-notes";

// reinstall 把 filter 范围内不是当前版本的各处换成当前版本。先现查状态：快照可能已落后于磁盘，
// 而重新安装只换列出的几处。被改过的几处逐处让用户勾选是否覆盖，没勾选的保持原样。
// 文件夹不在了的几处可能是用户有意删掉的：只点了这一处时直接装回去;批量时逐处让用户选不动、装回去还是卸载，默认不动。
// what 是重新安装的是什么：技能名(经 reinstallSkill 加上引号)或作用域。filter 给几个时(合成一处的几个 Agent 各自的作用域)合起来看，
// 共用一个文件夹的几处只算一处。
export async function reinstall(filter: SkillScopeFilter | SkillScopeFilter[], what: string) {
  const s = currentSnapshot();
  const t = messages().actions;
  const filters = Array.isArray(filter) ? filter : [filter];
  const inspected = await ui.read(async () => eachPlace(s, (await Promise.all(filters.map((f) => LibraryService.InspectSkillScopes(f)))).flatMap((x) => x ?? [])));
  if (inspected === undefined) return;
  const single = filters.length === 1 && !!filters[0].skill_id && !!filters[0].scope;
  const { targets, modified, missing } = reinstallTargets(inspected ?? [], single);
  if (!targets.length) {
    // 项目不在这台机器上的几处没核对过，不说成「都是当前版本」。
    const off = (inspected ?? []).filter((p) => !canInstall(p)).length;
    ui.toast(off ? t.offMachineSkipped(what, off) : t.allCurrent(what));
    return;
  }
  let chosen = noReinstallChoices;
  if (modified.length || missing.length) {
    const picked = await ui.open<ReinstallChoices>((close) => (
      <ReinstallDialog modified={modified} missing={missing} nothing={(chosen) => reinstallsNothing(targets, single, chosen)} close={close} />
    ));
    if (!picked) return;
    chosen = picked;
  }
  const { requests, uninstalls, keptModified, keptMissing } = planReinstall(targets, single, chosen);
  if (!requests.length && !uninstalls.length) {
    ui.toast(t.nothingReinstalled(what, keptModified, keptMissing));
    return;
  }
  const requested = new Set(requests.map(placeKey));
  const written = (p: SkillScopeInspection) => requested.has(placeKey(p));
  const explained = (p: SkillScopeInspection) => p.status === SkillScopeStatus.SkillScopeModified;
  if (requests.length && !(await confirmLinkWrites(inspected ?? [], written, t.reinstallTitle(what), t.reinstall, explained))) return;
  const done = await ui.run(async () => {
    const results = requests.length ? ((await LibraryService.Reinstall(requests)) ?? []) : [];
    const removed = await eachCall(uninstalls, ({ place: p, consent }) => LibraryService.Uninstall(p.skill_id, p.scope, consent));
    const rows = failedAttemptRows(removed, (i) => `${skillName(s, uninstalls[i].place.skill_id)} · ${scopeLabel(s, uninstalls[i].place.scope)}`, t.notUninstalled);
    return { results, rows, uninstalled: removed.length - rows.length };
  }, { failed: (d) => d.rows.length > 0 || d.results.some((r) => r.outcome === ReinstallOutcome.ReinstallFailed) });
  if (!done) return;
  const failed = eachPlace(s, done.results.filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed));
  if (failed.length || done.rows.length) {
    await showResults(t.reinstallPartly, [...failed.map((r) => failedRow(s, r)), ...done.rows], t.kept(keptModified, keptMissing) || undefined);
    return;
  }
  const placed = placeCount(s, done.results.filter((r) => r.outcome === ReinstallOutcome.Reinstalled));
  ui.toast(t.reinstalled(placed, done.uninstalled, what, keptModified, keptMissing));
}

// ReinstallDialog 在重新安装前问用户两类地方：文件夹不在了的(不动、装回去还是卸载，默认不动)与被改过的(勾选的才覆盖，默认都不覆盖)。
// 这样选下去什么都不会做时(nothing,和真正执行时同一份计算，见 consent 的 reinstallsNothing),确认按钮点不了。
// 选择怎么记见 consent 的 ReinstallChoices,这里只经那几个函数读、改。
function ReinstallDialog({
  modified,
  missing,
  nothing: nothingFor,
  close,
}: {
  modified: SkillScopeInspection[];
  missing: SkillScopeInspection[];
  nothing: (chosen: ReinstallChoices) => boolean;
  close: (value?: ReinstallChoices) => void;
}) {
  const s = useSnapshot();
  const t = useT();
  const [chosen, setChosen] = useState<ReinstallChoices>(noReinstallChoices);
  const folders = missingFolders(missing);
  const overwrites = overwriteCount(chosen);
  const nothing = nothingFor(chosen);
  return (
    <Modal
      title={missing.length ? t.actions.reinstallConfirmTitle : t.actions.modifiedTitle}
      onClose={() => close()}
      footer={
        <>
          {folders.length > 0 && (
            <Button className="mr-auto" onClick={() => setChosen((c) => putBackAll(c, missing))}>
              {t.actions.putBackAll}
            </Button>
          )}
          <Button onClick={() => close()}>{t.common.cancel}</Button>
          <Button
            className="reinstall-confirm"
            variant={overwrites ? "destructive" : "primary"}
            disabled={nothing}
            title={nothing ? t.actions.reinstallNothingPicked : undefined}
            onClick={() => close(chosen)}
            autoFocus={!nothing}
          >
            {missing.length ? t.common.ok : overwrites ? t.actions.reinstallOverwrite(overwrites) : t.actions.reinstall}
          </Button>
        </>
      }
    >
      {folders.length > 0 && (
        <>
          <Notice alert>{t.actions.missingNotice(folders.length)}</Notice>
          <ListBox>
            {folders.map((places) => {
              const p = places[0];
              const groups = [...new Set(places.flatMap((x) => x.groups ?? []))];
              const shared = sharedWithNote(s, p);
              return (
                <ListRow key={placeKey(p)} compact>
                  <div className="min-w-0 grow">
                    <div>
                      {t.actions.missingPlace(skillName(s, p.skill_id), placeNames(s, places))}
                    </div>
                    <div className="truncate font-mono text-caption text-muted-foreground" title={wslTitle(p.folder)}>
                      {shortPath(s, p.folder)}
                    </div>
                    {shared.length > 0 && (
                      <div className="text-caption text-muted-foreground">{t.actions.missingShared(shared.map((code) => agentName(s, code)))}</div>
                    )}
                    {groups.length > 0 && (
                      <div className="text-caption text-muted-foreground">{t.actions.missingGrouped(groups.map((g) => groupName(s, g)))}</div>
                    )}
                    {!groups.length && staleLinksOf(places).length > 0 && (
                      <div className="missing-stale-links text-caption text-muted-foreground">
                        {t.actions.missingStaleLinks(staleLinksOf(places).map((link) => shortPath(s, link)))}
                      </div>
                    )}
                  </div>
                  <Segmented
                    className="shrink-0"
                    value={missingChoice(chosen, p)}
                    onChange={(value) => setChosen((c) => chooseMissing(c, p, value))}
                    options={[
                      { value: "keep", label: t.actions.leaveAlone },
                      { value: "putBack", label: t.actions.putBack },
                      ...(groups.length ? [] : [{ value: "uninstall" as const, label: t.actions.uninstall }]),
                    ]}
                  />
                </ListRow>
              );
            })}
          </ListBox>
        </>
      )}
      {modified.length > 0 && (
        <>
          <Notice alert>{t.actions.modifiedNotice}</Notice>
          <ListBox>
            {eachPlace(s, modified).map((p) => {
              const on = overwriting(chosen, p);
              return (
                <ListRow key={placeKey(p)} asChild compact clickable selected={on}>
                  <label>
                    <input type="checkbox" className="shrink-0 accent-primary" checked={on} onChange={() => setChosen((c) => toggleOverwrite(c, p))} />
                    <div className="min-w-0 grow">
                      <div>
                        {skillName(s, p.skill_id)} · {scopeLabel(s, p.scope)}
                      </div>
                      <div className="truncate font-mono text-caption text-muted-foreground" title={wslTitle(p.folder)}>
                        {shortPath(s, p.folder)}
                      </div>
                      {p.link_target && <div className="text-caption break-all text-muted-foreground">{linkNote(s, p.link_target)}</div>}
                      {sourceLatestHere(p) && <div className="reinstall-source-latest text-caption text-foreground">{t.actions.reinstallSourceLatest}</div>}
                    </div>
                  </label>
                </ListRow>
              );
            })}
          </ListBox>
        </>
      )}
    </Modal>
  );
}
