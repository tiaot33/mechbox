// 不打开更新计划直接做的更新(issue #131):更新页的"更新"(按默认勾选直接执行)、"全部更新",本地目录来源的"重新读取"。
// 执行一次更新见 run.ts;更新计划、用目录里的修改更新与更新历史的弹窗各自在 update-dialog.tsx、scope-update-dialog.tsx、update-history.tsx 里打开;
// 完成后的汇报见 report.tsx。默认勾哪些技能、哪些地方见 lib/consent.ts。
import { LibraryService } from "@bindings/cmd/desktop";
import type { CollectionID, ReinstallResult, Skill } from "@bindings/internal/library";
import { defaultInstallOn, defaultSkills, refreshRequests } from "@/lib/consent";
import { placeKey } from "@/lib/installs";
import { messages } from "@/lib/i18n";
import { currentSnapshot } from "@/lib/library";
import { collectionName } from "@/lib/source";
import { ui } from "@/lib/ui";
import { confirmLinkWrites } from "@/components/app/link-notes";
import { openUpdate } from "./update-dialog";
import { reportUpdates } from "./report";
import { placeAgain, runUpdate } from "./run";
import { currentRecord, directoryReread, movedSince, refreshOutcome, rereadChange, splitByRecord, type UpdateOutcome } from "./update-rules";

// updateWithDefaults 不打开更新计划，按默认勾选更新这些集合，最后一起汇报。改名、删除和会换掉用户修改的更新要在更新计划里由用户回答，这里不动。
// 读来源、更新、换装出去的地方算一件事(ui.flow):一个都没更新(都没有能直接更新的，或在确认里取消了)时不画对勾，
// 有的集合没更新成时写没有全部完成。
export async function updateWithDefaults(collectionIDs: CollectionID[]) {
  const s = currentSnapshot();
  const t = messages().update;
  const names = collectionIDs.map((id) => collectionName(s, id));
  const r = await ui.flow(
    t.updateLabel(names),
    async () => {
      const outcomes: UpdateOutcome[] = [];
      let missed = 0;
      for (const [i, id] of collectionIDs.entries()) {
        const name = names[i];
        const plan = await ui.run(() => LibraryService.PlanUpdate(id));
        if (!plan) {
          missed++;
          continue;
        }
        const checked = defaultSkills(plan);
        if (!checked.size) {
          ui.toast(t.nothingDirect(name));
          continue;
        }
        const o = await runUpdate(plan, name, checked, defaultInstallOn, undefined, true);
        if (o === "conflict") ui.toast(t.sourceMoved(name), "error");
        if (o && o !== "conflict") outcomes.push(o);
        else missed++;
      }
      return outcomes.length ? { outcomes, missed } : undefined;
    },
    { failed: (r) => r.missed > 0 },
  );
  if (r) await reportUpdates(r.outcomes);
}

// rereadSkill 是本地目录来源的"重新读取":读出目录当前内容成为新版本，并换到装过它的地方(用户改过的、文件夹不在了的地方不动)。
// 底层就是只勾这一个技能的更新，所以照样有版本历史和回滚。读目录到换完算一件事(ui.flow),没读成时不画对勾。
export async function rereadSkill(skill: Skill) {
  const o = await ui.flow(messages().update.rereadLabel(skill.name), () => reread(skill));
  if (o) await reportUpdates([o]);
}

async function reread(skill: Skill): Promise<UpdateOutcome | undefined> {
  const plan = await ui.run(() => LibraryService.PlanUpdate(skill.collection_id));
  if (!plan) return undefined;
  const change = rereadChange(plan, skill.id);
  if (change === "gone") {
    ui.toast(messages().update.goneFromDirectory(skill.name), "error");
    return undefined;
  }
  if (change === "unchanged") return refreshInstalls(skill);
  const o = await runUpdate(plan, skill.name, new Set([skill.id]), defaultInstallOn, undefined, true);
  if (o === "conflict") ui.toast(messages().update.directoryMoved, "error");
  return o === "conflict" ? undefined : o;
}

