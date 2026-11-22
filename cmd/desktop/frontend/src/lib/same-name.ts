// 同名：同一个 Agent 的全局和项目里同名时会怎样，同一层的别处同名不同内容的几份，库里同名的另外几个技能。
import { SameNameRule, type PlannedUnmanagedSkill, type Scope, type Skill, type SkillID, type UnmanagedSkill } from "@bindings/internal/library";
import { messages } from "./i18n";
import { installedWhere } from "./installs";
import type { Snapshot } from "./library";
import { shortPath } from "./paths";
import { parseScope, placeOf, projectName, scopeKey, type ScopeKey } from "./scope";
import { noSource, sourceLabel } from "./source";
import { unmanagedIn } from "./unmanaged";

// ---------- 同一个 Agent 的全局和项目里同名 ----------

// SameNameKind 是同名的一对里一边那份是什么：库装的(managed)、和库里某个技能一样但不是库装的(identical,见 lib/unmanaged 的 identicalIn:
// 它就是库里的技能，不说"不在技能库里")、不在技能库里的(unmanaged)。
export type SameNameKind = "managed" | "identical" | "unmanaged";

function sameNameKind(s: Snapshot, side: { skill_id?: SkillID; folder: string }): SameNameKind {
  if (side.skill_id) return "managed";
  return s.unmanaged.some((u) => u.folder === side.folder && !!u.skill_id) ? "identical" : "unmanaged";
}

// sameNameNote 说明作用域里的一个技能文件夹和同一个 Agent 全局 / 某个已登记项目里的文件夹同名时会怎样:
// 按 Agent 已知的规则说出后果(Claude Code 里全局的生效，Codex 里两份都会出现),没有已知规则的只说同名。
// 同名按 SKILL.md 里的 name,两处的文件夹名可以不同。folder 是这个作用域里那个技能文件夹;没有同名时返回 undefined。
// title 列出另一处的文件夹。
export function sameNameNote(s: Snapshot, key: ScopeKey, folder: string): { text: string; title: string } | undefined {
  const m = messages().model;
  const scope = parseScope(key);
  const agent = s.agents.find((a) => a.code === scope.agent);
  const rule = agent?.same_name ?? SameNameRule.SameNameUnknown;
  const who = agent?.name ?? scope.agent;
  if (scope.project_id) {
    const pairs = s.sameName.filter((p) => p.agent === scope.agent && p.project_id === scope.project_id && p.project.folder === folder);
    if (pairs.length === 0) return undefined;
    const kinds = [...new Set(pairs.map((p) => sameNameKind(s, p.global)))];
    const name = pairs[0].name;
    const text = {
      [SameNameRule.SameNameGlobalWins]: m.sameNameGlobalWins(name, kinds, who),
      [SameNameRule.SameNameBothShown]: m.sameNameGlobalBoth(name, kinds, who),
      [SameNameRule.SameNameUnknown]: m.sameNameGlobalUnknown(name, kinds),
    }[rule];
    return { text, title: pairs.map((p) => m.sameNameGlobalFolder(shortPath(s, p.global.folder))).join("\n") };
  }
  const pairs = s.sameName.filter((p) => p.agent === scope.agent && p.global.folder === folder);
  if (pairs.length === 0) return undefined;
  const name = pairs[0].name;
  const projects = [...new Set(pairs.map((p) => projectName(s, p.project_id)))];
  const text = {
    [SameNameRule.SameNameGlobalWins]: m.sameNameProjectWins(projects, name, who),
    [SameNameRule.SameNameBothShown]: m.sameNameProjectBoth(projects, name, who),
    [SameNameRule.SameNameUnknown]: m.sameNameProjectUnknown(projects, name),
  }[rule];
  return { text, title: pairs.map((p) => m.sameNameProjectFolder(projectName(s, p.project_id), shortPath(s, p.project.folder))).join("\n") };
}

