// 转圈：状态圈，颜色跟随文字。running 时是转动的弧，带 data-slot="spinner" 与"处理中"的读屏文字(e2e 等它消失);
// 换成 done / failed 时弧合成整圈、画出对勾 / 叉(同一个 Spinner 换 status 才有形变)。
import type { ComponentProps } from "react";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import { StatusMark, type MarkStatus } from "./status-mark";

function Spinner({ status = "running", className, ...props }: ComponentProps<"span"> & { status?: MarkStatus }) {
  const t = useT();
  const running = status === "running";
  return (
    <span
      data-slot={running ? "spinner" : undefined}
      role="status"
      aria-label={running ? t.common.working : undefined}
      className={cn("inline-flex size-[11px] shrink-0", className)}
      {...props}
    >
      <StatusMark status={status} size={11} strokeWidth={3} />
    </span>
  );
}

export { Spinner };
