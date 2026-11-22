// 未纳管技能：作用域的技能目录里不是库装的技能文件夹。能不能入库、归谁管，和库里某个技能一模一样的那一份怎么说。
// 默认勾不勾(归项目仓库管的、指向 Agent 目录以外的默认不接)是默认勾选的规则，见 lib/consent。
import { SkillFileProblemKind, SkillScopeStatus, type Skill, type SkillFileProblem, type SkillID, type SkillScopeInspection, type UnmanagedSkill } from "@bindings/internal/library";
import { messages } from "./i18n";
import { skillScopeIn } from "./installs";
import type { Snapshot } from "./library";
import { homePath } from "./paths";
import { scopeKey, type ScopeKey } from "./scope";

// unmanagedIn 是作用域的技能目录里不是库装的技能文件夹(未纳管技能)。
export function unmanagedIn(s: Snapshot, key: ScopeKey): UnmanagedSkill[] {
  return s.unmanaged.filter((u) => scopeKey(u.scope) === key);
}

// fetchable 是能入库的未纳管技能:SKILL.md 写错了的(invalid)不算。整批入库的入口只在有能入库的时候出现。
export function fetchable(u: UnmanagedSkill): boolean {
  return !u.invalid;
}

// skillFileProblemText 说明 SKILL.md 哪里写错了(见 UnmanagedSkill.invalid):哪个文件、第几行、怎样改。
export function skillFileProblemText(p: SkillFileProblem): string {
  const t = messages().list;
  switch (p.kind) {
    case SkillFileProblemKind.SkillFileNoFrontmatter:
      return t.skillFileNoFrontmatter;
    case SkillFileProblemKind.SkillFileUnclosedFrontmatter:
      return t.skillFileUnclosed;
    case SkillFileProblemKind.SkillFileInvalidYAML:
      return t.skillFileYAML(p.line ?? 0);
    case SkillFileProblemKind.SkillFileUnquotedColon:
      return t.skillFileColon(p.line ?? 0);
    case SkillFileProblemKind.SkillFileInvalidName:
      return t.skillFileName;
    case SkillFileProblemKind.SkillFileNoDescription:
      return t.skillFileDescription;
  }
  return t.skillFileOther;
}

// ---------- 归谁管 ----------

type Ownership = Pick<UnmanagedSkill, "in_repository" | "in_project_lock" | "link_outside">;

// repoOwned 是归项目仓库管的未纳管技能：提交在项目仓库里，或记在项目的 skills-lock.json 里。
export const repoOwned = (u: Pick<Ownership, "in_repository" | "in_project_lock">) => !!(u.in_repository || u.in_project_lock);

// notTakenOver 是还没交给 app 管的、用户自己的未纳管技能：归项目仓库管的、符号链接指向 Agent 目录以外的(作者有意留在自己目录里的)不算。
// 整批入库时默认勾哪些、照装时说哪些要先入库才带得过去，都按它。
export const notTakenOver = (u: Ownership) => !repoOwned(u) && !u.link_outside;

// folderNotes 是未纳管文件夹归谁管、指向哪里的标注，写在行的最后一列：提交在项目仓库里或记在 skills-lock.json 里、
// 指向 Agent 目录以外(写出链到哪里，home 是用户主目录，路径缩写成 ~)。"不在技能库里"的一行和显示成库里技能一行的副本(identicalIn)都写这几条。
export function folderNotes(u: Ownership & Pick<UnmanagedSkill, "link_target">, home?: string): { text: string; title: string }[] {
  const t = messages().list;
  return [
    u.in_repository
      ? { text: t.inRepository, title: t.inRepositoryTitle }
      : u.in_project_lock
        ? { text: t.inLock, title: t.inLockTitle }
        : undefined,
    u.link_outside
      ? { text: u.link_target ? t.linkOutsideTo(homePath(home, u.link_target)) : t.linkOutside, title: t.linkOutsideTitle(u.link_target ?? "") }
      : undefined,
  ].filter((x) => !!x);
}

