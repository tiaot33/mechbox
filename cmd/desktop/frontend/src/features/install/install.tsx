// 安装与卸载：作用域页的开关、详情抽屉、整组开关与拖放(技能、组、集合)都走这里，冲突处理只写一份。
// 规则来自技能库的契约(go doc ./internal/library):
// - 安装前先看安装计划：不是从库里装的同名文件夹可选覆盖;被另一个技能占用或作用域根目录不存在时只能说明原因。
// - 组安装逐条返回结果，被跳过的成员列出原因。
// - 卸载会删掉别处链接指着的文件夹时，库先给那几处换上独立的一份，确认前说出来。
// - 卸载会删掉被手动改过的文件夹时，确认前列出是哪几处，修改会跟着丢掉;按界面上已经读到的状态写，不先现查磁盘，
//   确认之后才被改过的、才出现的旧链接由技能库删之前核对出来，再问一次(runDeleting)。
// - 提到关系：装好后，装上的技能提到的技能在那里找不到时，提示里写出来，库里有的能一起装上(直接安装，组安装时也不加进组);
//   卸载、卸载整组会删掉文件夹、让别的技能因此找不到它时，确认里写出是哪几个(平时不弹确认的卸载这时也弹)。
//   都只用规则和已存下的 AI 判断结论，不等 AI;读不出提到关系时照常安装、卸载，不拦着。
import { LibraryService } from "@bindings/cmd/desktop";
import {
  ErrorCode,
  InstallPlanStatus,
  type CollectionID,
  type GroupID,
  type MissingMention,
  type SkillInScope,
  type Scope,
  type SkillID,
} from "@bindings/internal/library";
import { deleteConsent } from "@/lib/consent";
import { messages, useT } from "@/lib/i18n";
import { currentSnapshot, toLibraryError, useSnapshot, type Snapshot } from "@/lib/library";
import { mergeMissing, missingAt } from "@/lib/mentions";
import { agentName, groupName, placeOf, projectName, scopeKey, skillName } from "@/lib/scope";
import { agentNameIn, placeCount, scopeLabel } from "@/lib/places";
import { collectionSkills, sourceLabel } from "@/lib/source";
import { folderPlaces, groupInstallsIn } from "@/lib/installs";
import { shortPath } from "@/lib/paths";
import { ui } from "@/lib/ui";
import { openDrawer } from "@/lib/view";
import { Hint } from "@/components/ui/field";
import { Notice } from "@/components/ui/notice";
import { DeleteNotes, SharedFolders } from "@/components/app/link-notes";
import { runDeleting } from "@/components/app/discard-edits";
import { runEach, showResults } from "@/components/app/results";
import { memberRows, summarizeMembers } from "@/components/app/result-rows";
import {
  asksBeforeUninstall,
  groupInstalled,
  groupInstallPlan,
  installResults,
  installSteps,
  mentionedMissing,
  notDirectIn,
  occupied,
  readInstallPlans,
  readsTwiceNotes,
  skippedPicked,
  uninstallGroupPlaces,
  uninstallPicked,
  uninstallPlace,
  type InstallItem,
  type PlanRead,
} from "./install-rules";

export async function installSkill(id: SkillID, scope: Scope) {
  const s = currentSnapshot();
  const t = messages().actions;
  const plan = await ui.read(() => LibraryService.PlanInstall(id, scope));
  if (!plan) return;
  const where = scopeLabel(s, scope);
  const twice = readsTwiceNotes(s, [{ plan }]);
  let overwrite = false;
  switch (plan.status) {
    case InstallPlanStatus.InstallOccupied: {
      // 占着的是同名的另一个技能时，toast 上能点开它。
      const { reason, occupier } = occupied(s, id, plan);
      ui.toast(reason, "error", occupier ? { label: t.openOccupier, onClick: () => openDrawer(occupier) } : undefined);
      return;
    }
    case InstallPlanStatus.InstallUntracked: {
      const ok = await ui.confirm({
        title: t.untrackedTitle,
        body: (
          <>
            <Notice alert>{t.untrackedBody(shortPath(s, plan.folder), plan.name)}</Notice>
            <ReadsTwiceNotice notes={twice} />
          </>
        ),
        confirm: t.overwriteInstall,
        danger: true,
      });
      if (!ok) return;
      overwrite = true;
      break;
    }
    default:
      if (twice.length && !(await ui.confirm({ title: t.installTitle(plan.name, where), body: <ReadsTwiceNotice notes={twice} />, confirm: t.install }))) return;
  }
  const done = await ui.run(() => LibraryService.Install(id, scope, { overwrite }).then(() => true));
  if (done) await toastInstalled([{ skill_id: id, scope }], t.installed(plan.name, where));
}

