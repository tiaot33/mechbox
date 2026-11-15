// 小标签：状态与标记，按轻重分三档，不靠色相区分。
// - must(必须处理):有风险或已经坏了(改动了脚本、被手动改过、文件夹不在了、需要重新安装、操作失败),实心底加警示图标;
// - todo(要处理):有一件可以做的事(有新版本、可能改名、同名、来源里已删除),同色描边、没有底色;
// - plain(平常):只是信息(已是最新、未安装、组名、「直接安装」这类标记),灰字，没有边框和底色。
import type { ComponentProps } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";
import { Icon } from "@/components/ui/icon";
import { cn } from "@/lib/utils";

const badgeVariants = cva("inline-flex shrink-0 items-center gap-[3px] rounded-control text-caption whitespace-nowrap tabular-nums", {
  variants: {
    tone: {
      must: "bg-solid px-1.5 py-px font-semibold text-on-solid",
      todo: "border border-foreground px-[5px] text-foreground",
      plain: "text-muted-foreground",
    },
  },
  defaultVariants: { tone: "plain" },
});

type Tone = NonNullable<VariantProps<typeof badgeVariants>["tone"]>;

function Badge({
  className,
  tone,
  asChild = false,
  children,
  ...props
}: ComponentProps<"span"> & VariantProps<typeof badgeVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot.Root : "span";
  return (
    <Comp data-slot="badge" data-tone={tone ?? "plain"} className={cn(badgeVariants({ tone }), className)} {...props}>
      {tone === "must" && <Icon name="alert" className="size-3" />}
      <Slot.Slottable>{children}</Slot.Slottable>
    </Comp>
  );
}

export { Badge, badgeVariants, type Tone };
