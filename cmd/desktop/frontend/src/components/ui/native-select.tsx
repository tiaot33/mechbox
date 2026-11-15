// 原生下拉框：选项由系统菜单弹出，和 macOS 的其他应用一致，只给框本身套上主题样式。
// 外观和普通按钮一样(macOS 的弹出按钮也是这样):没有边框的灰底，和按钮同高、同圆角。
// SelectButton 是外观相同的按钮，给原生下拉框做不到的选择(如可勾选几项的弹层)当触发按钮，和下拉框排在一行时两个框一样。
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

const selectBox =
  "min-w-0 rounded-control bg-control px-2.5 text-body text-foreground outline-none hover:bg-control-hover focus-visible:ring-3 focus-visible:ring-primary-soft disabled:text-subtle disabled:hover:bg-control";

function NativeSelect({ className, ...props }: ComponentProps<"select">) {
  return <select data-slot="native-select" className={cn(selectBox, "h-6 max-w-[150px]", className)} {...props} />;
}

function SelectButton({ className, type = "button", ...props }: ComponentProps<"button">) {
  return (
    <button
      data-slot="select-button"
      type={type}
      className={cn(selectBox, "inline-flex min-h-6 max-w-[260px] items-center gap-1 py-[2px] text-left", className)}
      {...props}
    />
  );
}

export { NativeSelect, SelectButton };
