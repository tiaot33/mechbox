// shadcn/ui 的 Dialog,按本应用改过：
// - 贴着窗口上部(10vh)出现而不是垂直居中：内容变高(切换入库来源、预览结果出来)时只往下长，不上下跳;
// - 表面是半透明白色加 backdrop-blur,遮罩是淡色，不用 shadcn 默认的不透明背景和深色遮罩;
// - 标题、关闭按钮、可滚动的主体与底部按钮行各有一个部件，弹窗外壳 Modal(lib/ui.tsx)把它们拼起来。
import type { ComponentProps } from "react";
import { Dialog as DialogPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";

function Dialog(props: ComponentProps<typeof DialogPrimitive.Root>) {
  return <DialogPrimitive.Root data-slot="dialog" {...props} />;
}

function DialogClose(props: ComponentProps<typeof DialogPrimitive.Close>) {
  return <DialogPrimitive.Close data-slot="dialog-close" {...props} />;
}

function DialogContent({ className, children, wide = false, ...props }: ComponentProps<typeof DialogPrimitive.Content> & { wide?: boolean }) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay data-slot="dialog-overlay" className="fixed inset-0 z-50 animate-fade-in bg-scrim-strong" />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        aria-describedby={undefined}
        className={cn(
          "fixed top-[10vh] left-1/2 z-50 flex max-h-[calc(90vh-40px)] w-[560px] max-w-[calc(100vw-48px)] -translate-x-1/2 animate-dialog-in flex-col rounded-tile border border-hairline bg-glass-overlay shadow-dialog outline-none backdrop-blur-xl backdrop-saturate-150",
          wide && "w-[760px]",
          className,
        )}
        {...props}
      >
        {children}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
}

function DialogHeader({ className, ...props }: ComponentProps<"div">) {
  return <div data-slot="dialog-header" className={cn("flex items-center gap-2 px-4 pt-3.5 pb-1.5", className)} {...props} />;
}

function DialogTitle({ className, ...props }: ComponentProps<typeof DialogPrimitive.Title>) {
  return <DialogPrimitive.Title data-slot="dialog-title" className={cn("flex-1 text-[15px] font-bold", className)} {...props} />;
}

// 子元素不被压缩：否则 overflow 隐藏的块会被挤扁、内容被裁掉，主体反而滚不起来。
function DialogBody({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-body"
      className={cn("flex min-h-0 flex-col gap-2.5 overflow-y-auto px-4 pt-2 pb-3 *:shrink-0", className)}
      {...props}
    />
  );
}

function DialogFooter({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn("flex items-center justify-end gap-2 border-t border-border-subtle px-4 pt-2.5 pb-3.5", className)}
      {...props}
    />
  );
}

export { Dialog, DialogBody, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle };
