// 重新安装：先现查各处状态，只换不是当前版本的几处;被改过的逐处由用户勾选是否覆盖，
// 文件夹不在了的逐处由用户选不动、装回去还是卸载(默认勾哪些见 lib/consent.ts)。结果逐条列出，没换上的写明原因。
import { useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ErrorCode,
  ReinstallOutcome,
  SkillScopeStatus,
  type SkillScopeFilter,
  type SkillScopeInspection,
} from "@bindings/internal/library";
import { toggled } from "@/lib/utils";
import { overwriteKey, placeKey, planReinstall, reinstallTargets, type MissingChoice, type ReinstallChoices } from "@/lib/consent";
import { describeError } from "@/lib/errors";
import { messages, useT } from "@/lib/i18n";
import { currentSnapshot, toLibraryError, useSnapshot } from "@/lib/library";
import {
  agentName,
  folderKey,
  groupName,
  linkNote,
  mergeProjectPlaces,
  scopeLabel,
  sharedWithNote,
  shortPath,
  skillName,
} from "@/lib/model";
import { Modal, ui } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { ListBox, ListRow } from "@/components/ui/list";
import { Segmented } from "@/components/ui/segmented";
import { Notice } from "@/components/ui/notice";
import { failedRow, showResults, type ResultRow } from "@/components/app/results";
import { confirmLinkWrites } from "@/components/app/link-notes";

// reinstall 把 filter 范围内不是当前版本的各处换成当前版本。先现查状态：快照可能已落后于磁盘，
// 而重新安装只换列出的几处。被改过的几处逐处让用户勾选是否覆盖，没勾选的保持原样。
// 文件夹不在了的几处可能是用户有意删掉的：只点了这一处时直接装回去;批量时逐处让用户选不动、装回去还是卸载，默认不动。
// what 是重新安装的是什么：技能名(经 reinstallSkill 加上引号)或作用域。
export async function reinstall(filter: SkillScopeFilter, what: string) {
  const s = currentSnapshot();
  const t = messages().actions;
  const inspected = await ui.run(() => LibraryService.InspectSkillScopes(filter), { reload: false });
  if (inspected === undefined) return;
  const single = !!filter.skill_id && !!filter.scope;
  const { targets, modified, missing } = reinstallTargets(inspected ?? [], single);
  if (!targets.length) {
    ui.toast(t.allCurrent(what));
    return;
  }
  let chosen: ReinstallChoices = { overwrite: new Set(), missing: new Map() };
  if (modified.length || missing.length) {
    const picked = await ui.open<ReinstallChoices>((close) => <ReinstallDialog modified={modified} missing={missing} close={close} />);
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
    const rows: ResultRow[] = [];
    let uninstalled = 0;
    for (const p of uninstalls) {
      try {
        await LibraryService.Uninstall(p.skill_id, p.scope);
        uninstalled++;
      } catch (e) {
        const err = toLibraryError(e);
        if (err.code === ErrorCode.Canceled) throw e;
        rows.push({ label: `${skillName(s, p.skill_id)} · ${scopeLabel(s, p.scope)}`, outcome: t.notUninstalled, tone: "bad", reason: describeError(err) });
      }
    }
    return { results, rows, uninstalled };
  }, { failed: (d) => d.rows.length > 0 || d.results.some((r) => r.outcome === ReinstallOutcome.ReinstallFailed) });
  if (!done) return;
  const failed = mergeProjectPlaces(s, done.results.filter((r) => r.outcome === ReinstallOutcome.ReinstallFailed));
  if (failed.length || done.rows.length) {
    await showResults(t.reinstallPartly, [...failed.map((r) => failedRow(s, r)), ...done.rows], t.kept(keptModified, keptMissing) || undefined);
    return;
  }
  const placed = mergeProjectPlaces(s, done.results.filter((r) => r.outcome === ReinstallOutcome.Reinstalled)).length;
  ui.toast(t.reinstalled(placed, done.uninstalled, what, keptModified, keptMissing));
}

// ReinstallDialog 在重新安装前问用户两类地方：文件夹不在了的(不动、装回去还是卸载，默认不动)与被改过的(勾选的才覆盖，默认都不覆盖)。
function ReinstallDialog({
  modified,
  missing,
  close,
}: {
  modified: SkillScopeInspection[];
  missing: SkillScopeInspection[];
  close: (value?: ReinstallChoices) => void;
}) {
  const s = useSnapshot();
  const t = useT();
  const [overwrite, setOverwrite] = useState<Set<string>>(new Set());
  const [choices, setChoices] = useState<Map<string, MissingChoice>>(new Map());
  const folders = [...new Map(missing.map((p) => [folderKey(p), missing.filter((x) => folderKey(x) === folderKey(p))])).entries()];
  const choose = (key: string, value: MissingChoice) => setChoices((prev) => new Map(prev).set(key, value));
  const toggle = (key: string) => setOverwrite((prev) => toggled(prev, key));
  return (
    <Modal
      title={missing.length ? t.actions.reinstallConfirmTitle : t.actions.modifiedTitle}
      onClose={() => close()}
      footer={
        <>
          {folders.length > 0 && (
            <Button className="mr-auto" onClick={() => setChoices(new Map(folders.map(([key]) => [key, "putBack"])))}>
              {t.actions.putBackAll}
            </Button>
          )}
          <Button onClick={() => close()}>{t.common.cancel}</Button>
          <Button variant={overwrite.size ? "danger" : "primary"} onClick={() => close({ overwrite, missing: choices })} autoFocus>
            {missing.length ? t.common.ok : overwrite.size ? t.actions.reinstallOverwrite(overwrite.size) : t.actions.reinstall}
          </Button>
        </>
      }
    >
      {folders.length > 0 && (
        <>
          <Notice tone="warn">{t.actions.missingNotice(folders.length)}</Notice>
          <ListBox>
            {folders.map(([key, places]) => {
              const p = places[0];
              const groups = [...new Set(places.flatMap((x) => x.groups ?? []))];
              const shared = sharedWithNote(p);
              return (
                <ListRow key={key} compact>
                  <div className="min-w-0 grow">
                    <div>
                      {t.actions.missingPlace(skillName(s, p.skill_id), mergeProjectPlaces(s, places).map((x) => scopeLabel(s, x.scope)))}
                    </div>
                    <div className="truncate font-mono text-[11px] text-muted-foreground">{shortPath(s, p.folder)}</div>
                    {shared.length > 0 && (
                      <div className="text-[11px] text-muted-foreground">{t.actions.missingShared(shared.map((code) => agentName(s, code)))}</div>
                    )}
                    {groups.length > 0 && (
                      <div className="text-[11px] text-muted-foreground">{t.actions.missingGrouped(groups.map((g) => groupName(s, g)))}</div>
                    )}
                  </div>
                  <Segmented
                    className="shrink-0"
                    value={choices.get(key) ?? "keep"}
                    onChange={(value) => choose(key, value)}
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
          <Notice tone="warn">{t.actions.modifiedNotice}</Notice>
          <ListBox>
            {mergeProjectPlaces(s, modified).map((p) => {
              const key = overwriteKey(p);
              return (
                <ListRow key={key} asChild compact clickable selected={overwrite.has(key)}>
                  <label>
                    <input type="checkbox" className="shrink-0 accent-primary" checked={overwrite.has(key)} onChange={() => toggle(key)} />
                    <div className="min-w-0 grow">
                      <div>
                        {skillName(s, p.skill_id)} · {scopeLabel(s, p.scope)}
                      </div>
                      <div className="truncate font-mono text-[11px] text-muted-foreground">{shortPath(s, p.folder)}</div>
                      {p.link_target && <div className="text-[11px] break-all text-muted-foreground">{linkNote(s, p.link_target)}</div>}
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
