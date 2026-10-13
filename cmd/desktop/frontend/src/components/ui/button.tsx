// shadcn/ui 的 Button,变体按原型改过：白底胶囊(default)、蓝色主按钮(primary)、红字(danger)、
// 图标按钮(ghost)与文字链接(link)。loading 时文字留着占位、上面盖一个转圈，按钮宽度不变。
import type { ComponentProps } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";
import { cn } from "@/lib/utils";
import { Spinner } from "./spinner";

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
  };

function Button({ className, variant, size, asChild = false, loading = false, disabled, children, ...props }: ButtonProps) {
  const classes = cn(buttonVariants({ variant, size: size ?? (variant === "link" ? "inline" : "default") }), className);
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
      aria-busy={loading || undefined}
      {...props}
    >
      {loading ? (
        <>
          <span className="invisible inline-flex items-center gap-[5px]">{children}</span>
          <span className="absolute inset-0 grid place-items-center">
            <Spinner />
          </span>
        </>
      ) : (
        children
      )}
    </button>
  );
}

export { Button, buttonVariants };
