// 分段选择(入库来源、检查间隔、列表 / 卡片):一组按钮，选中的一项是白底。按钮用 aria-pressed 标出选中。
import type { ReactNode } from "react";
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
  return (
    <div
      role="group"
      data-slot="segmented"
      className={cn("inline-flex items-center gap-px rounded-md border border-border bg-black/5 p-0.5", className)}
    >
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          title={o.title}
          aria-pressed={o.value === value}
          disabled={disabled}
          onClick={() => onChange(o.value)}
          className={cn(
            "h-[22px] rounded px-2.5 text-xs text-foreground-secondary transition-colors hover:bg-white/85 hover:text-foreground disabled:text-subtle disabled:hover:bg-transparent aria-pressed:bg-white aria-pressed:text-foreground aria-pressed:shadow-[0_1px_2px_rgba(17,24,39,0.10),0_0_0_1px_rgba(17,24,39,0.04)]",
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
