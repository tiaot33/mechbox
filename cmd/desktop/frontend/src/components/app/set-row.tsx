import { cn } from "@/lib/utils";

// SetRow 是设置卡片里的一行：一行一个设置，开关靠右;卡片标题下的第一行不画分隔线。
export function SetRow({ className, children }: { className?: string; children: React.ReactNode }) {
  return (
    <div
      className={cn(
        "set-row flex items-center gap-3 border-t border-border-subtle py-2 text-[12.5px] [[data-slot=card-header]+&]:border-t-0 [[data-slot=card-header]+&]:pt-0",
        className,
      )}
    >
      {children}
    </div>
  );
}
