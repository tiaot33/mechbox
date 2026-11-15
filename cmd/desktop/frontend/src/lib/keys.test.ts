import { describe, expect, it, vi } from "vitest";
import { pressKeys } from "./keys";

const row = {};
const key = (k: string, target: unknown = row) => ({ key: k, target, currentTarget: row, preventDefault: vi.fn() });

describe("pressKeys", () => {
  it("焦点在行上按 Enter 或空格时打开", () => {
    const open = vi.fn();
    const enter = key("Enter");
    pressKeys(open)(enter);
    pressKeys(open)(key(" "));
    expect(open).toHaveBeenCalledTimes(2);
    expect(enter.preventDefault).toHaveBeenCalled();
  });

  it("别的键不打开", () => {
    const open = vi.fn();
    pressKeys(open)(key("a"));
    pressKeys(open)(key("Tab"));
    expect(open).not.toHaveBeenCalled();
  });

  it("行里的按钮上按键交给按钮自己", () => {
    const open = vi.fn();
    pressKeys(open)(key("Enter", {}));
    expect(open).not.toHaveBeenCalled();
  });
});
