// shadcn/ui 的 Button,照 macOS 设置页的按钮：一块没有边框、没有投影的灰底，圆角 7 px,高 24 px(和输入框、下拉框、分段选择一样高),字在正中;按下时缩一点。
// 按轻重分四档，一个页面或弹窗里从上到下最多各用到一次主要和强调:
// - 主要(primary):实心绿底白字，一屏或一个弹窗里只有一个;
// - 绿胶囊(pill):实心绿底白字、胶囊形。只给「更新」和「入库」:它们在列表里反复出现，是这个应用最常做的两件事，要一眼认出来;
// - 强调(accent):和普通按钮一样的灰底，字是半粗的绿色。给一块区域里最要紧的那个动作(技能详情的「安装」);
// - 普通(default):灰底、主要档的字。常用但不抢眼的动作;
// - 轻(ghost):没有底、次要档的灰字，悬停才铺灰底。顶栏、列表行内，以及不常用的动作(「更新历史」「添加备注」「跳过」「不感兴趣」)。
// 破坏性不是另一档：平时用普通或轻;
// 确认删除的弹窗里，确认按钮用实心强调(solid)。文字链接(link)只用于去别的地方，动作用轻按钮。
// loading 时文字留着占位、上面盖一个转圈，按钮宽度不变;done 时转圈合成对勾(给操作完成后停一下再关弹窗的按钮用)。
// ripple 的按钮按下时缩一点，从按下的位置漾开一圈。
import type { ComponentProps } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";
import { cn } from "@/lib/utils";
import { Spinner } from "./spinner";

const buttonVariants = cva(
  "relative inline-flex shrink-0 items-center justify-center gap-[5px] rounded-control whitespace-nowrap outline-none transition-[color,background-color,box-shadow,transform,filter] duration-150 focus-visible:ring-3 focus-visible:ring-primary-soft enabled:active:scale-[0.97]",
  {
    variants: {
      variant: {
        default:
          "bg-control text-foreground hover:bg-control-hover active:bg-control-active disabled:text-subtle disabled:hover:bg-control",
        primary: "bg-primary text-on-primary hover:brightness-110 active:brightness-95 disabled:opacity-45 disabled:hover:brightness-100",
        pill: "rounded-pill bg-primary px-3 text-on-primary hover:brightness-110 active:brightness-95 disabled:opacity-45 disabled:hover:brightness-100",
        accent:
          "bg-control font-semibold text-on-primary-soft hover:bg-control-hover active:bg-control-active disabled:font-normal disabled:text-subtle disabled:hover:bg-control",
        solid: "bg-solid text-on-solid hover:bg-solid/85 disabled:opacity-45 disabled:hover:bg-solid",
        ghost: "text-foreground-secondary hover:bg-fill hover:text-foreground disabled:text-subtle disabled:hover:bg-transparent",
        link: "text-primary hover:underline focus-visible:ring-0 focus-visible:underline disabled:text-subtle disabled:no-underline enabled:active:scale-100",
      },
      size: {
        // 字比行框的中线低半个像素(中文字形偏下),底下垫 1 px 把它托回正中。
        default: "h-6 px-2.5 pb-px text-body [&_[data-slot=icon]]:size-3",
        // 小一号(20 px、标注字号):列表行里的按钮(行首的「入库」「更新」、跟在状态后面的动作)。
        sm: "h-5 px-2 text-caption [&_[data-slot=icon]]:size-3",
        icon: "size-6 [&_[data-slot=icon]]:size-3.5",
        "icon-sm": "size-5 [&_[data-slot=icon]]:size-3",
        inline: "h-auto p-0 text-body",
      },
    },
    // 行内的轻按钮(size="inline")没有内边距，悬停只把字加深，不铺灰底，按下也不缩。
    compoundVariants: [{ variant: "ghost", size: "inline", class: "hover:bg-transparent enabled:active:scale-100" }],
    defaultVariants: { variant: "default", size: "default" },
  },
);

type ButtonProps = ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean;
    loading?: boolean;
    done?: boolean;
    ripple?: boolean;
  };

function spawnRipple(e: React.PointerEvent<HTMLButtonElement>, light: boolean) {
  const b = e.currentTarget;
  const r = b.getBoundingClientRect();
  const d = Math.hypot(r.width, r.height) * 2;
  const dot = document.createElement("span");
  dot.className = cn("pointer-events-none absolute rounded-full", light ? "bg-on-primary/30" : "bg-foreground/10");
  Object.assign(dot.style, { width: `${d}px`, height: `${d}px`, left: `${e.clientX - r.left - d / 2}px`, top: `${e.clientY - r.top - d / 2}px` });
  b.append(dot);
  dot.animate([{ transform: "scale(0)", opacity: 1 }, { transform: "scale(1)", opacity: 0 }], {
    duration: 600,
    easing: "cubic-bezier(0.23, 1, 0.32, 1)",
  }).onfinish = () => dot.remove();
}

function Button({
  className,
  variant,
  size,
  asChild = false,
  loading = false,
  done = false,
  ripple = false,
  disabled,
  children,
  onPointerDown,
  ...props
}: ButtonProps) {
  const classes = cn(
    buttonVariants({ variant, size: size ?? (variant === "link" ? "inline" : "default") }),
    done && "pointer-events-none",
    ripple && "overflow-hidden transition-[color,background-color,border-color,opacity,transform] duration-150 active:scale-[0.96] disabled:active:scale-100",
    className,
  );
  if (asChild) {
    return (
      <Slot.Root data-slot="button" data-variant={variant ?? "default"} className={classes} {...props}>
        {children}
      </Slot.Root>
    );
  }
  return (
    <button
      type="button"
      data-slot="button"
      data-variant={variant ?? "default"}
      className={classes}
      disabled={disabled || loading}
      aria-disabled={done || undefined}
      aria-busy={loading || undefined}
      onPointerDown={(e) => {
        onPointerDown?.(e);
        if (ripple && e.button === 0) spawnRipple(e, variant === "primary" || variant === "pill" || variant === "solid");
      }}
      {...props}
    >
      {loading || done ? (
        <>
          <span className="invisible inline-flex items-center gap-[5px]">{children}</span>
          <span className="absolute inset-0 grid place-items-center">
            <Spinner status={loading ? "running" : "done"} className={cn(done && (variant === "primary" || variant === "pill") && "[&_svg]:text-on-primary!", done && variant === "solid" && "[&_svg]:text-on-solid!")} />
          </span>
        </>
      ) : (
        children
      )}
    </button>
  );
}

export { Button, buttonVariants };
