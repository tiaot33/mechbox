// 列表的版式：一组一张卡片(标题行 + 技能行),卡片下面可以挂一条提示。
// 行高收紧：技能行 32px、标题行 36px、折起行与提示条更矮。所有行用同一套列：12px 边距、开头一格 14px(图标、勾选框或折起箭头)、16px 间距，接着是名字。
// 标题行、技能行、折起行、空行与提示条都从这套列排起，文字自然和技能名对齐，不用另算边距。
// 卡片有两种外观：普通的白色卡片，与"不在技能库里"用的淡化卡片(虚线、不铺底色、标题也淡),由 tone 一处决定。
import { createContext, useContext } from "react";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Icon, type IconName } from "@/components/app/icons";

// leadRow 是只有"开头一格 + 内容"的行：折起行、空行与提示条。
const leadRow = "grid grid-cols-[14px_minmax(0,1fr)] items-center gap-x-4 px-3";

// rowClass 是技能行与未纳管技能行的网格。技能库页、更新页是 开头一格 | 名字 | 描述 | 装在哪 | 最近变化;
// 作用域页、项目页是 开头一格 | 名字 | 描述 | 在这里的状态 | 开关(或入库,和开关一样大)。
const rowCols = {
  library: "grid-cols-[14px_minmax(140px,220px)_minmax(0,1fr)_minmax(80px,160px)_92px]",
  scope: "grid-cols-[14px_minmax(140px,220px)_minmax(0,1fr)_minmax(0,150px)_30px]",
};
export const rowClass = (kind: keyof typeof rowCols) =>
  cn("group/row grid min-h-8 items-center gap-x-4 border-b border-[rgba(17,24,39,0.06)] px-3 py-1 text-[12px] last:border-b-0", rowCols[kind]);

export type CardTone = "default" | "quiet";
const ToneContext = createContext<CardTone>("default");

// ListCard 是一组的卡片：标题行在上，展开时下面是行，中间一条细线;notice 是标题行与行之间的一行提示(见 Notice)。
export function ListCard({
  tone = "default",
  head,
  notice,
  open,
  children,
}: {
  tone?: CardTone;
  head: React.ReactNode;
  notice?: React.ReactNode;
  open: boolean;
  children: React.ReactNode;
}) {
  return (
    <ToneContext.Provider value={tone}>
      <div
        className={cn(
          "section-card rounded-[10px] border",
          tone === "quiet" ? "border-dashed border-black/[0.14]" : "border-hairline bg-white/55 shadow-tile",
        )}
      >
        {head}
        {open && (
          <div className="section-rows border-t border-[rgba(17,24,39,0.06)]">
            {notice}
            {children}
          </div>
        )}
      </div>
    </ToneContext.Provider>
  );
}

// CardHead 是卡片的标题行：开头一格(lead)、中间是名字、说明与操作(children,从左往右排，说明可以伸缩把操作推到右边)、最右边是收起箭头。
// 吸顶;拖放的目标属性放在 headProps 上。
export function CardHead({
  lead,
  open,
  onToggle,
  headProps,
  children,
}: {
  lead: React.ReactNode;
  open: boolean;
  onToggle: () => void;
  headProps?: React.HTMLAttributes<HTMLDivElement> & Record<`data-${string}`, unknown>;
  children: React.ReactNode;
}) {
  const t = useT().list;
  const tone = useContext(ToneContext);
  return (
    <div
      {...headProps}
      className={cn(
        "section-head group/head sticky top-0 z-[1] grid min-h-9 min-w-0 grid-cols-[14px_minmax(0,1fr)_auto] items-center gap-x-4 py-0.5 pr-2 pl-3 text-[13px] backdrop-blur-[8px]",
        tone === "quiet" ? "bg-white/25" : "bg-[rgba(250,250,251,0.86)]",
        open ? "rounded-t-[9px]" : "rounded-[9px]",
        headProps?.className,
      )}
    >
      {lead}
      <div className="flex min-w-0 items-center gap-2">{children}</div>
      <button
        type="button"
        className="sec-toggle grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-black/5 hover:text-foreground"
        aria-expanded={open}
        aria-label={open ? t.collapseSection : t.expandSection}
        title={open ? t.collapseSection : t.expandSection}
        onClick={onToggle}
      >
        <Icon name="down" className={cn("size-3 transition-transform", !open && "-rotate-90")} />
      </button>
    </div>
  );
}

