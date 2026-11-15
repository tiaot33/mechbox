// 下拉菜单：点按钮弹出的菜单，表面与选项和右键菜单(context-menu.tsx)一样;只保留用到的部分(选项、分隔线)。
import type { ComponentProps } from "react";
import { DropdownMenu as DropdownMenuPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";
import { menuItemClass, menuSurface } from "./context-menu";

function DropdownMenu(props: ComponentProps<typeof DropdownMenuPrimitive.Root>) {
  return <DropdownMenuPrimitive.Root data-slot="dropdown-menu" modal={false} {...props} />;
}

function DropdownMenuTrigger(props: ComponentProps<typeof DropdownMenuPrimitive.Trigger>) {
  return <DropdownMenuPrimitive.Trigger data-slot="dropdown-menu-trigger" {...props} />;
}

function DropdownMenuContent({ className, align = "end", sideOffset = 4, ...props }: ComponentProps<typeof DropdownMenuPrimitive.Content>) {
  return (
    <DropdownMenuPrimitive.Portal>
      <DropdownMenuPrimitive.Content
        data-slot="dropdown-menu-content"
        align={align}
        sideOffset={sideOffset}
        collisionPadding={8}
        className={cn(menuSurface, className)}
        {...props}
      />
    </DropdownMenuPrimitive.Portal>
  );
}

function DropdownMenuItem({ className, danger = false, ...props }: ComponentProps<typeof DropdownMenuPrimitive.Item> & { danger?: boolean }) {
  return <DropdownMenuPrimitive.Item data-slot="dropdown-menu-item" className={cn(menuItemClass, danger && "text-bad-strong", className)} {...props} />;
}

function DropdownMenuSeparator({ className, ...props }: ComponentProps<typeof DropdownMenuPrimitive.Separator>) {
  return <DropdownMenuPrimitive.Separator data-slot="dropdown-menu-separator" className={cn("mx-1 my-1 h-px bg-hairline", className)} {...props} />;
}

export { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger };
