import { describe, expect, it } from "vitest";
import { endEditing, startEditing } from "./editing";

describe("备注的编辑", () => {
  it("结束编辑只放掉自己", () => {
    const a = startEditing(1);
    expect(endEditing(a, a)).toBeNull();
  });

  it("旧技能的保存晚到时，不清掉新技能正在写的备注", () => {
    const a = startEditing(1);
    const b = startEditing(2);
    expect(endEditing(b, a)).toBe(b);
  });

  it("同一个技能先后两次编辑：前一次的保存晚到时，不关掉后一次", () => {
    const first = startEditing(1);
    const second = startEditing(1);
    expect(endEditing(second, first)).toBe(second);
  });
});