// toastInstalled 是装好之后的提示：装上的这几处提到的技能在那里找不到时写出名字，库里有、那里还没有安装记录的给一个按钮一起装上
// (有记录、文件夹不在了的要重新安装，不在这里装);都找得到时只提示 done。done 为空(已经列出了结果)时只在有找不到的时候提示。
// 「照着另一个 Agent 装」、加进已安装的组也用它。
export async function toastInstalled(placed: SkillInScope[], done?: string) {
  const t = messages().actions;
  let missing: MissingMention[] = [];
  if (placed.length) {
    try {
      missing = missingAt(currentSnapshot(), (await LibraryService.ListMissingMentions()) ?? [], placed);
    } catch {
      // 读不出提到关系时只说装好了。
    }
  }
  if (!missing.length) {
    if (done) ui.toast(done);
    return;
  }
  const { skills, names, targets, count } = mentionedMissing(currentSnapshot(), missing);
  ui.toast(
    done ? t.installedMissing(done, skills, names) : t.missingThere(skills, names),
    "ok",
    targets.length ? { label: t.installMentioned(count), onClick: () => void installMentioned(targets) } : undefined,
  );
}

// installMentioned 把提到的、库里有的技能装到提到它的技能所在的作用域：逐处直接安装，和"安装到…"一样处理同名文件夹。
async function installMentioned(targets: SkillInScope[]) {
  const s = currentSnapshot();
  const t = messages().actions;
  const items = targets.flatMap((p) => {
    const skill = s.skills.find((x) => x.id === p.skill_id);
    return skill ? [{ skill, scope: p.scope }] : [];
  });
  const n = new Set(items.map((x) => x.skill.id)).size;
  await installEach(items, t.installMentionedTitle(n), t.installedMentioned(n));
}

// lostMentions 是卸掉这几处的文件夹之后才会找不到被提到技能的地方(技能库一次算出);读不出来时当作没有，不拦着卸载。
// 目录列表相同、合成一处的几个 Agent 的同一条只留一条。
export async function lostMentions(gone: SkillInScope[]): Promise<MissingMention[]> {
  if (!gone.length) return [];
  try {
    const lost = await LibraryService.ListLostMentions(gone.map((p) => ({ skill_id: p.skill_id, scope: p.scope })));
    return mergeMissing(currentSnapshot(), lost ?? []);
  } catch {
    return [];
  }
}

// lostMentionLines 是 lost 逐条的说法：哪个技能、在哪、找不到谁。
export function lostMentionLines(s: Snapshot, lost: MissingMention[]): string[] {
  const t = messages().actions;
  return lost.map((m) => t.lostMention(skillName(s, m.skill_id), scopeLabel(s, m.scope), m.name));
}

// LostMentions 是卸载确认里的一段：哪些装着的技能会因此找不到它们提到的技能。prefix 是限定语(如移出组时「选一并卸载时」)。
export function LostMentions({ lost, prefix }: { lost: MissingMention[]; prefix?: string }) {
  const s = useSnapshot();
  const t = useT().actions;
  if (!lost.length) return null;
  return (
    <Notice alert className="lost-mentions">
      <span className="font-semibold text-foreground">
        {prefix}
        {t.lostMentionsLead}
      </span>
      <ul className="mt-1">
        {lostMentionLines(s, lost).map((line) => (
          <li key={line}>{line}</li>
        ))}
      </ul>
    </Notice>
  );
}