// rereadDirectory 是本地目录来源整个目录的"重新读取":目录里改过的技能都读出当前内容成为新版本，并换到装过它们的地方
// (用户改过的、文件夹不在了的地方不动);和逐个技能的重新读取一样是一次更新，有版本历史、可以回滚。
// 默认读进来的照更新计划的默认勾选：回滚掉的(跳过)、库里有用户修改的不读。还有要用户拿主意的(目录里删掉或改了名、
// 会换掉库里用户修改的)时打开更新计划，由用户在那里勾选、确认改名;目录和库里完全一样时只提示一句。读目录到换完算一件事(ui.flow),
// 没读进来或转去更新计划时不画对勾。
export async function rereadDirectory(collectionID: CollectionID) {
  const label = collectionName(currentSnapshot(), collectionID);
  let decide = false;
  const o = await ui.flow(messages().update.rereadLabel(label), async () => {
    const r = await readDirectory(collectionID, label);
    if (r !== "decide") return r;
    decide = true;
    return undefined;
  });
  if (decide) return openUpdate(collectionID);
  if (o) await reportUpdates([o]);
}

async function readDirectory(collectionID: CollectionID, label: string): Promise<UpdateOutcome | "decide" | undefined> {
  const t = messages().update;
  const plan = await ui.run(() => LibraryService.PlanUpdate(collectionID));
  if (!plan) return undefined;
  const next = directoryReread(plan);
  if (next.kind === "decide") return "decide";
  if (next.kind === "nothing") {
    ui.toast(next.changed ? t.directoryNothingDefault(label) : t.directoryUnchanged(label));
    return undefined;
  }
  const o = await runUpdate(plan, label, next.checked, defaultInstallOn, undefined, true);
  if (o === "conflict") ui.toast(t.directoryMoved, "error");
  return o === "conflict" ? undefined : o;
}

// refreshInstalls 是目录内容没变时的"重新读取":库里已是目录现在的内容，但装出去的地方可能还没换上(上次没换上、
// 落后于库里的版本),把这些换成当前版本;用户改过的、文件夹不在了的地方不动，项目不在这台机器上的装不上。都已是当前版本时只提示一句。
// 带来当前版本的那次更新里列过的地方，补装算进那次更新，回滚它时一起装回去;其余的是普通的重新安装。
async function refreshInstalls(skill: Skill): Promise<UpdateOutcome | undefined> {
  const s = currentSnapshot();
  const inspected = await ui.read(() => LibraryService.InspectSkillScopes({ skill_id: skill.id }));
  if (inspected === undefined) return undefined;
  const kept = refreshRequests(inspected ?? []);
  const { requests, keptEdited, keptMissing } = kept;
  if (!requests.length) {
    ui.toast(messages().update.unchanged(skill.name, keptEdited, keptMissing));
    return undefined;
  }
  const wanted = new Set(requests.map(placeKey));
  if (!(await confirmLinkWrites(inspected ?? [], (p) => wanted.has(placeKey(p)), messages().update.rereadTitle(skill.name), messages().update.reinstall))) return undefined;
  const history = await ui.read(() => LibraryService.ListUpdateHistory({ skill_id: skill.id }));
  if (history === undefined) return undefined;
  const record = currentRecord(history ?? [], skill);
  const { viaRecord, plain } = splitByRecord(requests, record);
  const results: ReinstallResult[] = [];
  if (viaRecord.length) {
    const r = await placeAgain(record!.id, viaRecord);
    if (!r) return undefined;
    // 那次更新之后技能又换过版本(更新后又回滚)的地方，那次更新已管不到它们，按普通重新安装换上。
    const since = movedSince(viaRecord, r);
    results.push(...since.results);
    plain.push(...since.moved);
  }
  if (plain.length) {
    const r = await placeAgain(0, plain);
    if (!r) return undefined;
    results.push(...r);
  }
  return refreshOutcome(s, skill, record, kept, results, plain);
}
