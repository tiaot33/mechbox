// 小标签：状态与提醒。提醒分三级，每级一种颜色、一种圆点(浅色、深色各自定，见 index.css 的 critical / warn / info),不抢眼：
// 没有底色和边框，只是一个小圆点加一行字。
// - critical(严重):已经坏了，不处理就用不了(文件夹不在了、项目不在这台机器上、操作失败)。红，实心圆点外面一圈淡红的晕;字是红色、半粗;
// - warn(注意):有风险或有冲突，不处理可能丢东西或用错(被手动改过、更新改动了脚本、需要重新安装、同名、更新会换掉你的修改)。橙，实心圆点;字是橙色;
// - info(提示):有一件可以做的事，放着也没事(有新版本、可能改名、来源里已删除)。蓝，空心的圆圈;字是次要档的灰色;
// - plain(平常):只是信息(已是最新、未安装、「直接安装」这类标记)。灰字，没有圆点。
// 三种样子：带文字的标签(圆点 + 字);只有圆点(compact,占地小的地方：列表的行、侧栏),文字留在 DOM 里给读屏和测试，鼠标停住时用提示显示
// (tip 是文字下面的补充说明;可拖动的行上原生的 title 不出现，所以用 Tooltip);Dot 是单独的圆点，给自己排版的地方用。
import type { ComponentProps, ReactNode } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

const badgeVariants = cva("inline-flex shrink-0 items-center gap-1 text-caption whitespace-nowrap tabular-nums", {
  variants: {
    tone: {
      critical: "font-semibold text-critical-fg",
      warn: "text-warn-fg",
      info: "text-foreground-secondary",
      plain: "text-muted-foreground",
    },
  },
  defaultVariants: { tone: "plain" },
});

type Tone = NonNullable<VariantProps<typeof badgeVariants>["tone"]>;
type Level = Exclude<Tone, "plain">;

const dotShape: Record<Level, string> = {
  critical: "bg-critical shadow-[0_0_0_2px_var(--color-critical-halo)]",
  warn: "bg-warn",
  info: "border-[1.5px] border-info",
};

// levelOrder 是三级从重到轻的先后;worstLevel 取几个提醒里最重的一级(一处只画一个圆点时用)。
const levelOrder: Level[] = ["critical", "warn", "info"];
function worstLevel(tones: (Tone | undefined)[]): Level | undefined {
  return levelOrder.find((l) => tones.includes(l));
}

// Dot 是提醒的小圆点：6 px,三级形状不同(空心圈、实心点、带晕的实心点),不只靠颜色分。
function Dot({ tone, className, ...props }: ComponentProps<"i"> & { tone: Level }) {
  return <i data-slot="dot" data-tone={tone} className={cn("block size-1.5 shrink-0 rounded-full", dotShape[tone], className)} {...props} />;
}

function Badge({
  className,
  tone,
  compact = false,
  tip,
  asChild = false,
  children,
  ...props
}: ComponentProps<"span"> & VariantProps<typeof badgeVariants> & { asChild?: boolean; compact?: boolean; tip?: ReactNode }) {
  const Comp = asChild ? Slot.Root : "span";
  const level = tone && tone !== "plain" ? tone : null;
  if (compact && level) {
    // 文字的字号设成 0:它还在 DOM 里(读屏、测试都读得到),界面上只剩圆点。
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <Comp
            data-slot="badge"
            data-tone={tone}
            data-compact
            className={cn("inline-flex size-3.5 shrink-0 items-center justify-center text-[0px] leading-none outline-none", asChild && "cursor-pointer", className)}
            {...props}
          >
            <Dot tone={level} />
            <Slot.Slottable>{children}</Slot.Slottable>
          </Comp>
        </TooltipTrigger>
        <TooltipContent>
          <b className="font-semibold">{labelOf(children)}</b>
          {tip && <div className="mt-0.5 text-foreground-secondary">{tip}</div>}
        </TooltipContent>
      </Tooltip>
    );
  }
  return (
    <Comp data-slot="badge" data-tone={tone ?? "plain"} className={cn(badgeVariants({ tone }), className)} {...props}>
      {level && <Dot tone={level} className={cn(level === "critical" && "mx-0.5")} />}
      <Slot.Slottable>{children}</Slot.Slottable>
    </Comp>
  );
}

// labelOf 取出标签的文字:asChild 时文字在子元素(按钮)里面。
function labelOf(children: ReactNode): ReactNode {
  if (children && typeof children === "object" && "props" in children) return (children.props as { children?: ReactNode }).children;
  return children;
}

export { Badge, badgeVariants, Dot, worstLevel, type Level, type Tone };
