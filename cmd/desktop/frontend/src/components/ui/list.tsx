// 弹窗里的白底列表：候选、作用域、结果逐行列出。行可以是可勾选的 label(clickable),选中时淡蓝底。
import { useLayoutEffect, useRef, useState, type ComponentProps } from "react";
import { Slot } from "radix-ui";
import { cn } from "@/lib/utils";

const box = "overflow-hidden rounded-box border border-border-subtle bg-white";

function ListBox({ className, scroll = false, ...props }: ComponentProps<"div"> & { scroll?: boolean }) {
  if (scroll) return <ScrollListBox className={className} {...props} />;
  return <div data-slot="list-box" className={cn(box, className)} {...props} />;
}

// 长列表自己滚，上下的操作行与诊断保持可见：弹窗放不下时列表先缩，但至少留 10rem;
// 内容本来就不到 10rem 时按内容高度，不在几行下面留一块空白。
function ScrollListBox({ className, style, children, ...props }: ComponentProps<"div">) {
  const inner = useRef<HTMLDivElement>(null);
  const [height, setHeight] = useState(0);
  useLayoutEffect(() => {
    const el = inner.current;
    if (!el) return;
    const observer = new ResizeObserver(() => setHeight(el.offsetHeight));
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return (
    <div
      data-slot="list-box"
      className={cn(box, "shrink! overflow-y-auto", className)}
      // 加 2px 上下边框：border-box 下 min-height 含边框。
      style={{ minHeight: Math.min(160, height + 2), ...style }}
      {...props}
    >
      <div ref={inner}>{children}</div>
    </div>
  );
}

function ListRow({
  className,
  asChild = false,
  compact = false,
  clickable = false,
  selected = false,
  ...props
}: ComponentProps<"div"> & { asChild?: boolean; compact?: boolean; clickable?: boolean; selected?: boolean }) {
  const Comp = asChild ? Slot.Root : "div";
  return (
    <Comp
      data-slot="list-row"
      className={cn(
        "flex min-w-0 items-center gap-2 border-t border-border-subtle px-3 py-2 first:border-t-0",
        compact && "gap-1.5 px-2.5 py-0.5 leading-5",
        clickable && "cursor-pointer hover:bg-gray-50",
        selected && "bg-primary-soft hover:bg-primary-soft",
        className,
      )}
      {...props}
    />
  );
}

export { ListBox, ListRow };