// outsideLinkNote 是指向 Agent 目录以外的符号链接入库时的说明:之后在 app 里更新会换掉链接。
export const outsideLinkNote = (target: string) => messages().model.outsideLinkNote(target);

// ---------- 和库里某个技能一模一样的那一份 ----------

// splitIdentical 把作用域里的未纳管文件夹分开：identical 是和库里某个技能一样的(就是那个技能),unmanaged 是其余的。
export function splitIdentical(list: UnmanagedSkill[]): { identical: UnmanagedSkill[]; unmanaged: UnmanagedSkill[] } {
  return { identical: list.filter((u) => !!u.skill_id), unmanaged: list.filter((u) => !u.skill_id) };
}

// identicalIn 是作用域里内容和库里这个技能完全一样、但不是库装的那个文件夹(技能库认出它就是这个技能，见 UnmanagedSkill.skill_id);
// 作用域页、项目页把它显示成库里这个技能的一行，点"入库"只补记安装。
export function identicalIn(s: Snapshot, key: ScopeKey, skillID: SkillID): UnmanagedSkill | undefined {
  return unmanagedIn(s, key).find((u) => u.skill_id === skillID);
}

// extraCopy 是这个作用域已经装着它那个技能时，同一个作用域里另一份一样的文件夹：它也是那个技能，但一个技能在一个作用域只记一处安装，
// 入库没什么可做(技能库不另记，整批入库的计划里也没有它),不给"入库",不算进要入库的个数。
export function extraCopy(s: Snapshot, u: UnmanagedSkill): boolean {
  return !!u.skill_id && !!skillScopeIn(s, u.skill_id, scopeKey(u.scope));
}

// extraCopyNote 是多出来的那一份(extraCopy)怎么说：here 是这里装着的那一处;它的文件夹不在了时(如用户给它改了名),
// 不说"多一份",只说这一份和库里的一样。
export function extraCopyNote(skill: Skill, here: SkillScopeInspection): { text: string; title: string } {
  const t = messages().list;
  return here.status === SkillScopeStatus.SkillScopeMissing
    ? { text: t.extraCopyMissing, title: t.extraCopyMissingTitle(skill.name) }
    : { text: t.extraCopy, title: t.extraCopyTitle(skill.name) };
}

// identicalsOf 是各处这个技能的、不是库装的文件夹(见 identicalIn),一个文件夹一条：共用一个目录的几个 Agent 那里是同一个文件夹，只算一处;
// 同一处有几份一样的(多出来的几份)各算一条。
export function identicalsOf(s: Snapshot, skillID: SkillID): UnmanagedSkill[] {
  const folders = new Set<string>();
  return s.unmanaged.filter((u) => {
    if (u.skill_id !== skillID || folders.has(u.folder)) return false;
    folders.add(u.folder);
    return true;
  });
}

// identicalState 是这个技能那一份不是库装的文件夹(identicalIn)怎么说：不是 app 装的，接着写归谁管、指向哪里(folderNotes),
// 符号链接时 title 里写出指向哪里(指向 Agent 目录以外的，folderNotes 已经写了，不再写一遍);stale 表示和库里的旧版本一样。作用域页的行和技能详情的安装表都用它。
// here 是这个作用域里这个技能的安装(有就是多出来的一份，见 extraCopy):写明这里已经装着这个技能、不用入库(extraCopyNote)。
export function identicalState(skill: Skill, u: UnmanagedSkill, home?: string, here?: SkillScopeInspection): { text: string; title: string; stale: boolean } {
  const t = messages().list;
  // 归谁管写在前面(在项目仓库里、链到哪里),一眼看得出这一份不是库装过来的。
  const own = here ? extraCopyNote(skill, here) : { text: t.identicalHere, title: t.identicalHereTitle(skill.name) };
  const notes = [...folderNotes(u, home), own];
  const titles = [...notes.map((x) => x.title), ...(u.link_target && !u.link_outside ? [t.linkTitle(u.link_target)] : [])];
  return { text: notes.map((x) => x.text).join(" · "), title: titles.join("\n"), stale: u.version_id !== skill.version_id };
}
