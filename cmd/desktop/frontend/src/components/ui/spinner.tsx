import type { ComponentProps } from "react";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

function Spinner({ className, ...props }: ComponentProps<"span">) {
  const t = useT();
  return (
    <span
      data-slot="spinner"
      role="status"
      aria-label={t.layout.working}
      className={cn(
        "inline-block size-[11px] shrink-0 animate-spin rounded-full border-[1.5px] border-current border-r-transparent",
        className,
      )}
      {...props}
    />
  );
}

export { Spinner };
