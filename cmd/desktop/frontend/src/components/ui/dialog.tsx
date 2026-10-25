// shadcn/ui 的 Dialog,按本应用改过：
// - 贴着窗口上部(10vh)出现而不是垂直居中：内容变高(切换入库来源、预览结果出来)时只往下长，不上下跳;
// - 表面是半透明白色加 backdrop-blur,遮罩是淡色，不用 shadcn 默认的不透明背景和深色遮罩;
// - 标题、关闭按钮、可滚动的主体与底部按钮行各有一个部件，弹窗外壳 Modal(lib/ui.tsx)把它们拼起来;
// - 从某个元素打开的(DialogMotion 给了 origin,如左栏"入库技能"):弹窗从那个元素的位置和大小放大出来，关掉时缩回去;
//   同一个弹窗里换了一页(入库预览出来，宽度变了)时从上一页的大小形变过去，不再从头弹出。
import { createContext, use, useCallback, useLayoutEffect, useRef, type ComponentProps } from "react";
import { Dialog as DialogPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";

function Dialog(props: ComponentProps<typeof DialogPrimitive.Root>) {
  return <DialogPrimitive.Root data-slot="dialog" {...props} />;
}

function DialogClose(props: ComponentProps<typeof DialogPrimitive.Close>) {
  return <DialogPrimitive.Close data-slot="dialog-close" {...props} />;
}

// DialogMotion 是弹窗栈(lib/ui.tsx 的 open)给每个弹窗的动画状态：origin 是从哪个元素打开的;
// closing 为 true 时弹窗缩回 origin,缩完调 exited 从栈里拿掉;last 记着上一页的起点，换页时接着形变。
export interface DialogMotion {
  origin?: Element;
  closing: boolean;
  exited: () => void;
  last: { from?: Box; played: boolean };
}

export const DialogMotionContext = createContext<DialogMotion | null>(null);

interface Box {
  rect: DOMRect;
  radius: number;
}

const RADIUS = 14; // rounded-tile
const EASE = "cubic-bezier(0.32, 0.72, 0, 1)";
const reduced = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;

// originBox 是 origin 现在在窗口里的位置;已经不在页面上(或被收起看不见)时为 undefined。
function originBox(origin: Element | undefined): Box | undefined {
  if (!origin?.isConnected) return undefined;
  const rect = origin.getBoundingClientRect();
  if (rect.width < 1 || rect.height < 1) return undefined;
  return { rect, radius: parseFloat(getComputedStyle(origin).borderTopLeftRadius) || 0 };
}

// boxFrame 是把弹窗(本来在 to)变换到 box 的位置和大小的那一帧;圆角按缩放比例反算，缩放中看起来仍是 box 的圆角。
function boxFrame(box: Box, to: DOMRect): Keyframe {
  const sx = box.rect.width / to.width;
  const sy = box.rect.height / to.height;
  return {
    transform: `translate(${box.rect.left - to.left}px, ${box.rect.top - to.top}px) scale(${sx}, ${sy})`,
    borderRadius: `${box.radius / sx}px / ${box.radius / sy}px`,
  };
}

// morph 让弹窗在 box 与它本来的位置之间形变;里面的内容在形变后段淡入(打开)或前段淡出(关掉)，缩放中的文字不露出来。
function morph(el: HTMLElement, box: Box, dir: "in" | "out"): Animation[] {
  const to = el.getBoundingClientRect();
  const at = boxFrame(box, to);
  const rest: Keyframe = { transform: "none", borderRadius: `${RADIUS}px` };
  const timing: KeyframeAnimationOptions = { duration: dir === "in" ? 380 : 260, easing: EASE, fill: "both" };
  const fade: Keyframe[] =
    dir === "in"
      ? [{ opacity: 0 }, { opacity: 0, offset: 0.35 }, { opacity: 1 }]
      : [{ opacity: 1 }, { opacity: 0, offset: 0.4 }, { opacity: 0 }];
  return [
    el.animate(dir === "in" ? [at, rest] : [rest, at], timing),
    ...[...el.children].map((c) => c.animate(fade, { ...timing, easing: "linear" })),
  ];
}

function DialogContent({ className, children, wide = false, ...props }: ComponentProps<typeof DialogPrimitive.Content> & { wide?: boolean }) {
  const motion = use(DialogMotionContext);
  const morphing = !!motion?.origin;
  const content = useRef<HTMLDivElement>(null);
  const overlay = useRef<HTMLDivElement>(null);
  const latest = useRef(motion);
  latest.current = motion;

  // 打开：第一次从 origin 放大;换页(同一个弹窗里换了一个 Modal)时从上一页的大小形变。
  // 用 ref 回调而不是 effect:Portal 第一次提交时还没把内容挂上。
  // 开发模式的 StrictMode 会把 ref 回调跑两遍：清理时动画还没播完就记下它的起点，第二遍从同一个起点重播。
  const attach = useCallback((el: HTMLDivElement | null) => {
    content.current = el;
    const motion = latest.current;
    if (!el || !motion?.origin || reduced()) return;
    const last = motion.last;
    const from = last.played ? last.from : originBox(motion.origin);
    last.played = true;
    const anims = from ? morph(el, from, "in") : [];
    return () => {
      const running = anims.some((a) => a.playState === "running");
      last.from = running ? from : { rect: el.getBoundingClientRect(), radius: RADIUS };
      anims.forEach((a) => a.cancel());
      content.current = null;
    };
  }, []);

  // 关掉：缩回 origin(origin 不在了就原地淡出),遮罩同时淡出，播完从弹窗栈里拿掉。
  const closing = !!motion?.closing;
  useLayoutEffect(() => {
    const el = content.current;
    if (!closing || !motion || !el) return;
    // 还在放大时就关掉：先停下放大，按弹窗本来的位置量。
    el.getAnimations({ subtree: true }).forEach((a) => a.cancel());
    el.style.pointerEvents = "none";
    if (overlay.current) overlay.current.style.pointerEvents = "none";
    const box = reduced() ? undefined : originBox(motion.origin);
    const timing: KeyframeAnimationOptions = { duration: box ? 260 : 140, easing: "ease-out", fill: "both" };
    const anims = [
      ...(box ? morph(el, box, "out") : [el.animate([{ opacity: 1 }, { opacity: 0, transform: "scale(0.98)" }], timing)]),
      ...(overlay.current ? [overlay.current.animate([{ opacity: 1 }, { opacity: 0 }], timing)] : []),
    ];
    // 缩完拿掉;没缩完弹窗就被卸掉了也拿掉，免得栈里留一个看不见的弹窗。
    let removed = false;
    const remove = () => {
      if (removed) return;
      removed = true;
      motion.exited();
    };
    void Promise.all(anims.map((a) => a.finished)).then(remove, remove);
    return remove;
  }, [closing]);

  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay ref={overlay} data-slot="dialog-overlay" className="fixed inset-0 z-50 animate-fade-in bg-scrim-strong" />
      <DialogPrimitive.Content
        ref={attach}
        data-slot="dialog-content"
        data-closing={closing || undefined}
        aria-describedby={undefined}
        className={cn(
          "fixed top-[10vh] left-1/2 z-50 flex max-h-[calc(90vh-40px)] w-[560px] max-w-[calc(100vw-48px)] -translate-x-1/2 flex-col rounded-tile border border-hairline bg-glass-overlay shadow-dialog outline-none backdrop-blur-xl backdrop-saturate-150",
          morphing ? "origin-top-left" : "animate-dialog-in",
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
