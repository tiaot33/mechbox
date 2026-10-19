// 英文界面文案：结构与 zh.tsx 一一对应，satisfies Messages 让漏翻、参数不对的条目在 tsc 里报出。
import type { ReactNode } from "react";
import { System } from "@wailsio/runtime";
import type { Messages } from ".";
import type { InstalledWhere, RevisionSummary, SameNameKind } from "@/lib/model";

const and = (items: string[]) => new Intl.ListFormat("en", { type: "conjunction" }).format(items);
// count 是"1 skill"、"3 skills";不规则的复数给出 many。
const count = (n: number, one: string, many = `${one}s`) => `${n.toLocaleString("en")} ${n === 1 ? one : many}`;
// keptAsIs 是重新安装时没动的地方：用户改过的、文件夹不在了的。
const keptAsIs = (edited: number, missing: number) =>
  [edited ? `${count(edited, "edited place")} left as is` : "", missing ? `${count(missing, "missing folder")} left alone` : ""].filter(Boolean).join("; ");
// keptPlaces 是更新时没动的地方，接在更新结果的句子后面。
const keptPlaces = (edited: number, missing: number) => [
  edited ? `${count(edited, "place")} you edited left alone` : "",
  missing ? `${count(missing, "missing folder")} left alone` : "",
];
const kinds = (list: SameNameKind[]) => and(list.map((k) => (k === "managed" ? "installed by the Library" : "unmanaged")));
// projectsHave 是"Project “a” also has" / "Projects “a” and “b” also have"。
const projectsHave = (projects: string[]) => `${projects.length === 1 ? "Project" : "Projects"} ${quoted(projects)} also ${projects.length === 1 ? "has" : "have"}`;
const quoted = (names: string[]) => and(names.map((n) => `“${n}”`));

// store 是令牌与密钥存在哪里，按平台换说法;平台信息在页面加载后才有，每次用到时现取。
const store = () => (System.IsWindows() ? "Windows Credential Manager" : System.IsLinux() ? "the system keyring" : "the system keychain");
// manualDelete 是卸载时跳过后去哪里手动删除这些条目。
const manualDelete = () =>
  System.IsWindows()
    ? " and later delete them by hand in Credential Manager under Windows Credentials (the entries starting with com.tiaot33.mechbox)"
    : System.IsLinux()
      ? "; if you entered them in Settings before, later find com.tiaot33.mechbox in Passwords and Keys or KWalletManager and delete it"
      : " and later delete them by hand by searching for com.tiaot33.mechbox in Keychain Access";
// deniedHint 是删不掉时猜的原因;Windows 上没有授权这一步，不猜。
const deniedHint = () =>
  System.IsWindows()
    ? ""
    : System.IsLinux()
      ? " (the keyring may not have been unlocked, or GNOME Keyring or KWallet isn't running)"
      : " (access may have been denied)";
// readFailedHelp 是读不到时怎么办;只有 Linux 上可能是没装或没启动密钥环。
const readFailedHelp = () =>
  System.IsLinux()
    ? " If GNOME Keyring or KWallet isn't installed or running, install and start it, then retry; or start Mechbox from a terminal with GITHUB_TOKEN, SKILLS_S3_*, AWS_* and similar environment variables set."
    : "";

const where = (w: InstalledWhere) =>
  w.total ? `installed in ${w.total > 2 ? `${w.places.join(", ")} and ${w.total - 2} more` : and(w.places)}` : "not installed anywhere";

