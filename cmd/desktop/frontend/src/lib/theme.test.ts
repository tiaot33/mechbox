import { describe, expect, test } from "vitest";
import { Appearance } from "@bindings/cmd/desktop";
import { glassTintOf, themeOf } from "./theme";

describe("themeOf", () => {
  test.each([
    [Appearance.AppearanceLight, false, "light"],
    [Appearance.AppearanceLight, true, "light"],
    [Appearance.AppearanceDark, false, "dark"],
    [Appearance.AppearanceDark, true, "dark"],
    [Appearance.AppearanceSystem, false, "light"],
    [Appearance.AppearanceSystem, true, "dark"],
    [undefined, true, "dark"],
  ])("appearance %o with system dark %o is %s", (appearance, systemDark, want) => {
    expect(themeOf(appearance, systemDark)).toBe(want);
  });
});

describe("glassTintOf", () => {
  const settings = { glass_tint: 0.4, glass_tint_dark: 1.2 };
  test("each appearance keeps its own tint", () => {
    expect(glassTintOf(settings, "light")).toBe(0.4);
    expect(glassTintOf(settings, "dark")).toBe(1.2);
  });
});