// installCollection 是把集合拖到作用域：集合里还没直接安装在这里的技能逐个直接安装。一次性的，不像组安装那样记下来，
// 之后入库的新技能不会自动装过去。技能库没有整个集合的安装，先逐个看安装计划：被别的技能占用的跳过并说明原因;
// 有不是库装的同名文件夹的合成一次询问(跳过或覆盖),不逐个弹。有没装上的才列出结果，否则只提示装了几个。
export async function installCollection(id: CollectionID, scope: Scope) {
  const s = currentSnapshot();
  const t = messages().actions;
  const collection = s.collections.find((c) => c.id === id);
  if (!collection) return;
  const label = sourceLabel(s, collection.source);
  const where = scopeLabel(s, scope);
  const items = notDirectIn(s, collectionSkills(s, collection), [scope]);
  await installEach(items, t.collectionInstallTitle(label, where), t.collectionInstalled(label, where, items.length));
}

// installSkillTo 是详情里的"安装到…":把一个技能装到勾上的几个 Agent(同一个地方)。只勾一个时和开关一样装这一处;
// 勾几个时逐处装，同名文件夹合成一次询问，全部装上时提示装到了哪几个 Agent。
export async function installSkillTo(id: SkillID, scopes: Scope[]) {
  const s = currentSnapshot();
  const t = messages().actions;
  const skill = s.skills.find((x) => x.id === id);
  if (!skill || !scopes.length) return;
  if (scopes.length === 1) return installSkill(id, scopes[0]);
  const where = scopesLabel(s, scopes);
  await installEach(
    scopes.map((scope) => ({ skill, scope })),
    t.skillInstallTitle(skill.name, where),
    t.installed(skill.name, where),
  );
}

// installReplacing 是移除同名的另一个技能后，在它原来装着的几处装上留下的这个(见 removeTwin):逐处直接安装，和"安装到…"一样处理。
export async function installReplacing(id: SkillID, scopes: Scope[]) {
  const s = currentSnapshot();
  const t = messages().actions;
  const skill = s.skills.find((x) => x.id === id);
  if (!skill || !scopes.length) return false;
  return installEach(
    scopes.map((scope) => ({ skill, scope })),
    t.replaceInstallTitle(skill.name),
    t.replaceInstalled(skill.name),
  );
}

// installSkills 把勾选的技能装到 scopes(底部操作栏的"安装到…"可以勾选几个 Agent;拖勾选着的几行到作用域是一处):
// 和集合一样逐个直接安装，已经直接安装在那里的不再装;都已装在那里时只提示。scopes 在同一个地方(全局或同一个项目)。
// 返回是否至少装上了一处：取消了、都已装在那里、一处都没装上时是 false。
export async function installSkills(ids: SkillID[], scopes: Scope[]): Promise<boolean> {
  const s = currentSnapshot();
  const t = messages().actions;
  if (!scopes.length) return false;
  const where = scopesLabel(s, scopes);
  const items = notDirectIn(s, s.skills.filter((x) => ids.includes(x.id)), scopes);
  if (!items.length) {
    ui.toast(t.pickedAllThere(where));
    return false;
  }
  const n = new Set(items.map((x) => x.skill.id)).size;
  return installEach(items, t.pickedInstallTitle(n, where), t.pickedInstalled(n, where));
}

// scopesLabel 是同一个地方的几个 Agent 合起来的说法，如"Claude Code、Codex · 全局";只有一个时就是 scopeLabel。
function scopesLabel(s: Snapshot, scopes: Scope[]): string {
  if (scopes.length === 1) return scopeLabel(s, scopes[0]);
  const place = placeOf(scopes[0]);
  const names = messages().model.agentNames(scopes.map((sc) => agentNameIn(s, sc.agent, place)));
  return `${names} · ${place ? projectName(s, place) : messages().model.global}`;
}

// installEach 逐处直接安装(一处是一个技能装到一个 Agent):先逐处看安装计划，被别的技能占用的跳过并说明原因;
// 有不是库装的同名文件夹的合成一次询问(跳过或覆盖),不逐个弹。一处装不上(含看安装计划就出错)不影响其余的。
// 有没装上的才列出结果，否则只提示 done。装到几个 Agent 时，询问与结果里每一处写出是哪个 Agent。
// 返回是否至少装上了一处：取消了、一处都没装上时是 false。
async function installEach(items: InstallItem[], title: string, done: string): Promise<boolean> {
  const s = currentSnapshot();
  const t = messages().actions;
  if (!items.length) return false;
  // 看安装计划就出错的那几处算失败：顶栏画叉，和结果列表一致。
  const settled = await ui.run(() => Promise.allSettled(items.map((x) => LibraryService.PlanInstall(x.skill.id, x.scope))), {
    reload: false,
    failed: (results) => results.some((r) => r.status === "rejected"),
  });
  if (!settled) return false;
  const plans = readInstallPlans(
    s,
    items,
    settled.map((r): PlanRead => (r.status === "fulfilled" ? { plan: r.value } : { error: toLibraryError(r.reason) })),
  );
  const { several, label, untracked } = plans;
  const twice = readsTwiceNotes(
    s,
    plans.planned.filter((x) => x.plan.status !== InstallPlanStatus.InstallOccupied).map((x) => ({ plan: x.plan, label: several || items.length > 1 ? label(x) : undefined })),
  );
  let overwrite = false;
  if (untracked.length || twice.length) {
    const choice = await ui.confirm({
      title,
      body: (
        <>
          {untracked.length > 0 && (
            <Notice alert>
              {several
                ? t.untrackedPlaces(untracked.length, untracked.map(label))
                : t.untrackedSkills(untracked.length, untracked.map((x) => x.plan.name))}
            </Notice>
          )}
          <ReadsTwiceNotice notes={twice} />
        </>
      ),
      choices: untracked.length
        ? [
            { value: "skip", label: several ? t.skipPlaces : t.skipSkills, desc: several ? t.skipPlacesDesc : t.skipSkillsDesc },
            { value: "overwrite", label: t.overwriteFolders, desc: t.overwriteFoldersDesc },
          ]
        : undefined,
      confirm: t.install,
    });
    if (!choice) return false;
    overwrite = choice === "overwrite";
  }
  const steps = installSteps(s, plans, overwrite);
  const results = await runEach(
    steps,
    (step) => ("skip" in step ? undefined : LibraryService.Install(step.item.skill.id, step.item.scope, { overwrite: step.overwrite })),
    (results) => installResults(s, plans, steps, results).failed,
  );
  if (!results) return false;
  const { rows, placed, installed } = installResults(s, plans, steps, results);
  if (installed < rows.length) {
    const rest = rows.length - installed;
    await showResults(title, rows, several ? t.installedPlaces(installed, rest) : t.installedSummary(installed, rest));
    await toastInstalled(placed);
  } else {
    await toastInstalled(placed, done);
  }
  return installed > 0;
}

