// shadcn/ui 的 ContextMenu(右键菜单),按本应用改过：表面与弹层一致(毛玻璃),
// 选项高亮用悬停的灰底(子菜单开着时也是),破坏性的选项(移除)和别的选项一样;只保留用到的部分(选项、分隔线、子菜单)。
import type { ComponentProps } from "react";
import { ContextMenu as ContextMenuPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";

const surface =
  "z-50 min-w-[160px] animate-pop-in glass rounded-card border p-1 outline-none [--glass-drop:var(--shadow-popover)]";

const itemClass =
  "flex h-[26px] cursor-default items-center gap-2 rounded-control px-2 text-body outline-none select-none data-[disabled]:text-subtle data-[highlighted]:bg-fill data-[state=open]:bg-fill";

const separatorClass = "mx-1 my-1 h-px bg-row-line";

const subSurface = cn(surface, "max-h-72 overflow-y-auto");

// SubChevron 是子菜单那一项右边的箭头。
function SubChevron() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" className="ml-auto size-2.5 text-muted-foreground" aria-hidden>
      <polyline points="9 18 15 12 9 6" />
    </svg>
  );
}

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

function ContextMenuItem({ className, ...props }: ComponentProps<typeof ContextMenuPrimitive.Item>) {
  return (
    <ContextMenuPrimitive.Item
      data-slot="context-menu-item"
      className={cn(itemClass, className)}
      {...props}
    />
  );
}

function ContextMenuSeparator({ className, ...props }: ComponentProps<typeof ContextMenuPrimitive.Separator>) {
  return <ContextMenuPrimitive.Separator data-slot="context-menu-separator" className={cn(separatorClass, className)} {...props} />;
}

function ContextMenuSub(props: ComponentProps<typeof ContextMenuPrimitive.Sub>) {
  return <ContextMenuPrimitive.Sub data-slot="context-menu-sub" {...props} />;
}

function ContextMenuSubTrigger({ className, children, ...props }: ComponentProps<typeof ContextMenuPrimitive.SubTrigger>) {
  return (
    <ContextMenuPrimitive.SubTrigger data-slot="context-menu-sub-trigger" className={cn(itemClass, className)} {...props}>
      {children}
      <SubChevron />
    </ContextMenuPrimitive.SubTrigger>
  );
}

function ContextMenuSubContent({ className, ...props }: ComponentProps<typeof ContextMenuPrimitive.SubContent>) {
  return (
    <ContextMenuPrimitive.Portal>
      <ContextMenuPrimitive.SubContent data-slot="context-menu-sub-content" collisionPadding={8} className={cn(subSurface, className)} {...props} />
    </ContextMenuPrimitive.Portal>
  );
}

// 菜单的表面、选项、分隔线、子菜单的样式与箭头，下拉菜单(dropdown-menu.tsx)共用。
export {
  surface as menuSurface,
  itemClass as menuItemClass,
  separatorClass as menuSeparatorClass,
  subSurface as menuSubSurface,
  SubChevron as MenuSubChevron,
};

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
