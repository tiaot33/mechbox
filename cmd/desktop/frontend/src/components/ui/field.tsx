// 表单零件：带标题的输入项、字段下的错误，以及灰色的说明文字。
import type { ComponentProps, ReactNode } from "react";
import { cn } from "@/lib/utils";

function Field({ label, className, children, ...props }: ComponentProps<"label"> & { label?: ReactNode }) {
  return (
    <label data-slot="field" className={cn("flex min-w-0 flex-col gap-1", className)} {...props}>
      {label && <span className="text-xs text-muted-foreground">{label}</span>}
      {children}
    </label>
  );
}

function FieldError({ className, ...props }: ComponentProps<"div">) {
  return <div data-slot="field-error" className={cn("text-[11.5px] text-bad", className)} {...props} />;
}

function Hint({ className, ...props }: ComponentProps<"p">) {
  return <p data-slot="hint" className={cn("text-[11.5px] text-muted-foreground", className)} {...props} />;
}

export { Field, FieldError, Hint };
