// 提示条(对应 shadcn/ui 的 Alert):说明、警告、失败与完成，按语气取底色。
import type { ComponentProps } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const noticeVariants = cva("rounded-[9px] px-3 py-2 text-xs leading-normal", {
  variants: {
    tone: {
      neutral: "bg-gray-100 text-foreground-secondary",
      warn: "bg-warn-soft text-amber-800",
      bad: "bg-bad-soft text-red-800",
      ok: "bg-ok-soft text-emerald-800",
    },
  },
  defaultVariants: { tone: "neutral" },
});

function Notice({ className, tone, ...props }: ComponentProps<"div"> & VariantProps<typeof noticeVariants>) {
  return <div data-slot="notice" data-tone={tone ?? "neutral"} className={cn(noticeVariants({ tone }), className)} {...props} />;
}

export { Notice };
