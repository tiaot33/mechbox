// 同名：同一个 Agent 的全局和项目里同名时会怎样，同一个作用域的两个技能目录里同名，同一层的别处同名不同内容的几份，库里同名的另外几个技能。
import {
  SameNamePairKind as PairKind,
  SameNameRule,
  type PlannedUnmanagedSkill,
  type SameNameFolder,
  type SameNameSkill,
  type Scope,
  type Skill,
  type SkillID,
  type UnmanagedSkill,
} from "@bindings/internal/library";
import { messages } from "./i18n";
import { installedWhere } from "./installs";
import { agentShown, mergedAgents, scopeLabel } from "./places";
import type { Snapshot } from "./library";
import { shortPath } from "./paths";
import { agentName, parseScope, placeOf, projectName, scopeKey, type ScopeKey } from "./scope";
import { sourceLabel } from "./source";
import { unmanagedIn } from "./unmanaged";

// ---------- 同一个 Agent 的全局和项目里同名、同一个作用域两个目录里同名 ----------

// pairsOf 是某一种同名的那几对。只因共用目录被动算出已装(自己在这两处都没有安装记录)、又被关掉显示的 Agent 的不算：用户不想看到它。
function pairsOf(s: Snapshot, kind: PairKind): SameNameSkill[] {
  const owns = (scope: Scope) =>
    s.skillScopes.some((p) => p.scope.agent === scope.agent && placeOf(p.scope) === placeOf(scope) && !p.shared_only);
  return s.sameName.filter((p) => {
    if (p.kind !== kind) return false;
    const agent = s.agents.find((a) => a.code === p.second.scope.agent);
    return !agent || agentShown(s.agentsShown, agent) || owns(p.first.scope) || owns(p.second.scope);
  });
}

// SameNameKind 是同名的一对里一边那份是什么：库装的(managed)、和库里某个技能一样但不是库装的(identical,见 lib/unmanaged 的 identicalIn:
// 它就是库里的技能，不说"不在技能库里")、不在技能库里的(unmanaged)。
export type SameNameKind = "managed" | "identical" | "unmanaged";

function sameNameKind(s: Snapshot, side: { skill_id?: SkillID; folder: string }): SameNameKind {
  if (side.skill_id) return "managed";
  return s.unmanaged.some((u) => u.folder === side.folder && !!u.skill_id) ? "identical" : "unmanaged";
}

type Note = { text: string; title: string };

// sameNameNote 说明作用域里的一个技能文件夹和别处同名时会怎样:folder 是这个作用域里那个技能文件夹;没有同名时返回 undefined。
// 同名按 SKILL.md 里的 name,两处的文件夹名可以不同;title 列出另一处的文件夹。两种同名都有时逐行写。
// - 和同一个 Agent 全局 / 某个已登记项目里的文件夹同名:按 Agent 已知的规则说出后果(Claude Code 里全局的生效，Codex 里两份都会出现),
//   没有已知规则的只说同名。
// - 和这个作用域的另一个技能目录里的文件夹同名：不知道哪个生效，只说同名。合成一处的几个 Agent 目录相同，只说一次。
export function sameNameNote(s: Snapshot, key: ScopeKey, folder: string): Note | undefined {
  const notes = [globalAndProjectNote(s, key, folder), twoDirectoriesNote(s, key, folder)].filter((n): n is Note => !!n);
  if (!notes.length) return undefined;
  return { text: notes.map((n) => n.text).join("\n"), title: notes.map((n) => n.title).join("\n") };
}

// byRule 把同一处同名在合成一处的几个 Agent 上的那几对，按各 Agent 自己的规则分组：有已知规则的只写它们，主语只写持有这条规则的 Agent;
// 都没有已知规则时写一组(只说同名)。agentOf 取一对里是哪个 Agent 的。
function byRule(s: Snapshot, pairs: SameNameSkill[], agentOf: (p: SameNameSkill) => string) {
  const groups = new Map<SameNameRule, { codes: string[]; pairs: SameNameSkill[] }>();
  for (const p of pairs) {
    const code = agentOf(p);
    const rule = s.agents.find((a) => a.code === code)?.same_name ?? SameNameRule.SameNameUnknown;
    const group = groups.get(rule) ?? { codes: [], pairs: [] };
    if (!group.codes.includes(code)) group.codes.push(code);
    group.pairs.push(p);
    groups.set(rule, group);
  }
  const known = [...groups].filter(([rule]) => rule !== SameNameRule.SameNameUnknown);
  return (known.length ? known : [...groups]).map(([rule, g]) => ({
    rule,
    who: g.codes.map((code) => agentName(s, code as Scope["agent"])).join(messages().model.listSeparator),
    pairs: g.pairs,
  }));
}

