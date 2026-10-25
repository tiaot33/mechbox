// shadcn/ui 的 Button,变体按原型改过：白底胶囊(default)、蓝色主按钮(primary)、红字(danger)、
// 图标按钮(ghost)与文字链接(link)。loading 时文字留着占位、上面盖一个转圈，按钮宽度不变;
// done 时转圈合成对勾(给操作完成后停一下再关弹窗的按钮用)。ripple 的按钮按下时缩一点，从按下的位置漾开一圈。
import type { ComponentProps } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import { StatusMark } from "./status-mark";

const buttonVariants = cva(
  "relative inline-flex shrink-0 items-center justify-center gap-[5px] font-medium whitespace-nowrap outline-none transition-colors focus-visible:ring-3 focus-visible:ring-primary-soft",
  {
    variants: {
      variant: {
        default:
          "rounded-full border border-border bg-white text-foreground hover:border-border-strong disabled:text-subtle disabled:hover:border-border",
        primary:
          "rounded-full border border-primary bg-primary text-white hover:bg-primary-hover disabled:opacity-45 disabled:hover:bg-primary",
        danger:
          "rounded-full border border-border bg-white text-bad hover:border-bad-border disabled:text-subtle disabled:hover:border-border",
        ghost:
          "rounded-md text-muted-foreground hover:bg-black/5 hover:text-foreground disabled:text-subtle disabled:hover:bg-transparent",
        link: "font-normal text-primary hover:underline disabled:text-subtle disabled:no-underline",
      },
      size: {
        default: "h-7 px-3 text-xs [&_[data-slot=icon]]:size-3",
        sm: "h-6 px-2.5 text-[11.5px] [&_[data-slot=icon]]:size-3",
        icon: "size-[26px] [&_[data-slot=icon]]:size-3.5",
        "icon-sm": "size-5 rounded-[5px] [&_[data-slot=icon]]:size-[11px]",
        inline: "h-auto p-0 text-xs",
      },
    },
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
  dot.className = cn("pointer-events-none absolute rounded-full", light ? "bg-white/40" : "bg-black/10");
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
  const t = useT();
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
        if (ripple && e.button === 0) spawnRipple(e, variant === "primary");
      }}
      {...props}
    >
      {loading || done ? (
        <>
          <span className="invisible inline-flex items-center gap-[5px]">{children}</span>
          <span data-slot={loading ? "spinner" : undefined} role="status" aria-label={loading ? t.common.working : undefined} className="absolute inset-0 grid place-items-center">
            <StatusMark status={loading ? "running" : "done"} size={11} strokeWidth={3} className={cn(done && variant === "primary" && "text-white!")} />
          </span>
        </>
      ) : (
        children
      )}
    </button>
  );
}

export { Button, buttonVariants };
