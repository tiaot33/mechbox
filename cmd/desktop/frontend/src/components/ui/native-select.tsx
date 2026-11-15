// 原生下拉框：选项由系统菜单弹出，和 macOS 的其他应用一致，只给框本身套上主题样式。
// SelectButton 是外观相同的按钮，给原生下拉框做不到的选择(如可勾选几项的弹层)当触发按钮，和下拉框排在一行时两个框一样。
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

const selectBox =
  "min-w-0 rounded-control border border-border bg-panel px-2 text-body text-foreground outline-none focus-visible:border-primary focus-visible:ring-3 focus-visible:ring-primary-soft disabled:border-border disabled:text-subtle";

function NativeSelect({ className, ...props }: ComponentProps<"select">) {
  return <select data-slot="native-select" className={cn(selectBox, "h-7 max-w-[150px]", className)} {...props} />;
}

function SelectButton({ className, type = "button", ...props }: ComponentProps<"button">) {
  return (
    <button
      data-slot="select-button"
      type={type}
      className={cn(selectBox, "inline-flex min-h-7 max-w-[260px] items-center gap-1 py-[3px] text-left", className)}
      {...props}
    />
  );
}

export { NativeSelect, SelectButton };