export async function uninstallSkill(id: SkillID, scope: Scope) {
  const s = currentSnapshot();
  const t = messages().actions;
  // 全局共用目录：从其中一个卸载会撤销共用的各个 Agent 的直接安装，文件夹随之删除，先说明还有哪些 Agent 在用。
  // 目录列表相同、合成一处的几个 Agent 在界面上本来就是一处、名字里写着它们，不再另说。
  // 别处的符号链接指着这个文件夹时，删之前库先给那几处换上独立的一份，也先说明。
  // 文件夹被手动改过时，卸载会把修改一起删掉，先列出是哪几处。没有组安装留着时文件夹才删。
  const plan = uninstallPlace(s, id, scope);
  const { place, shared, deleted } = plan;
  const unfound = await lostMentions(deleted);
  if (place && asksBeforeUninstall(plan, unfound)) {
    const ok = await ui.confirm({
      title: t.uninstallTitle(skillName(s, id)),
      body: (
        <>
          {shared.length > 0 && (
            <Notice alert>
              {t.uninstallShared(
                shortPath(s, place.folder),
                shared.map((code) => agentName(s, code)),
                !!place.groups?.length,
              )}
            </Notice>
          )}
          <DeleteNotes deleted={deleted} />
          <LostMentions lost={unfound} />
        </>
      ),
      confirm: t.uninstall,
      danger: true,
    });
    if (!ok) return;
  }
  await runDeleting(deleteConsent(deleted), (consent) => LibraryService.Uninstall(id, scope, consent), {
    success: t.uninstalled(skillName(s, id), scopeLabel(s, scope)),
    onError: (err) => {
      if (err.code !== ErrorCode.FailedPrecondition) return false;
      ui.toast(t.onlyViaGroup, "error");
      return true;
    },
  });
}


// uninstallSkills 从 scopes 卸载勾选的技能(底部操作栏的"从这里卸载",项目页是项目里的各个 Agent):撤销它们的直接安装。
// 一批卸载先确认一次：会删掉几个文件夹、其中被手动改过的是哪几处，全局共用的文件夹还有哪些 Agent 在用，别处链接指着的先换上独立的一份。
// 只随组安装的跳过并说明;有跳过或失败的才列出结果。返回是否至少卸载了一处：没有可卸载的、取消了、都失败了时是 false。
export async function uninstallSkills(ids: SkillID[], scopes: Scope[], where: string): Promise<boolean> {
  const s = currentSnapshot();
  const t = messages().actions;
  const picked = uninstallPicked(s, ids, scopes);
  const { direct, viaGroup, deleted, shared } = picked;
  if (!direct.length) {
    ui.toast(t.pickedNothingDirect(where), "error");
    return false;
  }
  const title = t.uninstallPickedTitle(picked.skills, where);
  const unfound = await lostMentions(deleted);
  const ok = await ui.confirm({
    title,
    body: (
      <>
        <Hint>{t.uninstallPickedHint(picked.folders, viaGroup.length)}</Hint>
        {shared.length > 0 && (
          <>
            <Notice alert>{t.uninstallPickedShared}</Notice>
            <SharedFolders places={shared} />
          </>
        )}
        <DeleteNotes deleted={deleted} />
        <LostMentions lost={unfound} />
      </>
    ),
    confirm: t.uninstall,
    danger: true,
  });
  if (!ok) return false;
  const places = direct.map((p): SkillInScope => ({ skill_id: p.skill_id, scope: p.scope }));
  const done = await runDeleting(deleteConsent(deleted), (consent) =>
    LibraryService.UninstallSkills(places, consent).then(() => true),
  );
  if (!done) return false;
  const rows = skippedPicked(s, picked);
  if (rows.length) await showResults(title, rows, t.uninstallPickedSummary(direct.length, rows.length));
  else ui.toast(t.uninstalledPicked(direct.length, where));
  return true;
}

