// 小标签：状态与提醒。提醒分三级，每级一种颜色、一个实心小图标(浅色、深色的颜色各自定，见 index.css 的 critical / warn / info),
// 没有底色和边框，只是一个图标加一行字。
// - critical(严重):已经坏了，不处理就用不了(文件夹不在了、项目不在这台机器上、操作失败)。红色的圆里一个叉;字是红色、半粗;
// - warn(注意):有风险或有冲突，不处理可能丢东西或用错(被手动改过、更新改动了脚本、需要重新安装、同名、更新会换掉你的修改)。橙色的三角里一个叹号;字是橙色;
// - info(提示):有一件可以做的事，放着也没事(有你的修改、来源里已删除、库里有几个同名的)。蓝色的圆里一个 i;字是次要档的灰色;
// - update(更新):不是提醒，是来源里有新版本可以更新(有新版本、在 app 外更新到了作者的新版、本地目录里改过)。绿色(primary)的圆里一个向上的箭头;字是次要档的灰色。
// - rename(可能改名):同样不是提醒，但要用户确认是不是同一个技能，和有新版本分开：绿色的圆里一个向右的箭头。
//   这两种不用红橙蓝，和三级提醒分开画。
// - plain(平常):只是信息(已是最新、未安装、「直接安装」这类标记)。灰字，没有图标。
// 三种样子：带文字的标签(Badge:图标 + 字);MarkIcon 是单独的图标，给自己排版的地方用;
// AlertIcon 是占地小的地方(列表的行、侧栏)用的：一处的一条或几条提醒只画一个图标，文字在悬停提示里;
// labeled 时(列表一行的状态列)图标后面写最要紧的那一条，还有别的时写 +N,一行只有这一个图标。
import type { ComponentProps } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { byLevel, byUrgency, type Mark, type Tone } from "@/lib/level";
import { cn } from "@/lib/utils";

const badgeVariants = cva("inline-flex shrink-0 items-center gap-1.5 text-caption whitespace-nowrap tabular-nums", {
  variants: {
    tone: {
      critical: "font-semibold text-critical-fg",
      warn: "text-warn-fg",
      info: "text-foreground-secondary",
      update: "text-foreground-secondary",
      rename: "text-foreground-secondary",
      plain: "text-muted-foreground",
    } satisfies Record<Tone, string>,
  },
  defaultVariants: { tone: "plain" },
});

// 三级各一个实心小图标(照 SF Symbols 的 xmark.circle.fill / exclamationmark.triangle.fill / info.circle.fill),更新是 arrow.up.circle.fill(主题绿);
// 里面的符号是镂空的，透出底下的颜色，浅色深色都不用另画一份。
const markPath: Record<Mark, string> = {
  critical: "M8 1a7 7 0 1 0 0 14A7 7 0 0 0 8 1Zm-2.47 3.47L8 6.94l2.47-2.47 1.06 1.06L9.06 8l2.47 2.47-1.06 1.06L8 9.06l-2.47 2.47-1.06-1.06L6.94 8 4.47 5.53Z",
  warn: "M6.7 2.2a1.5 1.5 0 0 1 2.6 0l5.6 9.8a1.5 1.5 0 0 1-1.3 2.25H2.4a1.5 1.5 0 0 1-1.3-2.25ZM7.15 5.6l.25 4.2h1.2l.25-4.2ZM8 10.85a.95.95 0 1 0 0 1.9.95.95 0 0 0 0-1.9Z",
  info: "M8 1a7 7 0 1 0 0 14A7 7 0 0 0 8 1Zm0 2.9a1 1 0 1 0 0 2 1 1 0 0 0 0-2ZM7.15 7v5h1.7V7Z",
  update: "M8 1a7 7 0 1 0 0 14A7 7 0 0 0 8 1Zm0 3.2L4.6 7.6l1.06 1.06 1.59-1.59v4.73h1.5V7.07l1.59 1.59L11.4 7.6Z",
  rename: "M8 1a7 7 0 1 0 0 14A7 7 0 0 0 8 1Zm4.8 7L8.4 4.6 7.34 5.66l1.59 1.59H4.2v1.5h4.73l-1.59 1.59 1.06 1.06Z",
};

