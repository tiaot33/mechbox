import { cn } from "@/lib/utils";

// SetRow 是设置页一组里的一行：一行一个设置，控件靠右。行高固定(40 px),每行只有一层;一组里的第一行不画分隔线。
export function SetRow({ className, children }: { className?: string; children: React.ReactNode }) {
  return (
    <div
      className={cn(
        "set-row flex h-10 min-w-0 items-center gap-3 border-t border-row-line text-body first:border-t-0",
        className,
      )}
    >
      {children}
    </div>
  );
}
