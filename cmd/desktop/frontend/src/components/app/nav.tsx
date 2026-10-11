// 侧栏导航行的样式(左栏筛选、右栏作用域树、设置页的分区目录共用),以及侧栏里的小计数。
// 类名 nav-item 只是测试钩子(e2e 按它找行),不挂样式。
import { cva } from "class-variance-authority";
import { cn } from "@/lib/utils";
import { Count } from "./parts";

export const navItem = cva(
  "nav-item group flex w-full min-w-0 items-center text-left text-foreground-secondary transition-colors hover:bg-black/[0.04] hover:text-foreground disabled:text-subtle disabled:hover:bg-transparent [&_[data-slot=icon]]:text-muted-foreground data-[active=true]:[&_[data-slot=icon]]:text-foreground disabled:[&_[data-slot=icon]]:text-subtle",
  {
    variants: {
      kind: {
        // 左栏顶层：所有技能、更新、检查更新、同步、设置;设置页的分区目录
        main: "h-[30px] gap-2 rounded-md px-2 text-[12.5px] data-[active=true]:bg-[rgba(17,24,39,0.07)] data-[active=true]:font-semibold data-[active=true]:text-foreground",
        // 左栏分节里的集合与组
        section:
          "h-7 gap-2 rounded-md pr-1.5 pl-2 text-[12.5px] data-[active=true]:bg-primary-soft data-[active=true]:font-semibold data-[active=true]:text-primary-strong",
        // 右栏：地方下的 Agent 与项目
        tree: "h-7 gap-2 rounded-md pr-1.5 pl-2 text-[12.5px] data-[active=true]:bg-primary-soft data-[active=true]:font-semibold data-[active=true]:text-primary-strong",
        // 右栏项目下的 Agent
        nested: "h-[26px] gap-2 rounded-md pr-1.5 pl-2 text-xs data-[active=true]:bg-primary-soft data-[active=true]:font-semibold data-[active=true]:text-primary-strong",
      },
    },
    defaultVariants: { kind: "main" },
  },
);

// NavCount 是侧栏行尾的计数，比列表里的小一号;accent 的(左栏分节里)所在行选中时跟着变蓝。
export function NavCount({ value, small = false, accent = false }: { value: number; small?: boolean; accent?: boolean }) {
  return (
    <Count
      value={value}
      className={cn(
        "min-w-0 rounded px-1 py-px text-[9.5px] leading-[1.3]",
        small && "text-[9px]",
        accent && "group-data-[active=true]:bg-[rgba(0,113,227,0.12)] group-data-[active=true]:text-primary-strong",
      )}
    />
  );
}
