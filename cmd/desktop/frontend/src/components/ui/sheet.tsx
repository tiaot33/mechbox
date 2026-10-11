// shadcn/ui 的 Sheet,按本应用改过：
// - 在原地渲染、绝对定位在主体区域里(不经 Portal 铺满窗口),顶栏不被遮罩盖住;
// - 非模态：模态会把整页设成不可点，顶栏的搜索、视图切换在抽屉开着时就用不了。
//   遮罩由 SheetOverlay 自己画，点遮罩或按 Esc 关闭，点抽屉外的其他地方(顶栏、弹窗)不关闭;
// - 表面是半透明白色加 backdrop-blur,底下的内容糊掉而不是透出文字。
import type { ComponentProps } from "react";
import { Dialog as SheetPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";

function Sheet(props: ComponentProps<typeof SheetPrimitive.Root>) {
  return <SheetPrimitive.Root data-slot="sheet" modal={false} {...props} />;
}

function SheetClose(props: ComponentProps<typeof SheetPrimitive.Close>) {
  return <SheetPrimitive.Close data-slot="sheet-close" {...props} />;
}

function SheetOverlay({ className, ...props }: ComponentProps<"div">) {
  return (
    <SheetPrimitive.Close asChild>
      <div data-slot="sheet-overlay" aria-hidden className={cn("absolute inset-0 z-5 bg-scrim", className)} {...props} />
    </SheetPrimitive.Close>
  );
}

function SheetContent({
  className,
  children,
  onInteractOutside,
  onOpenAutoFocus,
  ...props
}: ComponentProps<typeof SheetPrimitive.Content>) {
  return (
    <>
      <SheetOverlay />
      <SheetPrimitive.Content
        data-slot="sheet-content"
        className={cn(
          "absolute z-6 flex animate-sheet-in flex-col overflow-hidden rounded-tile border border-hairline bg-glass-overlay shadow-sheet outline-none backdrop-blur-xl backdrop-saturate-150",
          className,
        )}
        onInteractOutside={(e) => {
          e.preventDefault();
          onInteractOutside?.(e);
        }}
        onOpenAutoFocus={(e) => {
          // 焦点交给抽屉本身而不是第一个按钮，打开时不出现按钮的焦点框。
          e.preventDefault();
          (e.currentTarget as HTMLElement).focus();
          onOpenAutoFocus?.(e);
        }}
        {...props}
      >
        {children}
      </SheetPrimitive.Content>
    </>
  );
}

function SheetHeader({ className, ...props }: ComponentProps<"div">) {
  return (
    <div data-slot="sheet-header" className={cn("flex shrink-0 items-center gap-2 border-b border-hairline", className)} {...props} />
  );
}

function SheetTitle({ className, ...props }: ComponentProps<typeof SheetPrimitive.Title>) {
  return <SheetPrimitive.Title data-slot="sheet-title" className={cn("font-semibold text-foreground", className)} {...props} />;
}

function SheetDescription({ className, ...props }: ComponentProps<typeof SheetPrimitive.Description>) {
  return (
    <SheetPrimitive.Description data-slot="sheet-description" className={cn("text-sm text-muted-foreground", className)} {...props} />
  );
}

export { Sheet, SheetClose, SheetContent, SheetDescription, SheetHeader, SheetTitle };
