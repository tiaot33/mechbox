// 不透明的卡片(详情抽屉与设置页的分区):标题在左，说明或操作在右。
import type { ComponentProps, ReactNode } from "react";
import { cn } from "@/lib/utils";

function Card({ className, ...props }: ComponentProps<"section">) {
  return (
    <section data-slot="card" className={cn("rounded-card bg-panel px-4 py-3.5 text-body shadow-card", className)} {...props} />
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
