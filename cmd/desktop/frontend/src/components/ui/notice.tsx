// 提示条(对应 shadcn/ui 的 Alert):说明、警告、失败与完成，按语气取底色。
import type { ComponentProps } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const noticeVariants = cva("rounded-[9px] px-3 py-2 text-xs leading-normal", {
  variants: {
    tone: {
      neutral: "bg-neutral-soft text-foreground-secondary",
      warn: "bg-warn-soft text-warn-on-soft",
      bad: "bg-bad-soft text-bad-on-soft",
      ok: "bg-ok-soft text-ok-on-soft",
    },
  },
  defaultVariants: { tone: "neutral" },
});

function Notice({ className, tone, ...props }: ComponentProps<"div"> & VariantProps<typeof noticeVariants>) {
  return <div data-slot="notice" data-tone={tone ?? "neutral"} className={cn(noticeVariants({ tone }), className)} {...props} />;
}

export { Notice };
