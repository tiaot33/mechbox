import { describe, expect, test } from "vitest";
import { cn } from "./utils";

describe("cn", () => {
  test("自定义字号和文字颜色并存", () => {
    expect(cn("text-caption text-muted-foreground")).toBe("text-caption text-muted-foreground");
    expect(cn("text-body-long", "text-foreground-secondary")).toBe("text-body-long text-foreground-secondary");
    expect(cn("text-small", "text-foreground-secondary")).toBe("text-small text-foreground-secondary");
  });

  test("后给的字号、圆角、颜色压过前面的", () => {
    expect(cn("text-body", "text-caption")).toBe("text-caption");
    expect(cn("text-body", "text-small")).toBe("text-small");
    expect(cn("rounded-control", "rounded-card")).toBe("rounded-card");
    expect(cn("text-foreground", "text-subtle")).toBe("text-subtle");
  });
});