const markColor: Record<Mark, string> = { critical: "text-critical", warn: "text-warn", info: "text-info", update: "text-primary", rename: "text-primary" };

// MarkIcon 是提醒或更新的小图标:12 px,形状各不相同(圆里一个叉、三角里一个叹号、圆里一个 i、圆里一个向上的箭头),不只靠颜色分。
function MarkIcon({ tone, className, title, ...props }: ComponentProps<"svg"> & { tone: Mark; title?: string }) {
  return (
    <svg
      data-slot="mark-icon"
      data-tone={tone}
      viewBox="0 0 16 16"
      aria-hidden={title ? undefined : true}
      className={cn("block size-3 shrink-0", markColor[tone], className)}
      {...props}
    >
      {title && <title>{title}</title>}
      <path fill="currentColor" fillRule="evenodd" d={markPath[tone]} />
    </svg>
  );
}

function Badge({ className, tone, asChild = false, children, ...props }: ComponentProps<"span"> & VariantProps<typeof badgeVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot.Root : "span";
  const level = tone && tone !== "plain" ? tone : null;
  return (
    <Comp data-slot="badge" data-tone={tone ?? "plain"} className={cn(badgeVariants({ tone }), className)} {...props}>
      {level && <MarkIcon tone={level} />}
      <Slot.Slottable>{children}</Slot.Slottable>
    </Comp>
  );
}

// Alert 是一条提醒或更新：级别、一句话(label)、补充说明(tip);className 是测试钩子;onClick 是它能直接做的那件事(有新版本时打开更新计划)。
type Alert = { tone: Mark; label: string; tip?: string; className?: string; onClick?: () => void };

// AlertIcon 把一处的一条或几条提醒合成一个图标：图标按最重的一级画，鼠标停住时从重到轻逐条列出(图标 + 一句话 + 说明)。
// 提示里的每一条同时留在 DOM 里(读屏和测试读到的和提示里显示的是同一份),界面上不占地方。
// 排在最前的那一条带 onClick 时整个图标是按钮，点它(或聚焦后按回车、空格)做那件事。
function AlertIcon({ items, className, labeled = false }: { items: (Alert | false | null | undefined | "")[]; className?: string; labeled?: boolean }) {
  const alerts = items.filter((x): x is Alert => !!x).sort((a, b) => (labeled ? byUrgency : byLevel)(a.tone, b.tone));
  if (!alerts.length) return null;
  // 只有排在最前、画出来的那一条能直接做事时才是按钮：点下去做的就是看到的那一条(文件夹不在了 +1 不会点开更新计划)。
  const act = alerts[0].onClick;
  const box = labeled
    ? cn("relative flex h-6 max-w-full min-w-0 items-center gap-1.5 rounded-control px-1 -mx-1 text-left text-caption outline-none", className)
    : cn("relative inline-grid size-4 shrink-0 place-items-center rounded-full outline-none", className);
  const inner = (
    <>
      <MarkIcon tone={alerts[0].tone} />
      {labeled && (
        <span aria-hidden className={cn("min-w-0 truncate", badgeVariants({ tone: alerts[0].tone }), "shrink")}>
          {alerts[0].label}
        </span>
      )}
      {labeled && alerts.length > 1 && (
        <span aria-hidden className="shrink-0 text-caption text-muted-foreground tabular-nums">
          +{alerts.length - 1}
        </span>
      )}
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
            className={cn(box, "cursor-pointer focus-visible:ring-3 focus-visible:ring-primary-soft", labeled && "hover:bg-fill")}
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
          <div key={a.label} className="grid grid-cols-[18px_1fr] items-baseline">
            <MarkIcon tone={a.tone} className="self-center justify-self-start" />
            <b className="font-semibold">{a.label}</b>
            {a.tip && <div className="col-start-2 text-foreground-secondary">{a.tip}</div>}
          </div>
        ))}
      </TooltipContent>
    </Tooltip>
  );
}

export { AlertIcon, Badge, badgeVariants, MarkIcon, type Alert };
