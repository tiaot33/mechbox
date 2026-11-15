// shadcn/ui 的 Input,以及带放大镜图标的搜索框(顶栏与技能市场)。
import type { ComponentProps, ReactNode } from "react";
import { cn } from "@/lib/utils";

function Input({ className, ...props }: ComponentProps<"input">) {
  return (
    <input
      data-slot="input"
      className={cn(
        "h-7 min-w-0 rounded-control border border-border bg-panel px-2.5 text-body outline-none placeholder:text-subtle focus:border-primary focus:ring-3 focus:ring-primary-soft disabled:opacity-60",
        className,
      )}
      {...props}
    />
  );
}

function SearchInput({
  className,
  icon,
  suffix,
  ...props
}: ComponentProps<"input"> & { icon: ReactNode; suffix?: ReactNode }) {
  return (
    <label
      data-slot="search"
      className={cn(
        "flex h-7 items-center gap-1.5 rounded-control border border-border bg-panel px-2 text-body text-subtle transition-colors focus-within:border-primary focus-within:ring-3 focus-within:ring-primary-soft",
        className,
      )}
    >
      {icon}
      <input className="min-w-0 flex-1 bg-transparent text-foreground outline-none placeholder:text-subtle" {...props} />
      {suffix}
    </label>
  );
}

export { Input, SearchInput };
