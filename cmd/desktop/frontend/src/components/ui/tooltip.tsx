// shadcn/ui 的 Tooltip,按本应用改过：表面与弹层一致(半透明白色加 backdrop-blur),深色文字;没有箭头。
// 原生的 title 在可拖动的行上不出现，要悬停看全文的地方用它。TooltipProvider 放在 App 最外层。
import type { ComponentProps } from "react";
import { Tooltip as TooltipPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";

function TooltipProvider({ delayDuration = 500, ...props }: ComponentProps<typeof TooltipPrimitive.Provider>) {
  return <TooltipPrimitive.Provider data-slot="tooltip-provider" delayDuration={delayDuration} {...props} />;
}

function Tooltip(props: ComponentProps<typeof TooltipPrimitive.Root>) {
  return <TooltipPrimitive.Root data-slot="tooltip" {...props} />;
}

function TooltipTrigger(props: ComponentProps<typeof TooltipPrimitive.Trigger>) {
  return <TooltipPrimitive.Trigger data-slot="tooltip-trigger" {...props} />;
}

function TooltipContent({ className, sideOffset = 6, ...props }: ComponentProps<typeof TooltipPrimitive.Content>) {
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        data-slot="tooltip-content"
        sideOffset={sideOffset}
        collisionPadding={8}
        className={cn(
          "z-50 max-w-[360px] animate-pop-in rounded-lg border border-border bg-glass-overlay px-2.5 py-1.5 text-[11.5px] leading-[1.45] whitespace-pre-line text-foreground shadow-popover backdrop-blur-xl backdrop-saturate-150 select-text",
          className,
        )}
        {...props}
      />
    </TooltipPrimitive.Portal>
  );
}

export { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger };
