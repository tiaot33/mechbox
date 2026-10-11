// 可拖动调宽：左右两栏(ResizablePane)与技能详情抽屉共用。拖动柄拖动调整宽度，双击恢复默认宽度，
// 方向键每次调 16px。宽度只属于这台机器的界面，存在 localStorage,下次打开沿用。
import { useRef, useState, type ReactNode } from "react";
import { cn } from "@/lib/utils";

interface WidthOptions {
  storageKey: string;
  defaultWidth: number;
  min: number;
  max: number;
}

export function useStoredWidth({ storageKey, defaultWidth, min, max }: WidthOptions) {
  const clamp = (w: number) => Math.min(max, Math.max(min, Math.round(w)));
  const [width, setWidth] = useState(() => {
    const saved = Number(localStorage.getItem(storageKey));
    return saved ? clamp(saved) : defaultWidth;
  });
  const apply = (w: number) => {
    const next = clamp(w);
    setWidth(next);
    localStorage.setItem(storageKey, String(next));
  };
  return [width, apply] as const;
}

// ResizeHandle 是一条竖向拖动柄。edge 是它所在的那条边：在左边缘时往左拖变宽，在右边缘时往右拖变宽。
export function ResizeHandle({
  edge,
  width,
  onWidth,
  label,
  className,
  ...opts
}: WidthOptions & {
  edge: "left" | "right";
  width: number;
  onWidth: (w: number) => void;
  label: string;
  className?: string;
}) {
  const [dragging, setDragging] = useState(false);
  const start = useRef<{ x: number; width: number } | null>(null);
  const sign = edge === "right" ? 1 : -1;
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-valuemin={opts.min}
      aria-valuemax={opts.max}
      aria-valuenow={width}
      aria-label={label}
      tabIndex={0}
      title="拖动调整宽度，双击恢复默认"
      className={cn("group/handle absolute top-0 bottom-0 z-10 flex w-2 cursor-col-resize justify-center outline-none", className)}
      onPointerDown={(e) => {
        e.currentTarget.setPointerCapture(e.pointerId);
        start.current = { x: e.clientX, width };
        setDragging(true);
      }}
      onPointerMove={(e) => start.current && onWidth(start.current.width + sign * (e.clientX - start.current.x))}
      onPointerUp={() => {
        start.current = null;
        setDragging(false);
      }}
      onDoubleClick={() => onWidth(opts.defaultWidth)}
      onKeyDown={(e) => {
        if (e.key === "ArrowLeft") onWidth(width - sign * 16);
        else if (e.key === "ArrowRight") onWidth(width + sign * 16);
      }}
    >
      <span
        className={cn(
          "my-3 w-0.5 rounded-full bg-primary/50 opacity-0 transition-opacity group-hover/handle:opacity-100 group-focus-visible/handle:opacity-100",
          dragging && "opacity-100",
        )}
      />
    </div>
  );
}

// ResizablePane 包住左栏或右栏，拖动柄在靠中间底板的那一侧。
export function ResizablePane({ side, children, ...opts }: WidthOptions & { side: "left" | "right"; children: ReactNode }) {
  const [width, apply] = useStoredWidth(opts);
  return (
    <div className="relative isolate flex shrink-0 flex-col" style={{ width }}>
      {children}
      <ResizeHandle
        {...opts}
        edge={side === "left" ? "right" : "left"}
        width={width}
        onWidth={apply}
        label={side === "left" ? "调整侧栏宽度" : "调整作用域栏宽度"}
        className={side === "left" ? "-right-2" : "-left-2"}
      />
    </div>
  );
}
