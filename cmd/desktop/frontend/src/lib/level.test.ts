import { describe, expect, it } from "vitest";
import { byLevel, byUrgency, worstLevel, type Level, type Mark } from "./level";

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

  it("更新排在三级提醒后面，不盖住提醒", () => {
    const list: Mark[] = ["update", "info", "critical"];
    expect(list.sort(byLevel)).toEqual(["critical", "info", "update"]);
  });

  it("更新不算提醒的级别", () => {
    expect(worstLevel(["update", "plain"])).toBeUndefined();
  });
});

describe("byUrgency", () => {
  it("一行的状态先写严重、注意，再写能做的更新、可能改名，提示最后", () => {
    const list: Mark[] = ["info", "rename", "update", "warn", "critical"];
    expect(list.sort(byUrgency)).toEqual(["critical", "warn", "update", "rename", "info"]);
  });

  it("可能改名和有新版本是两种，都不算提醒的级别", () => {
    expect(worstLevel(["rename", "update"])).toBeUndefined();
  });
});
