// 弹窗里的白底列表：候选、作用域、结果逐行列出。行可以是可勾选的 label(clickable),选中时淡蓝底。
import type { ComponentProps } from "react";
import { Slot } from "radix-ui";
import { cn } from "@/lib/utils";

function ListBox({ className, scroll = false, ...props }: ComponentProps<"div"> & { scroll?: boolean }) {
  return (
    <div
      data-slot="list-box"
      className={cn(
        "overflow-hidden rounded-box border border-border-subtle bg-white",
        // 长列表自己滚，上下的操作行与诊断保持可见。
        scroll && "min-h-40 shrink! overflow-y-auto",
        className,
      )}
      {...props}
    />
  );
}

function ListRow({
  className,
  asChild = false,
  compact = false,
  clickable = false,
  selected = false,
  ...props
}: ComponentProps<"div"> & { asChild?: boolean; compact?: boolean; clickable?: boolean; selected?: boolean }) {
  const Comp = asChild ? Slot.Root : "div";
  return (
    <Comp
      data-slot="list-row"
      className={cn(
        "flex min-w-0 items-center gap-2 border-t border-border-subtle px-3 py-2 first:border-t-0",
        compact && "gap-1.5 px-2.5 py-0.5 leading-5",
        clickable && "cursor-pointer hover:bg-gray-50",
        selected && "bg-primary-soft hover:bg-primary-soft",
        className,
      )}
      {...props}
    />
  );
}

export { ListBox, ListRow };
