// 一个技能在来源那边的情况：有新版本、可能改名、来源里已删除、本地目录来源在目录里改过，以及用户对它说过什么(跳过、继续保留)。
// 只在 sourceState 这一处判断：技能行的状态、节标题的计数、更新页、技能详情和左栏的更新数都读它的结论，说的是同一回事。
// 要不要处理(todo)直接取技能库给的 UpdateCheck.pending,这里不另算:有新版本且没跳过、来源里已删除且没选继续保留、
// 推测出改名且没跳过才计入;回滚掉的改动也算跳过。作者就是用户自己的(本地目录来源、没有来源)不计入：
// 不进更新页，本地目录来源只有「重新读取」,没有来源的只能用目录里的修改更新。Dock 角标由 Go 端按同一个标记算。
// 按默认勾选会不会更新到它(「更新」「全部更新」直接做的那些)是默认勾选的规则，见 lib/consent 的 updatedByDefault。
import { UpdateCheckStatus, type Collection, type Skill } from "@bindings/internal/library";
import type { Snapshot } from "./library";
import { collectionSkills, isLocal, userAuthored } from "./source";

// SourceState 是 sourceState 的结论。kind 是来源那边怎么了，几种互斥(技能库只在来源里已删除时才推测改名):
// - newVersion:有新版本(本地目录来源不算，它的是 localChanged);
// - renamed:推测它在来源里改了名，新名字在 renamedTo;
// - deleted:来源里已删除，也没推测出改名;
// - localChanged:本地目录来源的技能在目录里改过了(读目录时比出来的，见 features/update/check 的 checkLocalDirectories)。
//   回滚掉的那份(跳过)不算，等目录里再改过才重新标出;库里的当前版本是用 Agent 目录里的修改更新进来的也不算——
//   那是用户特意留下的修改，「重新读取」会把它换回目录里的内容;
// - none:没有变化，或还没检查过。
export interface SourceState {
  kind: "none" | "newVersion" | "renamed" | "deleted" | "localChanged";
  renamedTo: string;
  // userAuthored 是作者就是用户自己：本地目录来源、没有来源。
  userAuthored: boolean;
  // todo 是计入角标：更新页列出它，左栏的更新数算上它。
  todo: boolean;
  // changed 是界面上说的「有变化」:有新版本或可能改名，并且计入角标。集合标题与技能详情里的「N 个有变化」、集合标题上的「更新」都按它。
  changed: boolean;
  // canPlan 是能从这个技能打开更新计划：有新版本或可能改名，跳过了的也算(更新计划里照样能勾上)。
  canPlan: boolean;
  // skipped 是用户跳过了这次改动。
  skipped: boolean;
  // kept 是来源里已删除、用户选了继续保留：只留淡色标记。
  kept: boolean;
  // replacesEdits 是有新版本、而库里的当前版本带着用户的修改：更新会换掉它，更新计划里默认不勾。
  replacesEdits: boolean;
}

export function sourceState(skill: Skill): SourceState {
  const check = skill.last_update_check;
  const renamedTo = check?.renamed_to ?? "";
  const available = check?.status === UpdateCheckStatus.UpdateAvailable;
  const local = isLocal(skill.source);
  const kind: SourceState["kind"] = renamedTo
    ? "renamed"
    : available && !local
      ? "newVersion"
      : available && !check?.skipped && !skill.edited
        ? "localChanged"
        : check?.status === UpdateCheckStatus.UpdateDeleted
          ? "deleted"
          : "none";
  const todo = !!check?.pending;
  const canPlan = kind === "newVersion" || kind === "renamed";
  return {
    kind,
    renamedTo,
    userAuthored: userAuthored(skill.source),
    todo,
    changed: todo && canPlan,
    canPlan,
    skipped: !!check?.skipped,
    kept: kind === "deleted" && !!skill.kept_deleted,
    replacesEdits: kind === "newVersion" && !!skill.edited,
  };
}

// SourceChanges 是一组技能(一个集合的，或一次检查更新读到的)里要处理的各有哪些：todo 是计入角标的全部;
// newVersion、renamed、deleted 是其中有新版本、可能改名、来源里已删除的;changed 是有变化的(newVersion 加 renamed)。
// localChanged 是本地目录来源在目录里改过的，不计入角标。
export interface SourceChanges {
  todo: Skill[];
  changed: Skill[];
  newVersion: Skill[];
  renamed: Skill[];
  deleted: Skill[];
  localChanged: Skill[];
}

export function sourceChanges(skills: Skill[]): SourceChanges {
  const states = skills.map((skill) => ({ skill, state: sourceState(skill) }));
  const pick = (want: (st: SourceState) => boolean) => states.filter((x) => want(x.state)).map((x) => x.skill);
  return {
    todo: pick((st) => st.todo),
    changed: pick((st) => st.changed),
    newVersion: pick((st) => st.todo && st.kind === "newVersion"),
    renamed: pick((st) => st.todo && st.kind === "renamed"),
    deleted: pick((st) => st.todo && st.kind === "deleted"),
    localChanged: pick((st) => st.kind === "localChanged"),
  };
}

// todoSkills 是库里计入角标的技能：更新页「要处理的技能」列出它们，左栏的更新数是它们的个数。
export function todoSkills(s: Pick<Snapshot, "skills">): Skill[] {
  return s.skills.filter((x) => sourceState(x).todo);
}

// updatableCollections 是有计入角标的技能的集合，和左栏同一个顺序。
export function updatableCollections(s: Snapshot): Collection[] {
  return s.collections.filter((c) => collectionSkills(s, c).some((x) => sourceState(x).todo));
}
