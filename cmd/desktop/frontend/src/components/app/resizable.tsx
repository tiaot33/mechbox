// ResizablePane 包住左栏或右栏：靠中间底板那一侧有一条拖动柄，拖动调整宽度，双击恢复默认宽度;
// 方向键每次调 16px。宽度只属于这台机器的界面，存在 localStorage,下次打开沿用。
import { useRef, useState, type ReactNode } from "react";
import { cn } from "@/lib/utils";

export function ResizablePane({
  side,
  storageKey,
  defaultWidth,
  min,
  max,
  children,
}: {
  side: "left" | "right";
  storageKey: string;
  defaultWidth: number;
  min: number;
  max: number;
  children: ReactNode;
}) {
  const clamp = (w: number) => Math.min(max, Math.max(min, Math.round(w)));
  const [width, setWidth] = useState(() => {
    const saved = Number(localStorage.getItem(storageKey));
    return saved ? clamp(saved) : defaultWidth;
  });
  const [dragging, setDragging] = useState(false);
  const start = useRef<{ x: number; width: number } | null>(null);

  const apply = (w: number) => {
    const next = clamp(w);
    setWidth(next);
    localStorage.setItem(storageKey, String(next));
  };
  // 左栏往右拖变宽，右栏往左拖变宽。
  const sign = side === "left" ? 1 : -1;

  return (
    <div className="relative isolate flex shrink-0 flex-col" style={{ width }}>
      {children}
      <div
        role="separator"
        aria-orientation="vertical"
        aria-valuemin={min}
        aria-valuemax={max}
        aria-valuenow={width}
        aria-label={side === "left" ? "调整侧栏宽度" : "调整作用域栏宽度"}
        tabIndex={0}
        title="拖动调整宽度，双击恢复默认"
        className={cn(
          "group/handle absolute top-0 bottom-0 z-10 flex w-2 cursor-col-resize justify-center outline-none",
          side === "left" ? "-right-2" : "-left-2",
        )}
        onPointerDown={(e) => {
          e.currentTarget.setPointerCapture(e.pointerId);
          start.current = { x: e.clientX, width };
          setDragging(true);
        }}
        onPointerMove={(e) => start.current && apply(start.current.width + sign * (e.clientX - start.current.x))}
        onPointerUp={() => {
          start.current = null;
          setDragging(false);
        }}
        onDoubleClick={() => apply(defaultWidth)}
        onKeyDown={(e) => {
          if (e.key === "ArrowLeft") apply(width - sign * 16);
          else if (e.key === "ArrowRight") apply(width + sign * 16);
        }}
      >
        <span
          className={cn(
            "my-3 w-0.5 rounded-full bg-primary/50 opacity-0 transition-opacity group-hover/handle:opacity-100 group-focus-visible/handle:opacity-100",
            dragging && "opacity-100",
          )}
        />
      </div>
    </div>
  );
}