// skillSameNameNote 是库里一个技能在各处的同名说明(技能库页、更新页的一行用):同一个 Agent 的全局和某个项目里同名的一对只说一次,
// 写出是哪两处(各是库装的、和库里一样的还是不在技能库里的)和后果;text 与 title 相同，逐对一行。没有同名时返回 undefined。
export function skillSameNameNote(s: Snapshot, skillID: SkillID): { text: string; title: string } | undefined {
  const m = messages().model;
  const lines = s.sameName
    .filter((p) => p.global.skill_id === skillID || p.project.skill_id === skillID)
    .map((p) => {
      const agent = s.agents.find((a) => a.code === p.agent);
      const who = agent?.name ?? p.agent;
      const say = {
        [SameNameRule.SameNameGlobalWins]: m.sameNamePairWins,
        [SameNameRule.SameNameBothShown]: m.sameNamePairBoth,
        [SameNameRule.SameNameUnknown]: m.sameNamePairUnknown,
      }[agent?.same_name ?? SameNameRule.SameNameUnknown];
      return say(who, projectName(s, p.project_id), p.name, sameNameKind(s, p.global), sameNameKind(s, p.project));
    });
  const text = [...new Set(lines)].join("\n");
  return text ? { text, title: text } : undefined;
}

// ---------- 同名不同内容的几份 ----------
// 同一个技能名在几个作用域里都有不是库装的文件夹、内容又不同时，不合成一个技能(入库时只入一份),界面要把同名说出来。
// 和这个作用域比的是同一层的别处：全局比各 Agent 的全局，项目比这个项目里的各 Agent。内容同不同由技能库比(sameNameCopiesQuery)。

// sameNameScopes 是这个作用域要和哪几处比：这里的未纳管技能在同一层的别处有同名的(文件夹不同)时，返回这里加上那几处;没有时为空。
export function sameNameScopes(s: Snapshot, key: ScopeKey): Scope[] {
  const place = placeOf(parseScope(key));
  const here = unmanagedIn(s, key);
  const names = new Set(here.map((u) => u.name));
  const folders = new Set(here.map((u) => u.link_target || u.folder));
  const others = s.unmanaged.filter(
    (u) => scopeKey(u.scope) !== key && placeOf(u.scope) === place && names.has(u.name) && !folders.has(u.link_target || u.folder),
  );
  if (!others.length) return [];
  return [parseScope(key), ...new Map(others.map((u) => [scopeKey(u.scope), u.scope])).values()];
}

// sameNameCopies 从技能库比出的计划里取出同名不同内容的几份：按这个作用域里的文件夹，列出别处内容不同的同名文件夹。
// 计划里同名的几条都标 conflict,但 conflict 也包括同一个作用域里内容一样的另一个文件夹(一处只能记一个安装，拆成两条);
// 所以只把作用域不在这一条里的那几条算作内容不同：内容一样的别处已经和这里合在同一条里了。
export function sameNameCopies(plan: PlannedUnmanagedSkill[], key: ScopeKey): Map<string, UnmanagedSkill[]> {
  const result = new Map<string, UnmanagedSkill[]>();
  const conflicted = plan.filter((p) => p.conflict && !p.error);
  for (const p of conflicted) {
    const mine = new Set((p.folders ?? []).map((f) => scopeKey(f.scope)));
    if (!mine.has(key)) continue;
    const others = conflicted
      .filter((x) => x !== p && x.name === p.name)
      .flatMap((x) => (x.folders ?? []).filter((f) => !mine.has(scopeKey(f.scope))));
    if (!others.length) continue;
    for (const f of p.folders ?? []) if (scopeKey(f.scope) === key) result.set(f.folder, others);
  }
  return result;
}

// ---------- 库里同名的技能 ----------

// sameNameInLibrary 是库里和它同名的其他技能(同名但内容不同的不合并，见入库)。
export function sameNameInLibrary(s: Snapshot, skill: Pick<Skill, "id" | "name">): Skill[] {
  return s.skills.filter((x) => x.name === skill.name && x.id !== skill.id);
}

// skillSourceLabel 是技能来自哪个来源的说法：没有来源的写「没有来源」。
export function skillSourceLabel(s: Snapshot, skill: Skill): string {
  return noSource(skill.source) ? messages().model.noSource : sourceLabel(s, skill.source);
}

// twinNote 是库里一个同名技能哪来的、装在哪，供各处的同名提示分辨是哪一个。
export function twinNote(s: Snapshot, skill: Skill): string {
  return messages().actions.twin(skillSourceLabel(s, skill), installedWhere(s, skill.id));
}

// sameNameFetchNote 说明入库一个和库里技能同名的技能会怎样：不带来源时和库里所有技能比，带来源时只和这个来源里同名的比。
export function sameNameFetchNote(name: string, sourced: boolean): string {
  return sourced ? messages().model.sameNameFetchSourced(name) : messages().model.sameNameFetchUnsourced(name);
}
