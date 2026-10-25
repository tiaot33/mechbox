// shadcn/ui 的 Textarea:多行输入，样式和 Input 一致。
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

function Textarea({ className, ...props }: ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "min-w-0 resize-y rounded-lg border border-border bg-white px-2.5 py-1.5 outline-none placeholder:text-subtle focus:border-primary-border focus:ring-3 focus:ring-primary-soft disabled:opacity-60",
        className,
      )}
      {...props}
    />
  );
}

export { Textarea };
