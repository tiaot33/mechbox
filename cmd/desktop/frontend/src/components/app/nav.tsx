// 侧栏导航行的样式(左栏筛选、右栏作用域树、设置页的分区目录共用),以及侧栏里的小计数。
// 各种行左右内边距相同(px-2):行尾的计数落在同一条右边线上，行首的图标或箭头落在同一列。
// 类名 nav-item 只是测试钩子(e2e 按它找行),不挂样式。
import { cva } from "class-variance-authority";
import { cn } from "@/lib/utils";
import { Count } from "./parts";

export const navItem = cva(
  "nav-item group flex h-7 w-full min-w-0 items-center gap-2 rounded-control px-2 text-left text-body text-foreground-secondary transition-colors hover:bg-fill hover:text-foreground disabled:text-subtle disabled:hover:bg-transparent data-[active=true]:font-semibold data-[active=true]:text-foreground [&_[data-slot=icon]]:text-muted-foreground data-[active=true]:[&_[data-slot=icon]]:text-foreground disabled:[&_[data-slot=icon]]:text-subtle",
  {
    variants: {
      kind: {
        // 选中是灰底：左栏顶层(技能库、更新、检查更新、同步、设置)和设置页的分区目录
        main: "data-[active=true]:bg-fill",
        // 选中是淡绿底：左栏分节里的集合与组，右栏的 Agent 与项目
        item: "data-[active=true]:bg-primary-soft",
      },
    },
    defaultVariants: { kind: "main" },
  },
);

// NavCount 是侧栏行尾的计数;accent 的(左栏分节里)所在行选中时数字跟着加深。
export function NavCount({ value, accent = false }: { value: number; accent?: boolean }) {
  return (
    <Count
      value={value}
      className={cn(
        "min-w-0 px-1 py-px",
        accent && "group-data-[active=true]:text-foreground",
      )}
    />
  );
}
