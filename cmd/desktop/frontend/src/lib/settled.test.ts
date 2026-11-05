import { describe, expect, it } from "vitest";
import { settledFresh, SETTLED_HOLD } from "./settled";

describe("settledFresh", () => {
  const settled = { id: 1, failed: false, at: 10_000 };

  it("结束后停留时间之内画对勾或叉", () => {
    expect(settledFresh(settled, settled.at)).toBe(true);
    expect(settledFresh(settled, settled.at + SETTLED_HOLD - 1)).toBe(true);
  });

  it("早已结束的结果不再画：从设置页回到主界面时，新挂上的状态圈不补画之前那次", () => {
    expect(settledFresh(settled, settled.at + SETTLED_HOLD)).toBe(false);
    expect(settledFresh(settled, settled.at + 60_000)).toBe(false);
  });
});
