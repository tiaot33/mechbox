import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

function Spinner({ className, ...props }: ComponentProps<"span">) {
  return (
    <span
      data-slot="spinner"
      role="status"
      aria-label="正在处理…"
      className={cn(
        "inline-block size-[11px] shrink-0 animate-spin rounded-full border-[1.5px] border-current border-r-transparent",
        className,
      )}
      {...props}
    />
  );
}

export { Spinner };
