import { describe, expect, test } from "vitest";
import { Appearance } from "@bindings/cmd/desktop";
import { themeOf } from "./theme";

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
