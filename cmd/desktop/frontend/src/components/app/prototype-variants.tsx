// PROTOTYPE(一次性，不进 main):让刚下载的用户看懂「技能库 / 安装 / 未纳管」的三种界面，用底部的切换条或 ← → 切换。
// A 现在:main 原样(#279 之后)。
// B 按地方分块：左栏叫「技能库」;作用域页的块标题用"在哪"来说;未纳管行首是「入库」按钮，入库后那一行在它的集合里闪一下;
//   入库完成提示写清停在库里，给「装到…」。
// C 每行写在哪：左栏叫「技能库」;作用域页每行最后一列写两个地方「技能库 ✓/✗ · 这里 ✓/✗」,摘要里给图例。
import { useEffect, useSyncExternalStore } from "react";

export type Variant = "A" | "B" | "C";
const variants: { key: Variant; name: string }[] = [
  { key: "A", name: "现在" },
  { key: "B", name: "按地方分块" },
  { key: "C", name: "每行写在哪" },
];

const listeners = new Set<() => void>();
const read = (): Variant => {
  const v = new URLSearchParams(window.location.search).get("variant");
  return v === "B" || v === "C" ? v : "A";
};
let current: Variant = read();

function setVariant(v: Variant) {
  const url = new URL(window.location.href);
  url.searchParams.set("variant", v);
  window.history.replaceState(null, "", url);
  current = v;
  listeners.forEach((l) => l());
}

export function useVariant(): Variant {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => current,
  );
}

export const variantNow = () => current;

export function PrototypeSwitcher() {
  const v = useVariant();
  const i = variants.findIndex((x) => x.key === v);
  const go = (d: number) => setVariant(variants[(i + d + variants.length) % variants.length].key);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement | null;
      if (el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.isContentEditable)) return;
      if (e.key === "ArrowLeft") go(-1);
      if (e.key === "ArrowRight") go(1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
  if (!import.meta.env.DEV) return null;
  return (
    <div className="fixed bottom-4 left-1/2 z-[9999] flex -translate-x-1/2 items-center gap-1 rounded-full bg-neutral-900 px-1.5 py-1 text-[12px] text-white shadow-[0_6px_24px_rgba(0,0,0,0.35)]">
      <button type="button" className="rounded-full px-2.5 py-1 hover:bg-white/15" onClick={() => go(-1)}>
        ←
      </button>
      <span className="min-w-36 text-center font-medium">
        原型 {v} · {variants[i].name}
      </span>
      <button type="button" className="rounded-full px-2.5 py-1 hover:bg-white/15" onClick={() => go(1)}>
        →
      </button>
    </div>
  );
}
