import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

// 主题里的字号(text-title、text-body 等)与圆角(rounded-control 等)是自定义的名字，要登记给 tailwind-merge:
// 不登记时 text-caption 被当成文字颜色，和 text-muted-foreground 互相吞掉。
const twMerge = extendTailwindMerge({
  extend: {
    theme: {
      text: ["title", "heading", "body", "body-long", "small", "caption"],
      radius: ["control", "card", "tile", "pill"],
    },
  },
});

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function toggled<T>(set: ReadonlySet<T>, value: T): Set<T> {
  const next = new Set(set);
  if (!next.delete(value)) next.add(value);
  return next;
}
