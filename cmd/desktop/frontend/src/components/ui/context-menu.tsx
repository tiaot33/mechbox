// shadcn/ui 的 ContextMenu(右键菜单),按本应用改过：表面与弹层一致(半透明白色加 backdrop-blur),
// 选项高亮用浅灰底，danger 的选项用红字;只保留用到的部分(选项、分隔线、子菜单)。
import type { ComponentProps } from "react";
import { ContextMenu as ContextMenuPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";

const surface =
  "z-50 min-w-[160px] animate-pop-in rounded-[10px] border border-border bg-glass-overlay p-1 shadow-popover outline-none backdrop-blur-xl backdrop-saturate-150";

const itemClass =
  "flex h-[26px] cursor-default items-center gap-2 rounded-md px-2 text-xs outline-none select-none data-[disabled]:text-subtle data-[highlighted]:bg-black/[0.06] data-[state=open]:bg-black/[0.06]";

function ContextMenu(props: ComponentProps<typeof ContextMenuPrimitive.Root>) {
  return <ContextMenuPrimitive.Root data-slot="context-menu" {...props} />;
}

function ContextMenuTrigger(props: ComponentProps<typeof ContextMenuPrimitive.Trigger>) {
  return <ContextMenuPrimitive.Trigger data-slot="context-menu-trigger" {...props} />;
}

function ContextMenuContent({ className, ...props }: ComponentProps<typeof ContextMenuPrimitive.Content>) {
  return (
    <ContextMenuPrimitive.Portal>
      <ContextMenuPrimitive.Content data-slot="context-menu-content" collisionPadding={8} className={cn(surface, className)} {...props} />
    </ContextMenuPrimitive.Portal>
  );
}

function ContextMenuItem({ className, danger = false, ...props }: ComponentProps<typeof ContextMenuPrimitive.Item> & { danger?: boolean }) {
  return (
    <ContextMenuPrimitive.Item
      data-slot="context-menu-item"
      className={cn(itemClass, danger && "text-bad-strong", className)}
      {...props}
    />
  );
}

function ContextMenuSeparator({ className, ...props }: ComponentProps<typeof ContextMenuPrimitive.Separator>) {
  return <ContextMenuPrimitive.Separator data-slot="context-menu-separator" className={cn("mx-1 my-1 h-px bg-hairline", className)} {...props} />;
}

function ContextMenuSub(props: ComponentProps<typeof ContextMenuPrimitive.Sub>) {
  return <ContextMenuPrimitive.Sub data-slot="context-menu-sub" {...props} />;
}

function ContextMenuSubTrigger({ className, children, ...props }: ComponentProps<typeof ContextMenuPrimitive.SubTrigger>) {
  return (
    <ContextMenuPrimitive.SubTrigger data-slot="context-menu-sub-trigger" className={cn(itemClass, className)} {...props}>
      {children}
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" className="ml-auto size-2.5 text-muted-foreground" aria-hidden>
        <polyline points="9 18 15 12 9 6" />
      </svg>
    </ContextMenuPrimitive.SubTrigger>
  );
}

function ContextMenuSubContent({ className, ...props }: ComponentProps<typeof ContextMenuPrimitive.SubContent>) {
  return (
    <ContextMenuPrimitive.Portal>
      <ContextMenuPrimitive.SubContent data-slot="context-menu-sub-content" collisionPadding={8} className={cn(surface, "max-h-72 overflow-y-auto", className)} {...props} />
    </ContextMenuPrimitive.Portal>
  );
}

export {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
};
