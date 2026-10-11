// shadcn/ui 的 Switch,尺寸按原型改小。held:关着但被组安装留着(直接安装撤了，组安装还在),描一圈紫色。
import type { ComponentProps } from "react";
import { Switch as SwitchPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";

function Switch({ className, held = false, ...props }: ComponentProps<typeof SwitchPrimitive.Root> & { held?: boolean }) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        "relative inline-flex h-[17px] w-[30px] shrink-0 items-center rounded-full bg-border-strong outline-none transition-colors focus-visible:ring-3 focus-visible:ring-primary-soft disabled:opacity-45 data-[state=checked]:bg-primary",
        held && "shadow-[0_0_0_2px_var(--color-group-soft),inset_0_0_0_1.5px_var(--color-group)]",
        className,
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb className="pointer-events-none block size-[13px] translate-x-[2px] rounded-full bg-white shadow-[0_1px_2px_rgba(0,0,0,0.2)] transition-transform data-[state=checked]:translate-x-[15px]" />
    </SwitchPrimitive.Root>
  );
}

export { Switch };
