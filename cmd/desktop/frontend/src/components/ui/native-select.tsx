// 原生下拉框：选项由系统菜单弹出，和 macOS 的其他应用一致，只给框本身套上主题样式。
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

function NativeSelect({ className, ...props }: ComponentProps<"select">) {
  return (
    <select
      data-slot="native-select"
      className={cn(
        "h-[26px] max-w-[150px] min-w-0 rounded-[7px] border border-border bg-white px-1.5 text-xs text-foreground outline-none focus-visible:ring-3 focus-visible:ring-primary-soft",
        className,
      )}
      {...props}
    />
  );
}

export { NativeSelect };
