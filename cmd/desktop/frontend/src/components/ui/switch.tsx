// shadcn/ui 的 Switch,尺寸按原型改小。held:关着但被组安装留着(直接安装撤了，组安装还在),描一圈紫色;
// tone="group":开着时用组的紫色(作用域页上只随组安装的技能)。
import type { ComponentProps } from "react";
import { Switch as SwitchPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";

function Switch({
  className,
  held = false,
  tone,
  ...props
}: ComponentProps<typeof SwitchPrimitive.Root> & { held?: boolean; tone?: "group" }) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        "relative inline-flex h-[17px] w-[30px] shrink-0 items-center rounded-full bg-border-strong outline-none transition-colors focus-visible:ring-3 focus-visible:ring-primary-soft disabled:opacity-45 data-[state=checked]:bg-primary",
        held && "shadow-[0_0_0_2px_var(--color-group-soft),inset_0_0_0_1.5px_var(--color-group)]",
        tone === "group" && "data-[state=checked]:bg-group",
        className,
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb className="pointer-events-none block size-[13px] translate-x-[2px] rounded-full bg-white shadow-[0_1px_2px_rgba(0,0,0,0.2)] transition-transform data-[state=checked]:translate-x-[15px]" />
    </SwitchPrimitive.Root>
  );
}

// SwitchMark 是开着、但不能点的开关样子，只是记号不是控件：Agent 在这里用着这个技能，只是在这里改不了(项目作用域里来自全局的)。
function SwitchMark({ className, title }: { className?: string; title: string }) {
  return (
    <span
      role="img"
      aria-label={title}
      title={title}
      data-slot="switch-mark"
      className={cn("relative inline-flex h-[17px] w-[30px] shrink-0 items-center rounded-full bg-subtle/80", className)}
    >
      <span className="block size-[13px] translate-x-[15px] rounded-full bg-white shadow-[0_1px_2px_rgba(0,0,0,0.2)]" />
    </span>
  );
}

export { Switch, SwitchMark };