function globalAndProjectNote(s: Snapshot, key: ScopeKey, folder: string): Note | undefined {
  const m = messages().model;
  const scope = parseScope(key);
  // 合成一处的几个 Agent 都按自己的规则算(代表没有已知规则时，别的 Agent 的规则不能丢)。
  const agents = mergedAgents(s, scope.agent, placeOf(scope));
  const pairs = pairsOf(s, PairKind.SameNameGlobalAndProject);
  if (scope.project_id) {
    const here = pairs.filter((p) => agents.includes(p.second.scope.agent) && p.second.scope.project_id === scope.project_id && p.second.folder === folder);
    if (here.length === 0) return undefined;
    const kinds = [...new Set(here.map((p) => sameNameKind(s, p.first)))];
    const name = here[0].name;
    const text = byRule(s, here, (p) => p.second.scope.agent).map(({ rule, who }) =>
      ({
        [SameNameRule.SameNameGlobalWins]: m.sameNameGlobalWins(name, kinds, who),
        [SameNameRule.SameNameBothShown]: m.sameNameGlobalBoth(name, kinds, who),
        [SameNameRule.SameNameUnknown]: m.sameNameGlobalUnknown(name, kinds),
      })[rule],
    );
    const folders = [...new Set(here.map((p) => p.first.folder))];
    return { text: text.join("\n"), title: folders.map((f) => m.sameNameGlobalFolder(shortPath(s, f))).join("\n") };
  }
  const here = pairs.filter((p) => agents.includes(p.first.scope.agent) && p.first.folder === folder);
  if (here.length === 0) return undefined;
  const name = here[0].name;
  const projects = [...new Set(here.map((p) => projectName(s, p.second.scope.project_id ?? 0)))];
  const text = byRule(s, here, (p) => p.first.scope.agent).map(({ rule, who }) =>
    ({
      [SameNameRule.SameNameGlobalWins]: m.sameNameProjectWins(projects, name, who),
      [SameNameRule.SameNameBothShown]: m.sameNameProjectBoth(projects, name, who),
      [SameNameRule.SameNameUnknown]: m.sameNameProjectUnknown(projects, name),
    })[rule],
  );
  const lines = [...new Set(here.map((p) => m.sameNameProjectFolder(projectName(s, p.second.scope.project_id ?? 0), shortPath(s, p.second.folder))))];
  return { text: text.join("\n"), title: lines.join("\n") };
}

function twoDirectoriesNote(s: Snapshot, key: ScopeKey, folder: string): Note | undefined {
  const m = messages().model;
  const scope = parseScope(key);
  const agents = mergedAgents(s, scope.agent, placeOf(scope));
  const others = new Map<string, SameNameFolder>();
  let name = "";
  for (const p of pairsOf(s, PairKind.SameNameTwoDirectories)) {
    if (!agents.includes(p.first.scope.agent) || placeOf(p.first.scope) !== placeOf(scope)) continue;
    const other = p.first.folder === folder ? p.second : p.second.folder === folder ? p.first : undefined;
    if (!other) continue;
    others.set(other.folder, other);
    name = p.name;
  }
  if (!others.size) return undefined;
  const list = [...others.values()];
  return {
    text: m.sameNameTwoDirectories(name, [...new Set(list.map((o) => sameNameKind(s, o)))]),
    title: list.map((o) => m.sameNameOtherFolder(shortPath(s, o.folder))).join("\n"),
  };
}

// skillSameNameNote 是库里一个技能在各处的同名说明(技能库页、更新页的一行用):同一对只说一次，
// 写出是哪两处(各是库装的、和库里一样的还是不在技能库里的)和后果;text 与 title 相同，逐对一行。没有同名时返回 undefined。
// 合成一处的几个 Agent 写在一起(名字写那几个),说一次。
export function skillSameNameNote(s: Snapshot, skillID: SkillID): Note | undefined {
  const m = messages().model;
  const mine = (p: SameNameSkill) => p.first.skill_id === skillID || p.second.skill_id === skillID;
  // 同一对文件夹在合成一处的几个 Agent 上各有一对：按各自的规则分组写，主语只写持有这条规则的 Agent,不写出互相矛盾的两句。
  const byFolders = new Map<string, SameNameSkill[]>();
  for (const p of pairsOf(s, PairKind.SameNameGlobalAndProject).filter(mine)) {
    const key = `${p.first.folder}\n${p.second.folder}`;
    byFolders.set(key, [...(byFolders.get(key) ?? []), p]);
  }
  const lines = [...byFolders.values()].flatMap((same) =>
    byRule(s, same, (p) => p.second.scope.agent).map(({ rule, who, pairs: [p] }) => {
      const say = {
        [SameNameRule.SameNameGlobalWins]: m.sameNamePairWins,
        [SameNameRule.SameNameBothShown]: m.sameNamePairBoth,
        [SameNameRule.SameNameUnknown]: m.sameNamePairUnknown,
      }[rule];
      return say(who, projectName(s, p.second.scope.project_id ?? 0), p.name, sameNameKind(s, p.first), sameNameKind(s, p.second));
    }),
  );
  const twoDirectories = pairsOf(s, PairKind.SameNameTwoDirectories)
    .filter(mine)
    .map((p) => m.sameNamePairTwoDirectories(scopeLabel(s, p.first.scope), p.name, sameNameKind(s, p.first), sameNameKind(s, p.second)));
  const text = [...new Set([...lines, ...twoDirectories])].join("\n");
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

// twinNote 是库里一个同名技能哪来的、装在哪，供各处的同名提示分辨是哪一个。
// placed 是这次写操作刚装上的几处(写之前的快照里还没有),一起算进装在哪。
export function twinNote(s: Snapshot, skill: Skill, placed: readonly Scope[] = []): string {
  return messages().actions.twin(sourceLabel(s, skill.source), installedWhere(s, skill.id, placed));
}

// sameNameFetchNote 说明入库一个和库里技能同名的技能会怎样：不带来源时和库里所有技能比，带来源时只和这个来源里同名的比。
export function sameNameFetchNote(name: string, sourced: boolean): string {
  return sourced ? messages().model.sameNameFetchSourced(name) : messages().model.sameNameFetchUnsourced(name);
}
