// 表单零件：带标题的输入项、字段下的错误(主要档的字加警示图标，不用红色),以及灰色的说明文字。
import type { ComponentProps, ReactNode } from "react";
import { Icon } from "@/components/ui/icon";
import { cn } from "@/lib/utils";

function Field({ label, className, children, ...props }: ComponentProps<"label"> & { label?: ReactNode }) {
  return (
    <label data-slot="field" className={cn("flex min-w-0 flex-col gap-1", className)} {...props}>
      {label && <span className="text-body text-foreground-secondary">{label}</span>}
      {children}
    </label>
  );
}

function FieldError({ className, children, ...props }: ComponentProps<"div">) {
  return (
    <div data-slot="field-error" className={cn("flex gap-1 text-caption text-foreground", className)} {...props}>
      <Icon name="alert" className="size-3 shrink-0 translate-y-px" />
      <span className="min-w-0">{children}</span>
    </div>
  );
}

function Hint({ className, ...props }: ComponentProps<"p">) {
  return <p data-slot="hint" className={cn("text-caption text-muted-foreground", className)} {...props} />;
}

export { Field, FieldError, Hint };