export async function installGroup(groupID: GroupID, scope: Scope) {
  const s = currentSnapshot();
  const t = messages().actions;
  const plan = await ui.read(() => LibraryService.PlanGroupInstall(groupID, scope));
  if (!plan) return;
  const { untracked, blocked } = groupInstallPlan(plan.members ?? []);
  const twice = readsTwiceNotes(
    s,
    (plan.members ?? []).filter((m) => m.status === InstallPlanStatus.InstallReady || m.status === InstallPlanStatus.InstallUntracked).map((m) => ({ plan: m })),
  );
  let overwrite = false;
  if (untracked.length || twice.length) {
    const choice = await ui.confirm({
      title: t.groupInstallTitle(groupName(s, groupID), scopeLabel(s, scope)),
      body: (
        <>
          {untracked.length > 0 && (
            <Notice alert>
              {t.groupUntracked(
                untracked.length,
                untracked.map((m) => m.name),
              )}
            </Notice>
          )}
          <ReadsTwiceNotice notes={twice} />
        </>
      ),
      choices: untracked.length
        ? [
            { value: "skip", label: t.skipMembers, desc: t.skipMembersDesc },
            { value: "overwrite", label: t.overwriteFolders, desc: t.overwriteFoldersDesc },
          ]
        : undefined,
      confirm: t.install,
    });
    if (!choice) return;
    overwrite = choice === "overwrite";
  }
  const results = await ui.run(() => LibraryService.InstallGroup(groupID, scope, { overwrite }));
  if (!results) return;
  const { placed, listed } = groupInstalled(results, blocked);
  if (listed) {
    await showResults(t.groupInstallResults(groupName(s, groupID)), memberRows(s, results), summarizeMembers(results));
    await toastInstalled(placed);
  } else {
    await toastInstalled(placed, t.groupInstalled(groupName(s, groupID), scopeLabel(s, scope), summarizeMembers(results)));
  }
}

// 卸载整组的组安装总是先确认，写出会从这里卸载几个技能(组的节标题上的 ×、作用域页摘要里的组开关都是一点就卸载整组，容易误点)。
export async function uninstallGroup(groupID: GroupID, scope: Scope) {
  const s = currentSnapshot();
  const t = messages().actions;
  // 合成一处里几个 Agent 各有这个组的组安装时一起撤掉，影响一次算完、只问一次。
  const found = groupInstallsIn(s, { id: groupID }, scopeKey(scope));
  const scopes = found.length ? found : [scope];
  // 共用目录：只靠这个组安装留着的成员，卸载后文件夹删除，共用的几个 Agent 都不再算已装，先说明还有哪些 Agent 在用。
  // 别处的符号链接指着要删的文件夹时，删之前库先给那几处换上独立的一份，也先说明。
  const { gone, shared: removed } = uninstallGroupPlaces(s, groupID, scope, scopes);
  // 要删的文件夹被别处的符号链接指着、被手动改过时，确认里一并列出(DeleteNotes)。
  const deleted = folderPlaces(s, gone);
  const unfound = await lostMentions(deleted);
  const ok = await ui.confirm({
    title: t.groupUninstallTitle(groupName(s, groupID), scopeLabel(s, scope)),
    body: (
      <>
        <p>{t.groupUninstallBody(placeCount(s, gone), scopeLabel(s, scope))}</p>
        {removed.length > 0 && (
          <>
            <Notice alert>{t.groupUninstallShared(removed.length)}</Notice>
            <SharedFolders places={removed} />
          </>
        )}
        <DeleteNotes deleted={deleted} />
        <LostMentions lost={unfound} />
      </>
    ),
    confirm: t.uninstall,
    danger: true,
  });
  if (!ok) return;
  // 删之前核对出被改过的再问一次时，已经撤掉的那几条不再重撤。
  const done = new Set<string>();
  await runDeleting(
    deleteConsent(deleted),
    async (consent) => {
      for (const sc of scopes.filter((x) => !done.has(scopeKey(x)))) {
        await LibraryService.UninstallGroup(groupID, sc, consent);
        done.add(scopeKey(sc));
      }
    },
    { success: t.groupUninstalled(groupName(s, groupID), scopeLabel(s, scope)) },
  );
}


// ReadsTwiceNotice 是安装前「这几个 Agent 也读这个目录，会读到两份」的提示(见 readsTwiceNotes),一处一行;没有时不显示。
export function ReadsTwiceNotice({ notes }: { notes: string[] }) {
  if (!notes.length) return null;
  return (
    <Notice className="reads-twice">
      {notes.map((note) => (
        <span key={note} className="block">
          {note}
        </span>
      ))}
    </Notice>
  );
}
