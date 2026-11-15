// 分区(详情抽屉与设置页):不是浮起的卡片，没有底色、边框和投影，和上一个分区之间用一条线和间距分开;标题在左，说明或操作在右。
import type { ComponentProps, ReactNode } from "react";
import { cn } from "@/lib/utils";

function Card({ className, ...props }: ComponentProps<"section">) {
  return (
    <section data-slot="card" className={cn("mx-2 border-t border-row-line pt-4 pb-2 text-body first:border-t-0 first:pt-1", className)} {...props} />
  );
}

function CardHeader({ title, note, className }: { title: ReactNode; note?: ReactNode; className?: string }) {
  return (
    <div data-slot="card-header" className={cn("mb-2.5 flex items-baseline justify-between gap-3", className)}>
      <span data-slot="card-title" className="text-heading">
        {title}
      </span>
      {note && <span className="text-caption text-muted-foreground">{note}</span>}
    </div>
  );
}

export { Card, CardHeader };
