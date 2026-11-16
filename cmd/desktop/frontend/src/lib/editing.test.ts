import { describe, expect, it } from "vitest";
import { endEditing, startEditing } from "./editing";

describe("备注的编辑", () => {
  it("结束编辑只放掉自己", () => {
    const a = startEditing();
    expect(endEditing(a, a)).toBeNull();
  });

  it("前一次的保存晚到时，不关掉后来开始的那一次", () => {
    const first = startEditing();
    const second = startEditing();
    expect(endEditing(second, first)).toBe(second);
  });

  it("已经没有在编辑时，晚到的保存不会打开什么", () => {
    expect(endEditing(null, startEditing())).toBeNull();
  });
});