// HeadName 是标题行的名字;淡化的卡片里用次一级的颜色。
export function HeadName({ children, title }: { children: React.ReactNode; title?: string }) {
  const tone = useContext(ToneContext);
  return (
    <b className={cn("section-name truncate", tone === "quiet" ? "font-medium text-foreground-secondary" : "font-semibold")} title={title}>
      {children}
    </b>
  );
}

// HeadMeta 是名字后面淡色的一段说明(个数、来源版本等，用 · 隔开);grow 的放不下时先截它，名字尽量完整，也把后面的操作推到右边。
export function HeadMeta({ children, grow = false, title }: { children: React.ReactNode; grow?: boolean; title?: string }) {
  return (
    <span className={cn("min-w-0 truncate text-[12px] text-muted-foreground", grow ? "grow basis-0" : "shrink-0")} title={title}>
      {children}
    </span>
  );
}

// HeadActions 是标题行右边的一组操作，没有可伸缩的说明时自己靠右。
export function HeadActions({ children }: { children: React.ReactNode }) {
  return <span className="ml-auto flex shrink-0 items-center gap-0.5">{children}</span>;
}

// HeadAction 是标题行的一个操作：图标 + 文字的浅色按钮，和名字后面的说明分得开。
export function HeadAction({ icon, className, children, ...props }: React.ComponentProps<typeof Button> & { icon: IconName }) {
  return (
    <Button size="sm" variant="ghost" className={cn("text-[12px]", className)} {...props}>
      <Icon name={icon} />
      {children}
    </Button>
  );
}

// FoldRow 是折起的一行：箭头在开头一格，文字和技能名对齐。卡片外面用时(作用域页下面那块)加 bare,
// 留出和卡片描边一样宽的透明边，文字照样对齐。
export function FoldRow({
  open,
  label,
  bare = false,
  className,
  ...props
}: { open: boolean; label: string; bare?: boolean } & React.ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      type="button"
      aria-expanded={open}
      className={cn(
        "not-installed-fold w-full min-w-0 py-1.5 text-left text-[12px] text-muted-foreground hover:text-foreground",
        leadRow,
        bare ? "rounded-lg border border-transparent hover:bg-black/5" : "hover:bg-white/80",
        className,
      )}
      {...props}
    >
      <Icon name="chev" className={cn("size-2.5 justify-self-center transition-transform", open && "rotate-90")} />
      <span className="truncate">{label}</span>
    </button>
  );
}

// EmptyRow 是卡片里没有行时的一句说明，和技能名对齐。
export function EmptyRow({ children }: { children: React.ReactNode }) {
  return (
    <p className={cn("py-2 text-[12px] text-muted-foreground", leadRow)}>
      <span />
      <span>{children}</span>
    </p>
  );
}

// Notice 是卡片里标题行与行之间的一行提示：和技能行一样铺满整宽、底下一条细线，只是更矮(24px)、字更小更淡，
// 操作平时也是灰字，鼠标移上去才变蓝，不抢技能行的视线。
export function Notice({ className, actions, children }: { className?: string; actions: React.ReactNode; children: React.ReactNode }) {
  return (
    <div
      className={cn(
        leadRow,
        "h-6 grid-cols-[14px_minmax(0,1fr)_auto] border-b border-[rgba(17,24,39,0.06)] text-[11px] text-subtle",
        className,
      )}
    >
      <span />
      <span className="truncate">{children}</span>
      <span className="flex items-center gap-2.5">{actions}</span>
    </div>
  );
}

export function NoticeAction(props: React.ComponentProps<typeof Button>) {
  return <Button variant="link" {...props} className={cn("text-[11px] text-muted-foreground hover:text-primary", props.className)} />;
}