export const en = {
  common: {
    working: "Working…",
    sameContent: "File contents are identical",
    binary: "Binary file; content not shown.",
    cancel: "Cancel",
    ok: "OK",
    close: "Close",
    unknown: "Unknown",
  },

  errors: {
    invalidArgument: "Invalid input",
    notFound: "Not found",
    alreadyExists: "Already exists",
    conflict: "Something changed in the meantime. Please try again",
    failedPrecondition: "This can't be done in the current state",
    canceled: "Canceled",
    sourceUnreachable: "The Source can't be reached right now. You can retry later",
    sourceInvalid: "The Source's content can't be used. Try another Source",
    busy: "Another process is writing to the Library. Please retry shortly",
    permission: "Permission denied or credentials rejected",
    notConfigured: "Required configuration is missing (see Settings)",
    internal: "Internal error",
    failed: "Operation failed",
    sourceClear: "You can also clear the Source link and fetch it as a skill with no Source.",
    sourceExists: (name: string) =>
      `The Library already has “${name}” from this Source, and the content here matches none of its Versions, so they aren't merged. You can clear the Source link and fetch it as a skill with no Source, or not fetch it for now.`,
    sourceUnreachableRetry: (clear: string) => `Can't reach this Source. You can retry later or change the link. ${clear}`,
    sourceMissing: (name: string, clear: string, message: string) =>
      `“${name}” isn't in the Source. You can change the link. ${clear}\n${message}`,
    sourceUnrecognized: (clear: string, message: string) => `This Source link isn't recognized. You can edit it. ${clear}\n${message}`,
  },

  paths: {
    absolute: "Enter an absolute path (starting with / or ~/), or click “Choose…”",
    fetchRelative: "For a local directory, enter an absolute path (starting with / or ~/), or click “Choose…”",
  },

  model: {
    global: "Global",
    outsideLinkNote: (target: string) =>
      `It's a symlink to ${target}. Updating it in the app later replaces the link with a separate copy; changes made in ${target} will no longer affect it here.`,
    linkNote: (target: string) => `Will become a separate copy this time (currently a symlink to ${target})`,
    sameNameGlobalWins: (name: string, k: SameNameKind[], who: string) =>
      `Global also has ${name} (${kinds(k)}). ${who} uses the global one, so this one won't take effect`,
    sameNameGlobalBoth: (name: string, k: SameNameKind[], who: string) => `Global also has ${name} (${kinds(k)}). Both will show up in ${who}`,
    sameNameGlobalUnknown: (name: string, k: SameNameKind[]) => `Global also has a skill named ${name} (${kinds(k)})`,
    sameNameGlobalFolder: (folder: string) => `Global folder: ${folder}`,
    sameNameProjectWins: (projects: string[], name: string, who: string) =>
      `${projectsHave(projects)} ${name}. There, ${who} uses this global one`,
    sameNameProjectBoth: (projects: string[], name: string, who: string) =>
      `${projectsHave(projects)} ${name}. There, both will show up in ${who}`,
    sameNameProjectUnknown: (projects: string[], name: string) => `${projectsHave(projects)} a skill named ${name}`,
    sameNameProjectFolder: (project: string, folder: string) => `Folder in “${project}”: ${folder}`,
    sameNameFetchSourced: (name: string) =>
      `With a Source, it's compared only with the same-named skill from that Source: if the content matches one of its Versions, it's just recorded as installed here; if not, it can't be fetched (you can clear the Source). Same-named skills from other Sources or with no Source aren't merged—the Library will have another “${name}”, and each card shows where it's installed.`,
    sameNameFetchUnsourced: (name: string) =>
      `If the content matches a Version of one of them, it's just recorded as installed here; otherwise they aren't merged—the Library will have another “${name}”, and each card shows where it's installed.`,
    statusCurrent: "Current",
    statusStale: "Old Version",
    statusModified: "Edited by hand",
    statusMissing: "Folder missing",
    statusRootMissing: "Project not on this machine",
    revisionUnknown: "Revision unknown",
    revisionSame: (revision: string) => `At ${revision}`,
    revisionMixed: (r: Extract<RevisionSummary, { kind: "mixed" }>) =>
      [
        ...r.others.map((o) => `${and(o.names)} at ${o.revision}`),
        ...(r.unknown.length ? [`Revision of ${and(r.unknown)} unknown`] : []),
      ].join("; ") + `; the other ${r.rest} at ${r.revision}`,
    kindLocal: "Local",
    noSource: "No Source",
    unknownSource: "Unknown Source",
    allSkills: "All skills",
    updates: "Updates",
  },

  actions: {
    gotIt: "OK",
    memberInstalled: "Installed",
    memberAlready: "Already installed",
    memberSkipped: "Skipped",
    membersSummary: (installed: number, already: number, skipped: number) =>
      [`${installed} newly installed`, `${already} already installed`, ...(skipped ? [`${skipped} skipped`] : [])].join(" · "),
    occupiedSelf: (folder: string) => `Can't install: another Scope of this skill is already installed at\n${folder}\nUninstall it first, then install`,
    occupiedSource: (source: string, folder: string) =>
      `Can't install: a skill with the same name from ${source} is already installed at\n${folder}\nUninstall it first, then install`,
    occupiedOther: (folder: string) => `Can't install: another skill with the same name is already installed at\n${folder}\nUninstall it first, then install`,
    untrackedTitle: "A folder with the same name is already there",
    untrackedBody: (folder: string, name: string) => `${folder} exists but wasn't installed by the Library. Overwriting replaces it with “${name}”.`,
    overwriteInstall: "Overwrite and install",
    installed: (name: string, where: string) => `Installed “${name}” in ${where}`,
    uninstallTitle: (name: string) => `Uninstall “${name}”`,
    uninstallShared: (folder: string, agents: string[], grouped: boolean) =>
      `${folder} is also shared by ${and(agents)}. ` +
      (grouped
        ? "Uninstalling removes the direct install for these Agents; a Group install still keeps it, so the folder stays."
        : "Uninstalling deletes this folder, and none of these Agents will count it as installed."),
    uninstall: "Uninstall",
    uninstalled: (name: string, scope: string) => `Removed the direct install of “${name}” in ${scope}`,
    onlyViaGroup: "This skill is only installed through a Group, so there's no direct install to uninstall. Uninstall it on the Group instead",
    groupInstallTitle: (group: string, scope: string) => `Group install “${group}” in ${scope}`,
    groupUntracked: (n: number, names: string[]) =>
      `For ${count(n, "member")}, a folder with the same name that wasn't installed by the Library is already in place: ${names.join(", ")}`,
    skipMembers: "Skip these members",
    skipMembersDesc: "The other members install as usual; Group install again later to fill them in",
    overwriteFolders: "Overwrite these folders",
    overwriteFoldersDesc: "Replace them with the Versions in the Library",
    install: "Install",
    groupInstallResults: (group: string) => `Group install “${group}”`,
    groupInstalled: (group: string, scope: string, summary: string) => `Group installed “${group}” in ${scope}\n${summary}`,
    groupUninstallTitle: (group: string, scope: string) => `Uninstall the Group install of “${group}” in ${scope}`,
    groupUninstallShared: (n: number) =>
      n === 1
        ? "The skill folder below is shared with other Agents and kept only by this Group install: uninstalling deletes it, and none of the sharing Agents will count it as installed."
        : `The ${count(n, "skill folder")} below are shared with other Agents and kept only by this Group install: uninstalling deletes them, and none of the sharing Agents will count them as installed.`,
    groupUninstalled: (group: string, scope: string) => `Uninstalled the Group install of “${group}” in ${scope}`,
    addedToGroupTitle: (skill: string, group: string) => `Added “${skill}” to “${group}”`,
    addedToGroupPartly: "It's now a member, but couldn't be installed in the Scopes below. Resolve the conflicts, then Group install again to fill them in.",
    addedToGroup: (skill: string, group: string, scopes: number) =>
      `Added “${skill}” to “${group}”${scopes ? ` and installed it in the ${count(scopes, "Scope")} the Group is installed in` : ""}`,
    removeFromGroupTitle: (skill: string, group: string) => `Remove “${skill}” from “${group}”`,
    removeFromGroupHint: (n: number) => `This Group is installed in ${count(n, "Scope")}. What should happen to skills installed only through this Group?`,
    ifUninstallToo: "If you choose “Uninstall too”:",
    ifRemoveToo: "If you choose to delete the folders too:",
    ifMigrate: "If you choose to migrate:",
    keep: "Keep",
    keepDesc: "Turn them into direct installs; nothing changes on disk",
    uninstallToo: "Uninstall too",
    uninstallTooDesc: "Remove the folders from these Scopes' directories",
    removeFromGroup: "Remove",
    removedFromGroup: (skill: string, group: string) => `Removed “${skill}” from “${group}”`,
    newGroup: "New Group",
    newGroupLabel: "Name it after a use case, e.g. “Frontend debugging”",
    create: "Create",
    groupCreatedAs: (name: string) => `That name is taken; created as “${name}”`,
    groupCreated: (name: string) => `Created Group “${name}”`,
    renameGroup: "Rename Group",
    rename: "Rename",
    groupRenamedAs: (name: string) => `That name is taken; renamed to “${name}”`,
    groupRenamed: (name: string) => `Renamed to “${name}”`,
    deleteGroupTitle: (name: string) => `Delete Group “${name}”`,
    deleteGroupHint: (installs: number) =>
      `Member skills won't be deleted.${installs ? ` The Group's ${count(installs, "Group install")} will be deleted too. What should happen to skills installed only through this Group?` : ""}`,
    deleteGroup: "Delete Group",
    groupDeleted: "Group deleted",
    reinstallSkill: (name: string) => `“${name}”`,
    allCurrent: (what: string) => `Everything installed for ${what} is already the current Version`,
    kept: keptAsIs,
    nothingReinstalled: (what: string, edited: number, missing: number) =>
      [`Nothing reinstalled (${what})`, keptAsIs(edited, missing)].filter(Boolean).join("; "),
    reinstallTitle: (what: string) => `Reinstall (${what})`,
    reinstall: "Reinstall",
    reinstallPartly: "Some couldn't be completed",
    reinstalled: (placed: number, uninstalled: number, what: string, edited: number, missing: number) =>
      [
        placed || !uninstalled ? `Reinstalled in ${count(placed, "place")} (${what})` : "",
        uninstalled ? `${count(uninstalled, "place")} uninstalled` : "",
        keptAsIs(edited, missing),
      ]
        .filter(Boolean)
        .join("; "),
    notUninstalled: "Not uninstalled",
    modifiedTitle: "Some skill folders were edited",
    reinstallConfirmTitle: "Confirm before reinstalling",
    putBackAll: "Put all back",
    missingNotice: (n: number) =>
      `The folder is gone in ${count(n, "place")}. You may have deleted it outside the app or by mistake, or it was never installed on this machine.`,
    missingPlace: (skill: string, scopes: string[]) => `${skill} · ${scopes.join(", ")}`,
    missingShared: (agents: string[]) => `Shares a folder with ${and(agents)}; put back or uninstalled together`,
    missingGrouped: (groups: string[]) =>
      `Installed through the ${and(groups.map((g) => `“${g}”`))} Group${groups.length > 1 ? "s" : ""}; can't be uninstalled on its own`,
    leaveAlone: "Leave",
    putBack: "Put back",
    reinstallOverwrite: (n: number) => `Reinstall and overwrite ${count(n, "place")}`,
    modifiedNotice:
      "These places don't match any Version in the Library and may have been edited by hand. Checked ones are overwritten with the current Version from the Library, losing the edits; unchecked ones stay as they are.",
    detach: (scopes: string[], name: string, folder: string, when: "delete" | "replace" | "move") => {
      const many = scopes.length > 1;
      const before = { delete: "Before deleting this folder", replace: "Before replacing this folder", move: "Before moving this folder" }[when];
      return `“${name}” in ${and(scopes)} ${many ? "are symlinks" : "is a symlink"} to ${folder}. ${before}, ${many ? "they each get" : "it gets"} a separate copy (of what ${many ? "they" : "it"} currently read${many ? "" : "s"}) and stay${many ? "" : "s"} installed.`;
    },
    writeLinked: (n: number) =>
      `The ${count(n, "place")} below ${n === 1 ? "is" : "are"} currently a symlink and will become a separate copy this time; the folder the link points to isn't touched. Later edits made there won't show up here.`,
    failed: "Failed",
    checkSummary: (checked: number, available: number, renamed: number, deleted: number) =>
      [
        `Checked ${count(checked, "Source")}`,
        available ? `${count(available, "skill")} with a new Version` : "",
        renamed ? `${count(renamed, "skill")} maybe renamed` : "",
        deleted ? `${count(deleted, "skill")} deleted in the Source` : "",
      ]
        .filter(Boolean)
        .join("; "),
    checkUpdates: "Update check",
    collectionFallback: (id: number) => `Collection #${id}`,
    checkFailed: "Check failed",
    checkFailedNote: (summary: string, failed: number) => `${summary}; ${count(failed, "Source")} couldn't be read. You can retry later`,
    nothingPending: (checked: number) => `Checked ${count(checked, "Source")}; no updates to handle`,
    removeFromScopes: (scopes: number, edited: number) =>
      `Remove the folders from ${count(scopes, "Scope")}${edited ? `; ${edited} of them ${edited === 1 ? "was" : "were"} edited and the edits will be lost` : ""}`,
    removeFolders: (folders: number, edited: number) =>
      `Remove ${count(folders, "skill folder")}${edited ? `; ${edited} of them ${edited === 1 ? "was" : "were"} edited and the edits will be lost` : ""}`,
    removeSkillTitle: (name: string) => `Remove “${name}”`,
    removeSkillHint: "Deletes it and all its past Versions from the Library, and takes it out of every Group.",
    removeInstalled: "Also delete the installed folders",
    keepInstalled: "Keep the installed folders",
    keepInstalledDesc: "Only delete the Library's records; the folders stay where they are",
    remove: "Remove",
    removed: "Removed",
    projectRegistered: (path: string) => `Registered project ${path}`,
    projectRelocated: (name: string, path: string) => `Pointed project “${name}” to ${path}`,
    unregisterTitle: (name: string) => `Unregister project “${name}”`,
    unregisterHint: (path: string) =>
      `${path} will no longer be managed by Mechbox; its direct install and Group install records for every Agent will be deleted.`,
    removeProjectFolders: "Also delete the folders installed in the project",
    keepProjectFolders: "Keep the folders installed in the project",
    keepProjectFoldersDesc: "The folders stay in the project directory",
    unregister: "Unregister",
    unregistered: "Project unregistered",
    sharedBy: (agents: string[]) => `In use by ${and(agents)}`,
    agentAdded: (name: string) => `Added Agent “${name}”`,
    editAgentTitle: (name: string) => `Change the directories of Agent “${name}”`,
    editAgentHint: (installed: number, groupInstalls: number) =>
      `In the old directories, this Agent has ${and([installed ? count(installed, "installed skill") : "", groupInstalls ? count(groupInstalls, "Group install") : ""].filter(Boolean))}.`,
    editAgentShared: (n: number) =>
      `${n === 1 ? "1 of these folders is" : `${n} of these folders are`} still used by other Agents and stay in the old directory for them: migrating copies them to the new directory; not migrating leaves them alone.`,
    migrate: "Migrate to the new directory",
    migrateDesc:
      "Move the installed folders over as they are and keep the Group installs; where the new directory already has a folder with the same name, it isn't overwritten or moved—the old folder stays and is listed afterwards",
    noMigrate: "Don't migrate",
    noMigrateDesc: "Only change the directory; leave the folders in the old directory alone and drop this Agent's install records and Group installs there",
    save: "Save",
    agentSaved: (name: string) => `Saved Agent “${name}”`,
    notMigrated: "Not migrated",
    notMigratedReason: (to: string, from: string) =>
      `The new directory already has a folder with the same name, or it's taken by another skill's install: ${to}\nThe old folder stays where it is: ${from}`,
    migratedPartly: (migrated: number, skipped: number) =>
      `Migrated ${count(migrated, "folder")}; ${skipped === 1 ? "the 1 below wasn't migrated, and this Agent no longer counts it" : `the ${skipped} below weren't migrated, and this Agent no longer counts them`} as installed there.`,
    agentSavedMigrated: (name: string, n: number) => `Saved Agent “${name}” and migrated ${count(n, "folder")}`,
    deleteAgentTitle: (name: string) => `Delete Agent “${name}”`,
    deleteAgentHint: "It disappears from the Agent list, and its direct install and Group install records, global and in every project, are deleted.",
    deleteAgentShared: (n: number) =>
      `The ${count(n, "folder")} below ${n === 1 ? "is" : "are"} still used by other Agents and will be kept whatever you choose; their install records pass to those Agents.`,
    keepAgentFoldersDesc: "The folders stay where they are, no longer managed by the Library",
    delete: "Delete",
    agentDeleted: (name: string) => `Deleted Agent “${name}”`,
    twin: (source: string, w: InstalledWhere) => `${source}, ${where(w)}`,
    sameNameTwins: (name: string, twins: string[], note: string) => `The Library already has a skill named “${name}” (${twins.join("; ")}). ${note}`,
    matchedVersion: (name: string, match: string, place: string) =>
      `“${name}” matches a Version of “${match}” in the Library; recorded as installed in ${place}`,
    fetchedTracking: (name: string, source: string) => `Fetched “${name}”; it will follow ${source} for update checks`,
    fetched: (name: string) => `Fetched “${name}”`,
    fetchTitle: (name: string) => `Fetch “${name}”`,
    fetch: "Fetch",
    fetchHandOver: (place: string) => `Fetching hands it over to the app. The folder in ${place} stays exactly as it is, and the Agent sees the same content.`,
    fetchCliCost: "If you keep updating it with a command-line tool afterwards, the app can only report it as edited by hand.",
    fetchInRepository: "It's committed in the project repository. Updating it in the app later changes files in the project repository.",
    sourceLink: "Source link",
    sourceLinkPlaceholder: "owner/repo, Git URL or link",
    sourceNote: (found: boolean, source: string) =>
      `${found ? "A skill with the same name was found on skills.sh, mostly matching the content here. " : ""}It will follow ${source} for update checks. Where it differs from the Source, the author's new Version will show up in Updates.`,
    noSourceNote: "No Source: no update checks. If you edit it in the Agent directory later, update it with the edits from that directory.",
  },

  update: {
    collectionFallback: (id: number) => `Collection #${id}`,
    riskExecutableAdded: "New executable",
    riskScriptAdded: "New script",
    riskScriptChanged: "Changed script",
    riskOther: "Worth a look",
    riskSummary: (executables: number, scriptsAdded: number, scriptsChanged: number) =>
      [
        executables && `${count(executables, "new executable")}`,
        scriptsAdded && `${count(scriptsAdded, "new script")}`,
        scriptsChanged && `${count(scriptsChanged, "changed script")}`,
      ]
        .filter(Boolean)
        .join(" · "),
    confirmTitle: (label: string) => `Update ${label}`,
    update: "Update",
    summaryFromScope: (from: string, label: string, placed: number, keptEdited: number, keptMissing: number) =>
      [`Updated “${label}” with the edits in ${from}`, placed ? `placed in ${count(placed, "place")}` : "", ...keptPlaces(keptEdited, keptMissing)]
        .filter(Boolean)
        .join(" · "),
    summary: (local: boolean, label: string, changed: number, placed: number, keptEdited: number, keptMissing: number) =>
      [
        local ? `Re-read “${label}”` : `Updated ${label}`,
        changed ? `${count(changed, "skill")} got a new Version` : "Content unchanged",
        placed ? `placed in ${count(placed, "place")}` : "",
        ...keptPlaces(keptEdited, keptMissing),
      ]
        .filter(Boolean)
        .join(" · "),
    rollback: "Roll back",
    rollbackInHistory: "To undo one of them, roll it back in History",
    retryTitle: "Retry the places that weren't updated",
    retry: "Retry",
    allPlaced: "All remaining places are now updated",
    failedTitle: "Some places weren't updated",
    gotIt: "OK",
    retryFailed: (n: number) => `Retry ${count(n, "place")}`,
    failedNotice: "The places below weren't updated and stay as they are. Once the cause is fixed, you can retry.",
    notPlaced: "Not updated",
    nothingDirect: (name: string) =>
      `${name} has no changes that can be updated directly; confirm renames, deletions and updates that would replace your edits in the Update plan`,
    sourceMoved: (name: string) => `The Source of ${name} just changed again. Open the Update plan and review it again`,
    goneFromDirectory: (name: string) => `“${name}” is no longer in the directory`,
    directoryMoved: "The directory changed while it was being read. Re-read it once more",
    unchanged: (name: string, keptEdited: number, keptMissing: number) =>
      [`The content of “${name}” hasn't changed`, ...keptPlaces(keptEdited, keptMissing)].filter(Boolean).join(" · "),
    rereadTitle: (name: string) => `Re-read “${name}”`,
    reinstall: "Reinstall",
    rollbackReinstalled: "Put back",
    rollbackCurrent: "Already the old Version",
    rollbackReinstalledSince: "Reinstalled since, left alone",
    rollbackModified: "Edited by you, left alone",
    rollbackMissing: "Folder missing, left alone",
    rollbackRootMissing: "Project not on this machine, left alone",
    rollbackUninstalled: "No longer installed here",
    rollbackRemoved: "Skill removed from the Library",
    rollbackFailed: "Not put back",
    rollbackUnknown: "Not put back, needs a look",
    rollbackTitle: "Roll back this Update",
    rollbackHint:
      "Skills in the Library go back to the Versions they had before this Update, and the places it changed get the old Versions back; places reinstalled since, edited by you, or not on this machine are left alone. The rolled-back changes are recorded as skipped and no longer count toward the badge; you can check them again in the Update plan whenever you want.",
    alreadyRolledBack: "This Update has already been rolled back",
    rolledBack: (names: string[], back: number) =>
      [names.length ? `Rolled back: ${and(names)} returned to the Versions before the Update` : "Rolled back: no skills to return", back ? `put back in ${count(back, "place")}` : ""]
        .filter(Boolean)
        .join(" · "),
    rollbackDone: "Rollback complete",
    rollbackLeft: (head: string) => `${head}. The items below weren't put back and stay as they are.`,
    notInterested: "These new skills won't be mentioned again; you can still find them under “See other skills in the Source”",
    keptDeleted: (name: string) => `Keeping “${name}”; you won't be reminded again`,
  },

  drawer: {
    checkAvailable: "New Version",
    checkCurrent: "Up to date",
    checkDeleted: "Deleted in Source",
    checkFailed: "Check failed",
    sameRevisionAsOthers: (n: number) => `At the same Revision as the other ${count(n, "skill")}`,
    changedCount: (n: number) => `${n} changed`,
    defaultBranch: "Default branch",
    localDirectory: "Local directory",
    viewOnGitHub: "View on GitHub",
    viewOnGitLab: "View on GitLab",
    openSite: "Open site",
    openPage: "Open page",
    resizeWidth: "Resize details",
    title: "Skill details",
    update: "Update",
    maybeRenamed: (name: string, source: string, to: string) =>
      `“${name}” may have been renamed to “${to}” in ${source}. Once you confirm it's the same skill, its Groups, install records and Version history move over.`,
    view: "View",
    keptDeleted: (source: string) => `The author deleted it from ${source}; you chose to keep it.`,
    deletedInSource: (source: string, name: string) => `The author deleted “${name}” from ${source}. Your copy is still here and you can keep using it.`,
    remove: "Remove",
    keep: "Keep",
    rereadTitle: "Read the directory's current content as a new Version and place it where it's installed (places you edited or whose folder is gone are left alone)",
    reread: "Re-read",
    reinstallAll: "Reinstall all",
    historyTitle: (name: string) => `Update history of “${name}”`,
    historyHint: "Which Updates changed it; you can roll them back",
    history: "Update history",
    otherSkillsHint: "Read the Source it was fetched from again to see what else the author has published",
    otherSkills: "See other skills in the Source",
    newSkills: (source: string, names: string[]) => `${source} published ${count(names.length, "new skill")} (${names.join(", ")})`,
    goFetch: "Fetch",
    notInterested: "Not interested",
    metadata: "Metadata",
    collection: "Collection",
    noSourceHint: "Fetched from a Scope—you're the author. After editing it where it's installed, update it with those edits",
    sameSource: "Same Source",
    revision: "Revision",
    sameName: "Same name",
    sameNameList: (n: number, name: string, twins: string[]) =>
      `The Library has ${n} other “${name}” ${n === 1 ? "skill" : "skills"}: ${twins.join("; ")}`,
    fetchedAt: "Fetched",
    currentVersion: "Version",
    versionCount: (date: string, n: number) => `${date} · ${count(n, "Version")} in total`,
    updateCheck: "Checked",
    maybeRenamedTo: (to: string) => `Maybe renamed to ${to}`,
    skipped: "This change was skipped · ",
    replacesEdits: "Updating replaces your edits · ",
    neverChecked: "Never checked",
    groups: "Groups",
    removeFromGroup: "Remove from Group",
    addToGroup: "+ Add to Group",
    newGroup: "New Group…",
    install: "Install",
    installNote: (scopes: number, stale: number) => `${count(scopes, "Scope")}${stale ? ` · ${stale} need${stale === 1 ? "s" : ""} reinstalling` : ""}`,
    scope: "Scope",
    source: "Via",
    status: "Status",
    uninstallDirect: "Uninstall the direct install",
    addDirect: "Only installed through a Group; click to add a direct install",
    direct: "Direct",
    viaGroup: (group: string | undefined) => `Via “${group}”`,
    reinstall: "Reinstall",
    updateFromHereHint: "Make the content here the Library's new Version and place it everywhere else it's installed",
    updateFromHere: "Update with these edits",
    notInstalled: "Not installed in any Scope yet.",
    switchHint: (): ReactNode => (
      <>
        The switch only controls the <b>direct install</b>; to remove a Group install, uninstall it on the Group. When neither a direct install nor a Group install
        is left, the folder is removed from that directory.
      </>
    ),
    removeSkill: "Remove skill",
    installTo: "Install in",
    usedHere: "Already used here",
    otherAgents: "Other Agents",
    allInstalled: (place: string) => `Every Agent available in ${place} already has it`,
    noAgents: (place: string) => `No Agents available in ${place}; you can show them in Settings`,
    files: "Files",
    filesNote: (n: number) => `${n} · read-only, from the Library`,
    viewSource: "View source",
    viewRendered: "View rendered",
    executable: "Executable",
    added: "Added",
    changed: "Changed",
    deleted: "Deleted",
    versions: "Version history",
    versionsNote: (n: number) => count(n, "Version"),
    versionN: (n: number | undefined) => `Version ${n}`,
    basedOn: (n: number | undefined) => `Edits based on Version ${n}`,
    maybeEdited: "Differed from the Source when fetched; may contain your edits",
    current: "Current",
    compare: "Compare with current",
    viewFiles: "View files",
    versionToCurrent: (n: number | undefined) => `Version ${n} → current`,
    collapse: "Collapse",
    versionFiles: (n: number | undefined) => `Files of Version ${n}`,
  },

  list: {
    emptyLibrary: "The Library is empty",
    emptyHint: "Fetch skills from a local directory, GitHub or the skill market.",
    fetchSkills: "Fetch skills",
    noMatch: "No matching skills",
    renamedTitle: (to: string) => `Maybe renamed to ${to} in the Source; click to see the Update plan`,
    availableTitle: "The Source has a new Version; click to see the Update plan",
    maybeRenamed: "Maybe renamed",
    newVersion: "New Version",
    keptDeletedTitle: "The author deleted it from the Source; you chose to keep it",
    keptDeleted: "Deleted in Source",
    deletedTitle: "The author deleted it from the Source; your copy is still here and you can keep using it",
    deleted: "Deleted in Source",
    groupsTitle: (names: string[]) => `Groups: ${names.join(", ")}`,
    editedTitle: "This skill in the Library has your edits; updating replaces it with the Source's new Version, and you can roll back in History later",
    edited: "Updating replaces your edits",
    twins: (n: number, name: string, w: InstalledWhere) => `${n} skills named “${name}” in the Library; this one is ${where(w)}`,
    uninstallDirect: "Uninstall the direct install",
    installDirect: "Install directly in this Scope",
    hereState: (direct: boolean, via: string[]) =>
      [direct ? "Direct install" : "", via.length ? `via ${quoted(via)}` : ""].filter(Boolean).join(" + "),
    notInstalled: "Not installed",
    agents: (names: string[]) => names.join(", "),
    projects: (n: number) => count(n, "project"),
    unmanagedTitle:
      "This folder wasn't installed by the Library: it may come from a command-line tool, be copied in by hand, or be your own. The Library only lists it and never changes or deletes it.",
    unmanaged: "Unmanaged",
    inRepositoryTitle: "This folder is committed in the project's git repository and managed by it",
    inRepository: "In project repo",
    inLockTitle: "The project's skills-lock.json lists it: the team agreed to install it, and the project repository manages it",
    inLock: "In skills-lock.json",
    linkOutsideTitle: (target: string) => `Symlink to ${target}: not in any Agent's skill directory, so not fetched by default`,
    linkOutside: "Points outside Agent directories",
    foundTitle: (source: string) =>
      `From ${source}: a skill with the same name found on skills.sh, mostly matching the content here; after fetching, it follows that Source for update checks`,
    recordedTitle: (source: string) => `From ${source}: the Source recorded by npx skills; after fetching, it follows that Source for update checks`,
    from: (source: string) => `From ${source}`,
    linkTitle: (target: string) => `Symlink to ${target}`,
    link: (target: string) => `Symlink → ${target}`,
    notByLibrary: "Not from Library",
    revealInFinder: "Show in Finder",
    fetchTitle: "Hand it over to the app: the folder stays as it is, and you can install it elsewhere and take it along with Sync",
    fetch: "Fetch",
    sameName: "Same name",
    agentDirectory: "Agent directory",
    statInstalled: "installed",
    statUnmanaged: "unmanaged",
    statStale: "to reinstall",
    reinstallAll: "Reinstall all",
    notTaken: (n: number) => `${count(n, "skill")} here ${n === 1 ? "wasn't" : "weren't"} installed by the Library; Agents use them as usual.`,
    fetchAll: "Fetch all…",
    groups: "Groups",
    groupChipTitle: (name: string, on: boolean) =>
      `“${name}” ${on ? "is Group installed; click to uninstall (removes only this Group's Group install)" : "isn't Group installed; click to Group install"}`,
    groupDrag: (name: string, n: number) => `Group “${name}” · ${count(n, "skill")}`,
    groupDragTitle: (n: number) => `Drag to a Scope on the right = Group install (${count(n, "skill")})`,
    skillCount: (n: number) => count(n, "skill"),
    groupInstalledIn: "Group installed in",
    uninstallGroupHere: "Uninstall the Group install in this Scope",
    notGroupInstalled: "Not Group installed in any Scope yet",
    groupInstallTo: (scope: string) => `Group install in ${scope}`,
    rename: "Rename",
    deleteGroup: "Delete Group",
    changedSkills: (names: string[]) => `${and(names)} changed`,
    newVersionEdited: "New Version; updating replaces your edits",
    changedSkillsEdited: (names: string[]) => `${and(names)} changed; updating replaces your edits`,
    renamedSkill: (name: string, to: string) => `${name} maybe renamed to ${to}`,
    deletedSkills: (names: string[]) => `${and(names)} deleted in the Source`,
    riskTitle: "New or changed scripts, new executables; open the Update plan to see which",
    updateEditedTitle: "Updating replaces your edits; open the Update plan to check and confirm",
    updateEdited: "Update…",
    view: "View",
    update: "Update",
    updatesFooter:
      "Updating also replaces the installed copies with the new Version; places you edited are left alone. Confirm renames, deletions and updates that would replace your edits in the Update plan.",
    updateAll: "Update all",
    revisionMove: (from: string, to: string) => `${from} → ${to}`,
    revisionLatest: (from: string, to: string) => `${from} · latest ${to}`,
    otherSkillsHint: "Read the Source it was fetched from again to see what else the author has published",
    historyTitle: (label: string) => `History of ${label}`,
    history: "History",
  },

  plan: {
    title: (label: string) => `Update ${label}`,
    planFailed: "Couldn't make the Update plan.",
    retry: "Retry",
    reading: "Reading the Source to make the Update plan…",
    skippedSkills: (names: string[]) => `Skipped this change of ${quoted(names)}`,
    skippedRevision: (label: string) => `Skipped this Revision of ${label}`,
    rejected: (name: string, to: string | undefined) => `Noted: “${name}” and “${to}” aren't the same skill`,
    skipRevisionTitle:
      "Explicitly don't take the changes in this Revision: they no longer count toward the badge until the Source changes again; you can get them back in History later",
    skipRevision: "Skip this Revision",
    cancel: "Cancel",
    updateN: (n: number) => `Update ${n}`,
    conflict: "The Source or the Library changed after this Update plan was made. Please review the Update plan again before confirming.",
    replan: "Make the Update plan again",
    allAt: (revision: string) => ` · all skills at ${revision}`,
    mixed: (text: string) => ` · ${text}`,
    latest: (revision: string) => ` · latest in Source: ${revision}`,
    firstLook: "Take a look first",
    noRisks: "These changes add or change no scripts and add no executables.",
    skills: "Skills",
    skippedTitle: "You skipped this change from the Source before; you can still check it to update",
    skipped: "Skipped",
    changed: "Changed",
    editedTitle:
      "This skill in the Library was updated with your edits from a directory, or differed from the Source when fetched and may contain your edits; checking it replaces it with the Source's new Version, and you can roll back in History later",
    edited: "Updating replaces your edits",
    hideDiff: "Hide diff",
    showDiff: "Show diff",
    skipTitle: "Explicitly don't take this change; you'll be reminded only when it changes in the Source again",
    skip: "Skip",
    folded: (names: string[]) => `${count(names.length, "more skill")} from the same Source changed (${names.join(", ")})`,
    expand: "Expand",
    renameQuestion: (name: ReactNode, to: ReactNode): ReactNode => (
      <>
        {name} may have been renamed to {to} in the Source. Is it the same skill?
      </>
    ),
    renameYes: (to: string | undefined) =>
      `Once confirmed, the skill takes the new name, and its Groups, install records and Version history move over; in Agents, invoke it as /${to}.`,
    renameNo: (to: string | undefined) => `If it's the same skill, its invocation name in Agents changes to /${to}.`,
    renameEdited: " Its content also becomes the Source's—updating replaces your edits.",
    sameOne: "Same skill",
    undo: "Undo",
    yes: "Yes",
    no: "No",
    deletedHere: (source: string) => `Deleted from ${source}. Your copy is still here and you can keep using it.`,
    keptDeleted: "Kept",
    remove: "Remove",
    keep: "Keep",
    unchanged: (names: string[]) => `Unchanged: ${names.join(", ")}`,
    empty: "The Collection has no skills left.",
    splitting: (names: string[] | number, label: string) =>
      `After updating only ${typeof names === "number" ? `the ${names} checked skills` : and(names)}, the skills in ${label} won't all be at the same Revision.`,
    places: "Installed in these places; they get the new Version too",
    uncheckedNote: "Unchecked places stay as they are; you can reinstall there separately later.",
    commits: (n: number) => `Commits · ${n}`,
    moreCommits: (n: number) => `${n} more`,
    changesOf: (name: string) => `Changes in ${name}`,
    added: "Added",
    modifiedFile: "Changed",
    deleted: "Deleted",
    modifiedHere: "You edited it here; the new Version will overwrite your edits",
    notPlaced: "The checked skills aren't installed anywhere yet; only the Library is updated.",
    rootMissing: "Project not on this machine; can't install",
    folderMissing: "Folder missing, maybe you deleted it; check to put it back",
    sharedFolder: (agents: string[]) => `Shares a folder with ${and(agents)}; replaced together`,
    linkedHere: (scopes: string[]) => `${and(scopes)} link${scopes.length === 1 ? "s" : ""} here and ${scopes.length === 1 ? "isn't" : "aren't"} checked; ${scopes.length === 1 ? "it gets" : "they get"} a separate copy first, keeping the current content`,
  },

  unmanaged: {
    nothingHere: "There are no skills here that weren't installed by the Library",
    outsideTitle: (target: string) => `Symlink to ${target}`,
    outside: "Points outside Agent directories",
    agents: (names: string[]) => names.join(", "),
    fetchingNoSource: "Fetching skills with no Source…",
    reading: (source: string) => `Reading ${source}…`,
    leftTitle: "A few weren't fetched",
    done: "Done",
    leftHint: (n: number) =>
      n === 1
        ? "The skill below wasn't fetched and stays unmanaged; Agents use it as usual. You can change the Source link and retry, or clear the link to fetch it as a skill with no Source; or leave it for now and fetch it from the Scope later."
        : `The ${count(n, "skill")} below weren't fetched and stay unmanaged; Agents use them as usual. You can change the Source link and retry, or clear the link to fetch as a skill with no Source; or leave them for now and fetch them one by one from the Scope later.`,
    linkPlaceholder: "owner/repo, Git URL or link; leave empty for no Source",
    retry: "Retry",
    fetchWithoutSource: "Fetch without Source",
    followsNow: (source: string) => `Now follows ${source}`,
    noSourceNow: "Now has no Source",
    editSource: "Change Source",
    inRepository: "In project repo",
    inLockTitle: "The project's skills-lock.json lists it: the team agreed to install it",
    inLock: "In skills-lock.json",
    leadFirstOpen: (n: number) => `Found ${count(n, "skill")} in the Agents' global directories that ${n === 1 ? "wasn't" : "weren't"} installed by the Library.`,
    leadProject: (project: string, n: number) => `Found ${count(n, "skill")} in project “${project}” that ${n === 1 ? "wasn't" : "weren't"} installed by the Library.`,
    leadScope: (scope: string, n: number) => `Found ${count(n, "skill")} in ${scope} that ${n === 1 ? "wasn't" : "weren't"} installed by the Library.`,
    lead: (lead: string) => `${lead} Fetching changes nothing on disk; Agents see the same as now.`,
    title: "Hand the skills you use over to the app",
    notNow: "Not now",
    fetchN: (n: number) => `Fetch ${n}`,
    cost: "Fetching hands them over to the app: if you keep updating these skills with a command-line tool afterwards, the app can only report them as edited by hand.",
    projectsLater: "Skills in projects are picked up when you register the project.",
    noSource: "No Source",
    groupCount: (n: number) => `· ${n}`,
    noSourceNote: "No update checks; after editing in the Agent directory, update with those edits",
    sourceNote: "Will follow it for update checks",
    conflicts: "Same name, different content—fetch only one of each",
    conflictsHint: "The most recently modified copy is selected by default. The others stay unmanaged, in place and not overwritten; fetch the one you want later.",
    modifiedAt: (date: string) => `Modified ${new Date(date).toLocaleString("en")}`,
    from: (source: string) => ` · from ${source}`,
    noneOfThem: "Don't fetch any for now",
    team: (names: string[]) =>
      `The checked ${quoted(names)} ${names.length === 1 ? "is" : "are"} managed by the project repository. Updating ${names.length === 1 ? "it" : "them"} in the app later changes files in the project repository.`,
    outsideNote: (name: string, note: string) => `Checked “${name}”: ${note}`,
    unusable: (n: number) => `${n} can't be fetched`,
    unusableReason: (name: string, reason: string) => `${name}: ${reason}`,
    sameNameTitle: (name: string, note: string) => `The Library already has a skill named “${name}”. ${note}`,
    sameName: "Same name in Library",
    fetched: (n: number, matched: number) =>
      `Fetched ${count(n, "skill")}; the app manages ${n === 1 ? "it" : "them"} now` +
      (!matched
        ? ""
        : n === 1
          ? "; it matched a skill already in the Library and was just recorded as installed here"
          : matched === 1
            ? "; 1 of them matched a skill already in the Library and was just recorded as installed here"
            : `; ${matched} of them matched skills already in the Library and were just recorded as installed here`),
  },

  history: {
    update: "Update",
    skip: "Skip",
    rollback: "Rollback",
    close: "Close",
    readFailed: "Couldn't read the history.",
    retry: "Retry",
    reading: "Reading history…",
    empty: "No Updates, Skips or Rollbacks yet.",
    fromScope: (scope: string) => `Updated with the edits in ${scope}`,
    movedTo: (from: string[], to: string) => `${from.length ? `From ${from.join(", ")} to` : "To"} ${to}`,
    revision: (revision: string) => `Revision ${revision}`,
    wholeRevision: "Whole Revision",
    rolledBackAt: (date: string) => `Rolled back the Update of ${date}`,
    rolledBackOne: "Rolled back an Update",
    reinstalled: (placed: number, failed: number) => `Reinstalled in ${count(placed, "place")}${failed ? `; ${failed} not updated` : ""}`,
    putBack: (back: number, total: number) => `Put back in ${back} / ${count(total, "place")}`,
    retryTitle: "Retry the places that weren't updated",
    stillLeft: (n: number) => `${count(n, "place")} still not updated`,
    allPlaced: "All remaining places are now updated",
    rolledBack: "Rolled back",
    commits: (n: number) => `Commits · ${n}`,
    retryFailed: "Retry the rest",
    reviewTitle: "Open the Update plan with the skills skipped here checked; if the Source has changed since, the latest content is used",
    review: "Review Update plan",
    placedOk: "Updated",
    placedCurrent: "Already this Version",
    placedFailed: "Not updated",
    places: (rollback: boolean, n: number) => `${rollback ? "Places put back" : "Places reinstalled"} · ${n}`,
    added: "Added",
    changed: "Changed",
    deleted: "Deleted",
    collapse: "Collapse",
    whatChanged: "What changed",
    fromTo: (from: string, to: string) => `From ${from || "the old Version"} to ${to || "the new Version"}`,
  },

  settings: {
    general: "General",
    agents: "Agents",
    projects: "Projects",
    checks: "Update checks",
    github: "GitHub",
    sync: "Sync",
    about: "About",
    uninstall: "Uninstall Mechbox",
    onOpen: "Only on open",
    hourly: "Hourly",
    daily: "Daily",
    weekly: "Weekly",
    back: "Back to Library",
    title: "Settings",
    stayInMenuBar: "Stay in the menu bar after closing the window",
    stayInMenuBarDesc:
      "After you close the window, the app keeps running: the Dock icon and badge stay, the menu bar gets an entry, and checks continue on schedule. When off, closing the window quits.",
    dockBadge: "Dock badge",
    dockBadgeDesc: "Shows the number of skills with a new Version; skills from local directory Sources aren't counted.",
    language: "Language",
    languageDesc: "System controls such as dialog buttons and the right-click menu in text fields follow the system language, not this setting.",
    followSystem: "System",
    // 语言选项写各自的名字，两份文案里一样。
    chinese: "简体中文",
    english: "English",
    addAgent: "Add Agent",
    registerProject: "Register project",
    missingTitle: "The project directory doesn't exist on this machine; you can change the project's location",
    missing: "Not on this machine",
    relocate: "Relocate",
    unregister: "Unregister",
    noProjects: "No projects registered yet. Once registered, you can install skills into project-level directories.",
    autoCheck: "Check for updates automatically",
    autoCheckDesc: "Check every Source for new Versions on a schedule (local directory Sources aren't checked).",
    interval: "Interval",
    tokenInKeychain: () => `The token is stored in ${store()}`,
    secretsInKeychain: () => `Secrets are stored in ${store()}`,
    version: "Version",
    databasePath: "Database location",
    uninstallDesc: () =>
      `Deletes the Library, the settings on this page, and the token and secrets in ${store()}, then moves the app to the Trash after quitting; installed skill folders are kept by default and Agents keep using them.`,
    uninstallButton: "Uninstall Mechbox…",
  },

  uninstall: {
    keep: "Keep the installed folders",
    keepDesc: "They stay where they are and Agents keep using them; after reinstalling Mechbox you can fetch them again, though you may need to re-enter their Sources",
    keepModified: (n: number) => `Move to the Trash, but keep the ${count(n, "edited place")}`,
    keepModifiedDesc: "The edited ones stay where they are; the rest go to the Trash and Agents can no longer use them",
    trash: "Move everything to the Trash",
    trashDesc: (modified: number) =>
      `Agents can no longer use these skills${modified ? `; the ${count(modified, "edited place")} ${modified === 1 ? "goes" : "go"} too` : ""}`,
    title: "Uninstall Mechbox",
    cancel: "Cancel",
    retry: "Retry",
    skipKeychain: "Skip and continue uninstalling",
    uninstall: "Uninstall and quit",
    what: () =>
      `Deletes the Library on this machine (its skills, Version history and Update history, Groups, registered projects and custom Agents), the settings on the Settings page (including GitHub and Sync), and the token and secrets in ${store()}; then quits and moves Mechbox to the Trash. If it can't be moved to the Trash, it's selected in Finder so you can drag it there.`,
    syncKept: "The Library on S3 isn't touched. You can push before uninstalling, then pull it back after reinstalling and filling in the Sync settings.",
    goSync: "Sync first…",
    noSync: "Sync isn't configured: the Library's content can't be recovered after uninstalling.",
    inspecting: "Checking the installed skill folders…",
    folders: (global: number, inProjects: number, projects: string[], offMachine: number) =>
      `Installed skill folders: ${global} global${inProjects > 0 ? `; ${inProjects} in projects (${projects.join(", ")})` : ""}.` +
      `${offMachine > 0 ? ` ${count(offMachine, "more place")} in projects not on this machine ${offMachine === 1 ? "isn't" : "aren't"} affected.` : ""} Skills not installed by the Library aren't affected.`,
    adopted: (inProjects: boolean) =>
      `This includes folders that were already there before fetching and were later handed over to Mechbox.${inProjects ? " Folders in projects that are committed to a repository will show up as git changes once moved." : ""}`,
    modified: (n: number, trash: boolean) =>
      `The ${count(n, "place")} below ${n === 1 ? "was" : "were"} edited${trash ? " and will be moved to the Trash with the rest" : ""}:`,
    trashing: (n: number) => `Moving ${count(n, "skill folder")} to the Trash…`,
    uninstalling: "Uninstalling…",
    keychainFailed: (message: string) =>
      `Couldn't delete the token and secrets in ${store()}${deniedHint()}: ${message}\nMechbox hasn't been uninstalled and nothing was changed. You can retry, or skip${manualDelete()}.`,
    failed: (error: string, keychainDeleted: boolean) =>
      `Mechbox wasn't uninstalled; the Library and settings are still there: ${error}\nFolders already moved to the Trash can be put back from it; a retry skips them.${keychainDeleted ? ` The token and secrets in ${store()} have been deleted; re-enter them in Settings to keep using them.` : ""}`,
    done: "Uninstalled. Quitting…",
  },

  sync: {
    noRemote: "There's no Library on S3 yet: no machine has pushed one.",
    notALibrary: "The file on S3 isn't a Library, or it's corrupted.",
    newerApp: "The Library on S3 comes from a newer version of the app: upgrade the app on this machine first.",
    olderApp: "The Library on S3 comes from an older version of the app: upgrade the app on the machine that pushed it, then push again.",
    unknownMachine: "Unknown machine",
    pushedAt: (from: string, at: string) => `Pushed from ${from} at ${at}`,
    pushed: (from: string) => `Pushed from ${from}`,
    conflict: "The Library on S3 changed after you looked at the Sync plan. Please look at the Sync plan again before confirming",
    pushDone: (remote: string) => `Pushed to S3 (${remote})`,
    pullDone: "Pulled from S3 and replaced the Library on this machine",
    title: "Sync",
    back: "Back",
    pullConfirm: "Pull and replace this machine's Library",
    pushOverwrite: "Push and overwrite the Library on S3",
    push: "Push",
    intro: "Manually push the whole Library to S3, or pull it back, to reproduce the same setup on another machine. Never automatic.",
    notConfigured:
      "S3 isn't configured: fill in the bucket, Access Key ID and Secret Access Key in the Sync section of Settings, or set the environment variables SKILLS_S3_BUCKET, AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY (optionally SKILLS_S3_ENDPOINT, SKILLS_S3_KEY, AWS_REGION and AWS_SESSION_TOKEN).",
    pushTo: "Push to S3…",
    pullFrom: "Pull from S3…",
    pulled:
      "Switched to the Library from S3. Folders in Agent directories weren't touched: for projects marked “Not on this machine” on the right, first point them to their location on this machine; then check each Scope's status and use “Reinstall all” as needed.",
    direction: "Direction",
    toLocal: "S3 → this machine",
    toRemote: "This machine → S3",
    remote: "Library on S3",
    none: "None yet",
    contents: "Contains",
    counts: (skills: number, groups: number, projects: number) =>
      `${count(skills, "skill")} · ${count(groups, "Group")} · ${count(projects, "registered project")}`,
    pushWarning: "Pushing overwrites this copy on S3 with this machine's Library.",
    cannotPull: "The Library on S3 can't be pulled.",
    pullWarning:
      "Pulling replaces this machine's whole Library: skills, Groups and projects that are here but not in that Library will disappear. Folders in Agent directories aren't changed.",
    missingProjects: (names: string[]) =>
      `These projects aren't on this machine; after pulling, point them to their location here from the right sidebar: ${names.join(", ")}`,
  },

  sidebar: {
    fetch: "Fetch skills",
    market: "Skill market",
    allSkills: "All skills",
    updates: "Updates",
    updatesTitle: "Updates to handle: skills with a new Version, deleted in the Source, or maybe renamed; matches the Dock badge",
    checkTitle: "Check every Source for new Versions (read-only; local directory Sources aren't checked)",
    check: "Check for updates",
    collections: "Collections",
    bySource: "By Source",
    noCollections: "Once you fetch two or more skills from the same Source, they're grouped into a Collection automatically.",
    groups: "Groups",
    newGroup: "New Group",
    groupTitle: "Click to filter; drag to a Scope on the right = Group install; drag a skill card here = add to this Group",
    rename: "Rename",
    deleteGroup: "Delete Group",
    noGroups: "No Groups yet. Gather skills by use case to install them together.",
    sync: "Sync",
    manual: "Manual",
    notConfigured: "Not set up",
    settings: "Settings",
  },

  review: {
    kindGitHub: "GitHub repo",
    kindGit: "Git repo",
    kindURL: "Direct download",
    kindWellKnown: "well-known index",
    kindLocal: "Local directory",
    kindUnknown: "Unknown Source",
    fetched: (n: number) => `Fetched ${count(n, "skill")}`,
    alreadyTitle: "A skill with the same name from the same Source is already in the Library; to get new content, click “Update” in the skill's details",
    already: "Fetched",
    newTitle: "Appeared after the last Fetch from this Source",
    new: "New",
    title: "Preview",
    back: "Back",
    selected: (n: number, total: number) => `${n} / ${total} selected`,
    fetchSelected: "Fetch selected",
    recognizedAs: "Recognized as",
    branch: (ref: string) => `Branch ${ref}`,
    noToken: "No GitHub token is set in Settings and GITHUB_TOKEN / GH_TOKEN aren't set either; anonymous GitHub API access is easily rate-limited.",
    candidates: (n: number) => count(n, "Candidate"),
    selectAll: "Select all",
    selectNone: "Select none",
    noCandidates: "No skills to fetch were found in the Source.",
    diagnostics: (n: number | undefined) => `${n} ${n === 1 ? "path" : "paths"} didn't become Candidates`,
    viewAudit: "View audit",
  },

  agents: {
    shownHint:
      "Checked Agents appear wherever you choose where to install. Agents that already have skills installed are still listed in skill details, Updates and inspections, checked or not.",
    custom: "Custom",
    detected: "Detected",
    notDetected: "Not detected",
    directories: (global: string, project: string) => `${global} · in projects: ${project}`,
    edit: "Edit",
    delete: "Delete",
    show: (name: string) => `Show ${name}`,
    shown: "Shown",
    hidden: "Hidden",
    nameRequired: "Enter a name",
    globalRequired: "Enter the global skill directory",
    globalInHome: "The global skill directory must be inside your home directory (not the home directory itself)",
    projectRequired: "Enter the skill directory in projects",
    projectRelative: "Enter a path relative to the project root, e.g. .agents/skills",
    projectInside: "Must be inside the project root (not the project root itself)",
    editTitle: (name: string) => `Edit Agent “${name}”`,
    addTitle: "Add Agent",
    cancel: "Cancel",
    save: "Save",
    add: "Add",
    name: "Name",
    global: "Global skill directory (inside your home directory)",
    choose: "Choose…",
    chooseGlobal: "Choose the global skill directory",
    project: "Skill directory in projects (relative to the project root)",
  },

  scopes: {
    issues: (n: number) => `${count(n, "skill folder")} ${n === 1 ? "doesn't" : "don't"} match the current Version and need${n === 1 ? "s" : ""} reinstalling`,
    collapse: "Collapse",
    more: (n: number) => `${n} other ${n === 1 ? "Agent" : "Agents"}`,
    noAgents: "No Agents available; you can show them in Settings",
    global: "Global",
    projects: "Registered projects",
    register: "Register project",
    missingTitle: (path: string) => `${path}\nNot on this machine`,
    missingBadgeTitle: "The project directory doesn't exist on this machine; you can change the project's location",
    missing: "Not on this machine",
    relocateTitle: "Change project location",
    unregisterTitle: "Unregister project",
    noProjects: "No projects registered yet. Once registered, you can install skills into project-level directories.",
    relocateDialog: (name: string) => `Change the location of project “${name}”`,
    cancel: "Cancel",
    relocate: "Relocate",
    registerShort: "Register",
    relocateHint:
      "Point the project to a directory on this machine (after syncing from another machine, or after the project directory was moved or renamed). Direct installs and Group installs are kept; skill folders at the old location aren't moved and are no longer managed by the Library.",
    registerHint: "Once registered, the project and each Agent form project-level Scopes, and skills can be installed into the project's Agent directories.",
    pathPlaceholder: "/path/to/project or ~/path/to/project",
    chooseRelocate: "Choose the project's directory on this machine",
    chooseRegister: "Choose the project directory to register",
    choose: "Choose…",
  },

  fetch: {
    title: "Fetch skills",
    cancel: "Cancel",
    preview: "Preview",
    inputPlaceholder: "anthropics/skills, https://gitlab.com/team/skills, ~/skills",
    hint: "Paste where the skills come from and the Library will recognize the kind of Source: a local directory, a GitHub repo (owner/repo, owner/repo@skill-name or a link), a Git repo URL on GitLab or elsewhere (including SSH), a SKILL.md / zip / tar download link, or a site with a well-known index. Fetching copies the content as of the Preview.",
    choose: "Choose…",
    chooseDirectory: "Choose a skill directory",
    defaultBranch: "Default branch",
    refs: "Branch / tag",
    refsFailed: (reason: string) => `Couldn't get the list of branches and tags; you can still preview.\n${reason}`,
    readingRefs: "Reading branches and tags…",
    filterRefs: "Filter branches and tags",
    noRefs: "No matching branch or tag",
    branches: "Branches",
    tags: "Tag",
    locked: (ref: string | undefined) => `The branch is set by ${ref} in the input; edit the input to change it.`,
  },

  audit: {
    failed: (n: number) => `Audit: ${n} failed`,
    warned: (n: number) => `Audit: ${count(n, "warning")}`,
    passed: (n: number) => `Audit passed ${n}`,
    pass: "Pass",
    warn: "Warning",
    fail: "Fail",
    none: "No audit",
    retryTitle: "Click to retry",
    unavailable: "Audit unavailable",
    title: "Audit",
    note: "From skills.sh · for reference only",
    loading: "Querying skills.sh…",
    unavailableLong: "Can't get the audit from skills.sh right now.",
    retry: "Retry",
    noAudit: "skills.sh has no audit for this skill yet.",
    risk: (level: string) => `Risk ${level}`,
    auditedAt: (date: string) => `Audited ${date}`,
    details: "Details",
    scope: "The audit covers the content listed on skills.sh, which may not be this Version in the Library.",
  },

  toolbar: {
    toggleLeft: "Show/hide sidebar",
    search: "Search skills",
    installedOnlyTitle: (agent: string) => `Only show skills installed in this ${agent} Scope`,
    installedOnly: "Installed only",
    list: "List",
    card: "Cards",
    refresh: "Refresh (re-read the Library and inspect installed skills against the disk)",
    toggleRight: "Show/hide Scope sidebar",
    chooseScope: "Choose Scope",
    clearScope: "Clear Scope",
    moreAgents: (others: boolean, n: number) => `+ ${n} ${others ? "other " : ""}${n === 1 ? "Agent" : "Agents"}`,
    clear: "Clear",
  },

  fromScope: {
    title: (place: string, name: string) => `Update “${name}” with the edits in ${place}`,
    readFailed: "Couldn't read the edits here.",
    retry: "Retry",
    reading: (place: string) => `Reading the folder in ${place}…`,
    cancel: "Cancel",
    update: "Update",
    conflict: "The files here or the Library changed after reading. Please review again before confirming.",
    reread: "Re-read",
    becomes: (place: string, name: string) => `The content in ${place} becomes the new Version of “${name}” in the Library; you can roll it back in Update history later.`,
    same: (scopes: string[]) => `${and(scopes)} ${scopes.length === 1 ? "is" : "are"} the same folder as here and already ${scopes.length === 1 ? "has" : "have"} these edits.`,
    places: "Also installed in these places; they get these edits too",
    modifiedHere: "You edited it here too; these edits will overwrite the ones here",
    uncheckedNote: "Unchecked places stay as they are; you can reinstall there separately later.",
    nowhereElse: (name: string) => `“${name}” isn't installed anywhere else; only the Library is updated.`,
  },

  credentials: {
    keychainFailed: () => `Couldn't read ${store()}, so the token and secrets can't be viewed or changed right now.${readFailedHelp()}`,
    retry: "Retry",
    endpoint: "Endpoint (optional, defaults to AWS S3)",
    bucket: "Bucket",
    key: "Object key (optional, defaults to library.db)",
    region: "Region (optional, defaults to us-east-1)",
    accessKeyId: "Access Key ID",
    secretAccessKey: "Secret Access Key",
    sessionToken: "Session Token (optional)",
    tokenSaved: "Saved the GitHub token",
    tokenHint: "When empty, it's read from the GITHUB_TOKEN or GH_TOKEN environment variable.",
    token: "Personal access token",
    syncSaved: "Saved the Sync settings",
    syncHint: "When all are empty, they're read from the SKILLS_S3_* and AWS_* environment variables; fill in any one and the whole set comes from here.",
    save: "Save",
  },

  market: {
    title: "Skill market",
    placeholder: "Search skills on skills.sh",
    search: "Search",
    viewOnSite: (name: string) => `View “${name}” on skills.sh`,
    installs: (n: number) => count(n, "install"),
    fetch: "Fetch",
    none: "No skills found.",
    hint: "Results come from skills.sh. Fetching starts with a Preview where you can review the Candidates; skills from GitHub repos also show audit information.",
  },

  diff: {
    reading: (path: string) => `Reading ${path}…`,
    failed: (path: string) => `Couldn't read ${path}.`,
    unchanged: (n: number) => `… ${count(n, "unchanged line")} …`,
  },

  layout: {
    resizeTitle: "Drag to resize; double-click to reset",
    resizeLeft: "Resize sidebar",
    resizeRight: "Resize Scope sidebar",
    alreadyThere: "Already there",
  },

  app: {
    loadFailed: "Couldn't read the Library",
    retry: "Retry",
  },
} satisfies Messages;
