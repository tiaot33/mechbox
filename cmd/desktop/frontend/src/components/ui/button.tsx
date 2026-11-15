// shadcn/ui 的 Button,按设计系统分档(参照 macOS 原生控件：胶囊形、略微浮起，按下时缩一点):主要(primary,实心绿、顶上一道亮线，一屏或一个弹窗里只有一个)、普通(default,浮起的白色胶囊，细边加轻投影)、
// 轻(ghost,没有边框、次要档的灰字，顶栏、列表行内与「跳过」「不感兴趣」这类动作);
// 淡绿(tinted,淡绿底、绿字)给列表里反复出现、又要一眼看到的动作(节标题上的「更新」),比主要按钮轻。破坏性不是另一档：平时用普通;
// 确认删除的弹窗里，确认按钮用实心强调(solid)。文字链接(link)只用于去别的地方，动作用轻按钮。
// loading 时文字留着占位、上面盖一个转圈，按钮宽度不变;done 时转圈合成对勾(给操作完成后停一下再关弹窗的按钮用)。
// ripple 的按钮按下时缩一点，从按下的位置漾开一圈。
import type { ComponentProps } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";
import { cn } from "@/lib/utils";
import { Spinner } from "./spinner";

const buttonVariants = cva(
  "relative inline-flex shrink-0 items-center justify-center gap-[5px] whitespace-nowrap outline-none transition-[color,background-color,box-shadow,transform,filter] duration-150 focus-visible:ring-3 focus-visible:ring-primary-soft enabled:active:scale-[0.97]",
  {
    variants: {
      variant: {
        default:
          "rounded-pill bg-control text-foreground shadow-control hover:bg-control-hover active:bg-control-active disabled:text-subtle disabled:hover:bg-control",
        primary:
          "rounded-pill bg-primary bg-linear-to-b from-white/12 to-white/0 text-on-primary shadow-primary hover:brightness-105 active:brightness-95 disabled:opacity-45 disabled:hover:brightness-100",
        tinted:
          "rounded-pill bg-primary-soft text-on-primary-soft hover:bg-primary/20 disabled:opacity-45 disabled:hover:bg-primary-soft",
        solid:
          "rounded-pill bg-solid bg-linear-to-b from-white/12 to-white/0 text-on-solid shadow-primary hover:bg-solid/85 disabled:opacity-45 disabled:hover:bg-solid",
        ghost:
          "rounded-pill text-foreground-secondary hover:bg-fill hover:text-foreground disabled:text-subtle disabled:hover:bg-transparent",
        link: "text-primary hover:underline focus-visible:ring-0 focus-visible:underline disabled:text-subtle disabled:no-underline enabled:active:scale-100",
      },
      size: {
        default: "h-7 px-3.5 text-body [&_[data-slot=icon]]:size-3.5",
        sm: "h-6 px-3 text-body [&_[data-slot=icon]]:size-3",
        icon: "size-7 [&_[data-slot=icon]]:size-3.5",
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
        if (ripple && e.button === 0) spawnRipple(e, variant === "primary" || variant === "solid");
      }}
      {...props}
    >
      {loading || done ? (
        <>
          <span className="invisible inline-flex items-center gap-[5px]">{children}</span>
          <span className="absolute inset-0 grid place-items-center">
            <Spinner status={loading ? "running" : "done"} className={cn(done && variant === "primary" && "[&_svg]:text-on-primary!", done && variant === "solid" && "[&_svg]:text-on-solid!")} />
          </span>
        </>
      ) : (
        children
      )}
    </button>
  );
}

export { Button, buttonVariants };
