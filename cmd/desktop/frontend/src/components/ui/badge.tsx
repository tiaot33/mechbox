// 小标签：状态与提醒。提醒分三级，每级一种颜色、一种圆点(浅色、深色各自定，见 index.css 的 critical / warn / info),不抢眼：
// 没有底色和边框，只是一个小圆点加一行字。
// - critical(严重):已经坏了，不处理就用不了(文件夹不在了、项目不在这台机器上、操作失败)。红，实心圆点外面一圈淡红的晕;字是红色、半粗;
// - warn(注意):有风险或有冲突，不处理可能丢东西或用错(被手动改过、更新改动了脚本、需要重新安装、同名、更新会换掉你的修改)。橙，实心圆点;字是橙色;
// - info(提示):有一件可以做的事，放着也没事(有新版本、可能改名、来源里已删除)。蓝，空心的圆圈;字是次要档的灰色;
// - plain(平常):只是信息(已是最新、未安装、「直接安装」这类标记)。灰字，没有圆点。
// 三种样子：带文字的标签(Badge:圆点 + 字);Dot 是单独的圆点，给自己排版的地方用;
// AlertDot 是占地小的地方(列表的行、侧栏)用的：一处的一条或几条提醒只画一个圆点，文字在悬停提示里。
import type { ComponentProps } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { byLevel, type Level, type Tone } from "@/lib/level";
import { cn } from "@/lib/utils";

const badgeVariants = cva("inline-flex shrink-0 items-center gap-1 text-caption whitespace-nowrap tabular-nums", {
  variants: {
    tone: {
      critical: "font-semibold text-critical-fg",
      warn: "text-warn-fg",
      info: "text-foreground-secondary",
      plain: "text-muted-foreground",
    } satisfies Record<Tone, string>,
  },
  defaultVariants: { tone: "plain" },
});

const dotShape: Record<Level, string> = {
  critical: "bg-critical shadow-[0_0_0_2px_var(--color-critical-halo)]",
  warn: "bg-warn",
  info: "border-[1.5px] border-info",
};

// Dot 是提醒的小圆点：6 px,三级形状不同(空心圈、实心点、带晕的实心点),不只靠颜色分。
function Dot({ tone, className, ...props }: ComponentProps<"i"> & { tone: Level }) {
  return <i data-slot="dot" data-tone={tone} className={cn("block size-1.5 shrink-0 rounded-full", dotShape[tone], className)} {...props} />;
}

function Badge({ className, tone, asChild = false, children, ...props }: ComponentProps<"span"> & VariantProps<typeof badgeVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot.Root : "span";
  const level = tone && tone !== "plain" ? tone : null;
  return (
    <Comp data-slot="badge" data-tone={tone ?? "plain"} className={cn(badgeVariants({ tone }), className)} {...props}>
      {level && <Dot tone={level} className={cn(level === "critical" && "mx-0.5")} />}
      <Slot.Slottable>{children}</Slot.Slottable>
    </Comp>
  );
}

// Alert 是一条提醒：级别、一句话(label)、补充说明(tip);className 是测试钩子;onClick 是这条提醒能直接做的那件事(有新版本时打开更新计划)。
type Alert = { tone: Level; label: string; tip?: string; className?: string; onClick?: () => void };

// AlertDot 把一处的一条或几条提醒合成一个圆点：圆点按最重的一级画，鼠标停住时从重到轻逐条列出(圆点 + 一句话 + 说明)。
// 提示里的每一条同时留在 DOM 里(读屏和测试读到的和提示里显示的是同一份),界面上不占地方。
// 有带 onClick 的一条时整个圆点是按钮，点它(或聚焦后按回车、空格)做那件事。
function AlertDot({ items, className }: { items: (Alert | false | null | undefined | "")[]; className?: string }) {
  const alerts = items.filter((x): x is Alert => !!x).sort((a, b) => byLevel(a.tone, b.tone));
  if (!alerts.length) return null;
  const act = alerts.find((x) => x.onClick)?.onClick;
  const box = cn("relative inline-grid size-3.5 shrink-0 place-items-center rounded-full outline-none", className);
  const inner = (
    <>
      <Dot tone={alerts[0].tone} />
      {alerts.map((a) => (
        <span key={a.label} data-slot="badge" data-tone={a.tone} className={cn("sr-only", a.className)}>
          {a.label}
          {a.tip && <span data-slot="badge-tip">{a.tip}</span>}
        </span>
      ))}
    </>
  );
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        {act ? (
          <button
            type="button"
            data-slot="alert-dot"
            className={cn(box, "cursor-pointer focus-visible:ring-3 focus-visible:ring-primary-soft")}
            onClick={(e) => {
              e.stopPropagation();
              act();
            }}
          >
            {inner}
          </button>
        ) : (
          <span data-slot="alert-dot" className={box}>
            {inner}
          </span>
        )}
      </TooltipTrigger>
      <TooltipContent className="flex flex-col gap-1.5">
        {alerts.map((a) => (
          <div key={a.label} className="grid grid-cols-[14px_1fr] items-baseline">
            <Dot tone={a.tone} className="translate-y-[-1px] self-center justify-self-start" />
            <b className="font-semibold">{a.label}</b>
            {a.tip && <div className="col-start-2 text-foreground-secondary">{a.tip}</div>}
          </div>
        ))}
      </TooltipContent>
    </Tooltip>
  );
}

export { AlertDot, Badge, badgeVariants, Dot, type Alert };
