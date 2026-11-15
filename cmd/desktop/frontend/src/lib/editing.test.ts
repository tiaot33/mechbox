import { describe, expect, it } from "vitest";
import { editingAfter } from "./editing";

describe("editingAfter", () => {
  it("开始编辑时记下这一处", () => {
    expect(editingAfter<number>(null, 1, true)).toBe(1);
    expect(editingAfter<number>(2, 1, true)).toBe(1);
  });

  it("结束编辑只放掉自己", () => {
    expect(editingAfter<number>(1, 1, false)).toBeNull();
  });

  it("旧技能的保存晚到时，不清掉新技能正在写的备注", () => {
    expect(editingAfter<number>(2, 1, false)).toBe(2);
  });
});
