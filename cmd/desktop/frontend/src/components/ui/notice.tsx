// 提示条(对应 shadcn/ui 的 Alert):灰底，次要档的字，成段的行高。要提醒的(alert:有风险、失败)在前面加橙色的警示图标，不换底色，
// 首句用半粗的主要档文字;传入的是一段文字时自动按句拆出首句，自己排版的内容由调用方给首句加 font-semibold text-foreground。
import type { ComponentProps } from "react";
import { Icon } from "@/components/ui/icon";
import { splitLead } from "@/lib/lead";
import { cn } from "@/lib/utils";

function Notice({ className, alert = false, children, ...props }: ComponentProps<"div"> & { alert?: boolean }) {
  const [lead, rest] = alert && typeof children === "string" ? splitLead(children) : [null, null];
  return (
    <div
      data-slot="notice"
      data-alert={alert || undefined}
      className={cn("relative rounded-card bg-fill px-3 py-2 text-body-long text-foreground-secondary", alert && "pl-8", className)}
      {...props}
    >
      {alert && <Icon name="alert" className="absolute top-[11px] left-3 text-warn" />}
      {lead !== null ? (
        <>
          <span className="font-semibold text-foreground">{lead}</span>
          {rest}
        </>
      ) : (
        children
      )}
    </div>
  );
}

export { Notice };
