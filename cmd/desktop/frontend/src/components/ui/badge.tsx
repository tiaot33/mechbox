// 小标签：状态(已是最新、有新版本、检查失败)、标记(已入库、可执行、自定义)与审计结论，按语气取色。
import type { ComponentProps } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";
import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex shrink-0 items-center rounded-[5px] px-1.5 py-px text-[10.5px] font-medium whitespace-nowrap",
  {
    variants: {
      tone: {
        neutral: "border border-border-subtle bg-gray-100 px-[5px] py-0 font-normal text-foreground-secondary",
        ok: "bg-ok-soft text-ok-strong",
        warn: "bg-warn-soft text-warn-strong",
        bad: "bg-bad-soft text-bad-strong",
        accent: "bg-primary-soft text-primary",
        group: "bg-group-soft text-group-strong",
      },
    },
    defaultVariants: { tone: "neutral" },
  },
);

type Tone = NonNullable<VariantProps<typeof badgeVariants>["tone"]>;

function Badge({
  className,
  tone,
  asChild = false,
  ...props
}: ComponentProps<"span"> & VariantProps<typeof badgeVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot.Root : "span";
  return <Comp data-slot="badge" data-tone={tone ?? "neutral"} className={cn(badgeVariants({ tone }), className)} {...props} />;
}

export { Badge, badgeVariants, type Tone };
