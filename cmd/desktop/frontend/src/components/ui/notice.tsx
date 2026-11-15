// 提示条(对应 shadcn/ui 的 Alert):灰底，次要档的字，成段的行高。要提醒的(alert:有风险、失败)在前面加「注意」一级的橙色三角图标，不换底色，
// 首句用半粗的主要档文字：传入的是文字(一段，或几段文字拼成的)时按句拆出首句;传入的是自己排版的元素时不拆。
// action 是排在右边的按钮(「重新生成」「查看」):文字占左边，按钮上下居中。
import { Children, type ComponentProps, type ReactNode } from "react";
import { Dot } from "@/components/ui/badge";
import { splitLead } from "@/lib/lead";
import { cn } from "@/lib/utils";

function Notice({ className, alert = false, action, children, ...props }: ComponentProps<"div"> & { alert?: boolean; action?: ReactNode }) {
  const parts = Children.toArray(children);
  const text = alert && parts.length > 0 && parts.every((x) => typeof x === "string" || typeof x === "number");
  const [lead, rest] = text ? splitLead(parts.join("")) : [null, null];
  const body =
    lead !== null ? (
      <>
        <span className="font-semibold text-foreground">{lead}</span>
        {rest}
      </>
    ) : (
      children
    );
  return (
    <div
      data-slot="notice"
      data-alert={alert || undefined}
      className={cn("relative rounded-card bg-fill px-3 py-2 text-body-long text-foreground-secondary", alert && "pl-8", action && "flex items-center gap-2", className)}
      {...props}
    >
      {alert && <Dot tone="warn" className="absolute top-[11px] left-3" />}
      {action ? <span className="min-w-0 grow">{body}</span> : body}
      {action}
    </div>
  );
}

export { Notice };
