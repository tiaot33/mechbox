// 一个技能在来源那边的情况：是哪一种、要不要处理;各处(技能行、节标题、更新页、左栏)读的是同一个结论。
import { describe, expect, it } from "vitest";
import { SourceType, UpdateCheckStatus, type Skill } from "@bindings/internal/library";
import { sourceChanges, sourceState, todoSkills } from "./source-state";

type Check = Partial<NonNullable<Skill["last_update_check"]>>;
const tracked = (check: Check | undefined, extra: Partial<Skill> = {}) =>
  ({ id: 1, name: "foo", source: { type: SourceType.SourceGit, label: "git.dev.test/a/skills" }, last_update_check: check, ...extra }) as Skill;
const local = (check: Check, extra: Partial<Skill> = {}) =>
  ({ id: 1, name: "foo", source: { type: SourceType.SourceLocal, label: "/home/code/x" }, last_update_check: { status: UpdateCheckStatus.UpdateAvailable, ...check }, ...extra }) as Skill;

describe("sourceState", () => {
  it("没检查过、已是最新的没有变化", () => {
    expect(sourceState(tracked(undefined))).toMatchObject({ kind: "none", todo: false, changed: false, canPlan: false });
    expect(sourceState(tracked({ status: UpdateCheckStatus.UpdateCurrent })).kind).toBe("none");
  });

  it("有新版本、推测出改名而没跳过的才算有变化，和集合标题、更新页一致", () => {
    expect(sourceState(tracked({ status: UpdateCheckStatus.UpdateAvailable, pending: true }))).toMatchObject({ kind: "newVersion", todo: true, changed: true });
    expect(sourceState(tracked({ status: UpdateCheckStatus.UpdateDeleted, renamed_to: "bar", pending: true }))).toMatchObject({
      kind: "renamed",
      renamedTo: "bar",
      changed: true,
    });
  });

  it("跳过的、回滚掉的不算有变化，但照样能从它打开更新计划", () => {
    expect(sourceState(tracked({ status: UpdateCheckStatus.UpdateAvailable, skipped: true }))).toMatchObject({
      kind: "newVersion",
      todo: false,
      changed: false,
      canPlan: true,
      skipped: true,
    });
    expect(sourceState(tracked({ status: UpdateCheckStatus.UpdateDeleted, renamed_to: "bar", skipped: true }))).toMatchObject({ changed: false, canPlan: true });
  });

  it("来源里已删除的不算有变化;选了继续保留的不再是待办，只留标记", () => {
    expect(sourceState(tracked({ status: UpdateCheckStatus.UpdateDeleted, pending: true }))).toMatchObject({ kind: "deleted", todo: true, changed: false, kept: false });
    expect(sourceState(tracked({ status: UpdateCheckStatus.UpdateDeleted }, { kept_deleted: true }))).toMatchObject({ kind: "deleted", todo: false, kept: true });
  });

  it("推测出改名的不算来源里已删除", () => {
    expect(sourceState(tracked({ status: UpdateCheckStatus.UpdateDeleted, renamed_to: "bar", pending: true })).kind).toBe("renamed");
  });

  it("有新版本而库里的当前版本带着用户的修改：更新会换掉它", () => {
    expect(sourceState(tracked({ status: UpdateCheckStatus.UpdateAvailable, pending: true }, { edited: true })).replacesEdits).toBe(true);
    expect(sourceState(tracked({ status: UpdateCheckStatus.UpdateAvailable, pending: true })).replacesEdits).toBe(false);
    expect(sourceState(tracked({ status: UpdateCheckStatus.UpdateCurrent }, { edited: true })).replacesEdits).toBe(false);
  });

  it("本地目录来源目录里改过时标出来，不算有新版本、不计入角标", () => {
    expect(sourceState(local({}))).toMatchObject({ kind: "localChanged", todo: false, changed: false, canPlan: false, userAuthored: true });
  });

  it("本地目录来源回滚掉的(跳过)、库里有用户修改的不标：重新读取会换掉它们", () => {
    expect(sourceState(local({ skipped: true })).kind).toBe("none");
    expect(sourceState(local({}, { edited: true })).kind).toBe("none");
  });

  it("作者就是用户自己的是本地目录来源和没有来源的", () => {
    expect(sourceState(tracked(undefined)).userAuthored).toBe(false);
    expect(sourceState({ id: 1, name: "foo", source: { type: SourceType.$zero, label: "" } } as Skill).userAuthored).toBe(true);
  });
});

describe("sourceChanges", () => {
  const skills = [
    tracked({ status: UpdateCheckStatus.UpdateAvailable, pending: true }, { id: 1 }),
    tracked({ status: UpdateCheckStatus.UpdateAvailable, skipped: true }, { id: 2 }),
    tracked({ status: UpdateCheckStatus.UpdateDeleted, renamed_to: "bar", pending: true }, { id: 3 }),
    tracked({ status: UpdateCheckStatus.UpdateDeleted, pending: true }, { id: 4 }),
    tracked({ status: UpdateCheckStatus.UpdateDeleted }, { id: 5, kept_deleted: true }),
    tracked({ status: UpdateCheckStatus.UpdateCurrent }, { id: 6 }),
    local({}, { id: 7 }),
  ];
  const ids = (list: Skill[]) => list.map((x) => x.id);

  it("一组技能里要处理的各有哪些：跳过的、继续保留的都不算", () => {
    const found = sourceChanges(skills);
    expect(ids(found.todo)).toEqual([1, 3, 4]);
    expect(ids(found.changed)).toEqual([1, 3]);
    expect(ids(found.newVersion)).toEqual([1]);
    expect(ids(found.renamed)).toEqual([3]);
    expect(ids(found.deleted)).toEqual([4]);
    expect(ids(found.localChanged)).toEqual([7]);
  });

  it("左栏的更新数和更新页列出的是计入角标的技能", () => {
    expect(ids(todoSkills({ skills }))).toEqual([1, 3, 4]);
  });
});
