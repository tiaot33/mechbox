import { describe, expect, it } from "vitest";
import { byLevel, worstLevel, type Level } from "./level";

describe("worstLevel", () => {
  it("取几条里最重的一级", () => {
    expect(worstLevel(["info", "warn"])).toBe("warn");
    expect(worstLevel(["warn", "critical", "info"])).toBe("critical");
    expect(worstLevel(["info"])).toBe("info");
  });

  it("只有平常的信息或什么都没有时没有级别", () => {
    expect(worstLevel(["plain", undefined])).toBeUndefined();
    expect(worstLevel([])).toBeUndefined();
  });
});

describe("byLevel", () => {
  it("从重到轻排", () => {
    const list: Level[] = ["info", "critical", "warn", "info"];
    expect(list.sort(byLevel)).toEqual(["critical", "warn", "info", "info"]);
  });
});
