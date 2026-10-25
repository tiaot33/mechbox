// 转圈：状态圈的 running(转动的弧),颜色跟随文字。
import type { ComponentProps } from "react";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import { StatusMark } from "./status-mark";

function Spinner({ className, ...props }: ComponentProps<"span">) {
  const t = useT();
  return (
    <span
      data-slot="spinner"
      role="status"
      aria-label={t.common.working}
      className={cn("inline-flex size-[11px] shrink-0", className)}
      {...props}
    >
      <StatusMark status="running" size={11} strokeWidth={3} />
    </span>
  );
}

export { Spinner };
