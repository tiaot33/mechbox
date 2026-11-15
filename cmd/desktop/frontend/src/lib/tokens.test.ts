// docs/design-system/tokens.json 是 index.css 里主题变量的一份手抄：这里逐项比对，两边对不上就报出来，改一处要改另一处。
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

type Token = { name: string; value: string | { light: string; dark: string } };

const tokens = JSON.parse(readFileSync(new URL("../../../../../docs/design-system/tokens.json", import.meta.url), "utf8")) as Record<string, unknown>;
const css = readFileSync(new URL("../index.css", import.meta.url), "utf8");

// 这几组的名字和 index.css 的变量一一对应;spacing、size 两组是 Tailwind 的间距与写在组件里的尺寸，没有同名变量。
const groups = ["color", "type", "radius", "shadow", "glass"];
// 在 index.css 里没有单独一个变量的：毛玻璃的底色由 --glass-base 和 --glass-alpha 拼出来，glass-clear 的模糊写在工具类里。
const unmapped = ["glass", "glass-clear", "glass-blur-clear"];

function declarations(text: string): Map<string, string> {
  const out = new Map<string, string>();
  for (const m of text.matchAll(/--([\w-]+):\s*([^;]+);/g)) if (!out.has(m[1])) out.set(m[1], m[2]);
  return out;
}

const darkBlock = /:root\[data-theme="dark"\]\s*\{([^}]*)\}/g;
const light = declarations(css.replace(darkBlock, ""));
const dark = declarations([...css.matchAll(darkBlock)].map((m) => m[1]).join("\n"));

function resolve(value: string, theme: "light" | "dark"): string {
  const lookup = (name: string) => (theme === "dark" ? dark.get(name) : undefined) ?? light.get(name);
  let out = value;
  for (let i = 0; i < 5 && out.includes("var("); i++) out = out.replace(/var\(--([\w-]+)\)/g, (whole, name: string) => lookup(name) ?? whole);
  return out;
}

function normalize(value: string): string {
  return value
    .toLowerCase()
    .replace(/\s+/g, " ")
    .replace(/rgb\((\d+) (\d+) (\d+) \/ ([\d.]+)\)/g, "rgba($1, $2, $3, $4)")
    .replace(/#([0-9a-f])([0-9a-f])([0-9a-f])\b/g, "#$1$1$2$2$3$3")
    .trim();
}

function collect(node: unknown, out: Token[] = []): Token[] {
  if (Array.isArray(node)) node.forEach((x) => collect(x, out));
  else if (node && typeof node === "object") {
    const o = node as Record<string, unknown>;
    if (typeof o.name === "string" && "value" in o) out.push(o as Token);
    else Object.values(o).forEach((x) => collect(x, out));
  }
  return out;
}

const variableOf = (name: string) => [name, `color-${name}`, `text-${name}`].find((x) => light.has(x));

describe("docs/design-system/tokens.json 和 index.css", () => {
  const all = groups.flatMap((g) => collect(tokens[g]));

  it("每一项都在 index.css 里有同名的变量", () => {
    expect(all.filter((t) => !variableOf(t.name)).map((t) => t.name)).toEqual(unmapped);
  });

  it.each(all.filter((t) => variableOf(t.name)).map((t) => [t.name, t] as const))("%s 的值一致", (_, token) => {
    const variable = variableOf(token.name)!;
    const want = typeof token.value === "string" ? { light: token.value, dark: token.value } : token.value;
    for (const theme of ["light", "dark"] as const) {
      const declared = (theme === "dark" ? dark.get(variable) : undefined) ?? light.get(variable)!;
      expect(normalize(resolve(declared, theme)), `${theme}`).toBe(normalize(want[theme]));
    }
  });
});
