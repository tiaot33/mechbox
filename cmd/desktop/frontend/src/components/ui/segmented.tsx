// 分段选择(入库来源、检查间隔、列表 / 卡片):一组按钮，选中的一项是白底。按钮用 aria-pressed 标出选中。
// 白底是垫在按钮下面的一块，换选中项时用不回弹的弹簧滑到新的一项(第一次出现时不滑;系统要求减少动态效果时直接跳过去)。
import { motion, useReducedMotion } from "motion/react";
import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "@/lib/utils";

interface SegmentedOption<T> {
  value: T;
  label: ReactNode;
  title?: string;
}

function Segmented<T extends string | number>({
  value,
  options,
  onChange,
  disabled = false,
  iconOnly = false,
  className,
}: {
  value: T;
  options: SegmentedOption<T>[];
  onChange: (value: T) => void;
  disabled?: boolean;
  iconOnly?: boolean;
  className?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [thumb, setThumb] = useState<{ top: number; left: number; width: number; height: number } | null>(null);
  const reduce = useReducedMotion();
  const index = options.findIndex((o) => o.value === value);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => {
      const b = index < 0 ? null : (el.querySelectorAll<HTMLButtonElement>(":scope > button")[index] ?? null);
      if (!b || b.offsetWidth === 0) {
        setThumb(null);
        return;
      }
      setThumb({ top: b.offsetTop, left: b.offsetLeft, width: b.offsetWidth, height: b.offsetHeight });
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [index, options.length]);

  return (
    <div
      ref={ref}
      role="group"
      data-slot="segmented"
      className={cn("relative inline-flex items-center gap-px rounded-md border border-border bg-black/5 p-0.5", className)}
    >
      {thumb && (
        <motion.span
          aria-hidden
          initial={false}
          animate={{ x: thumb.left, width: thumb.width }}
          transition={reduce ? { duration: 0 } : { type: "spring", duration: 0.32, bounce: 0 }}
          style={{ top: thumb.top, height: thumb.height }}
          className="pointer-events-none absolute left-0 rounded bg-white shadow-[0_1px_2px_rgba(17,24,39,0.10),0_0_0_1px_rgba(17,24,39,0.04)]"
        />
      )}
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          title={o.title}
          aria-pressed={o.value === value}
          disabled={disabled}
          onClick={() => onChange(o.value)}
          className={cn(
            "relative h-[22px] rounded px-2.5 text-xs text-foreground-secondary transition-colors not-aria-pressed:hover:bg-black/5 hover:text-foreground disabled:text-subtle disabled:hover:bg-transparent aria-pressed:text-foreground",
            !thumb && "aria-pressed:bg-white aria-pressed:shadow-[0_1px_2px_rgba(17,24,39,0.10),0_0_0_1px_rgba(17,24,39,0.04)]",
            iconOnly && "grid h-5 w-7 place-items-center px-0 [&_[data-slot=icon]]:size-[13px]",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export { Segmented, type SegmentedOption };
