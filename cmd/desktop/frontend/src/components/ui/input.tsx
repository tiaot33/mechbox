// shadcn/ui 的 Input,以及带放大镜图标的搜索框(顶栏与技能市场)。
import type { ComponentProps, ReactNode } from "react";
import { cn } from "@/lib/utils";

function Input({ className, ...props }: ComponentProps<"input">) {
  return (
    <input
      data-slot="input"
      className={cn(
        "h-[30px] min-w-0 rounded-lg border border-border bg-white px-2.5 outline-none placeholder:text-subtle focus:border-primary-border focus:ring-3 focus:ring-primary-soft disabled:opacity-60",
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
        "flex h-7 items-center gap-1.5 rounded-md border border-border bg-white px-2 text-subtle transition-colors focus-within:border-primary",
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
