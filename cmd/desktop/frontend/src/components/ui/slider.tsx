// shadcn/ui 的 Slider,尺寸与配色按 Switch 改：浅灰轨道、蓝色已选段、白色圆钮;只用单个圆钮。
// valueText 是读屏读出的值(如「35%」),不给时读数值本身。
import type { ComponentProps } from "react";
import { Slider as SliderPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";

function Slider({
  className,
  "aria-label": label,
  valueText,
  ...props
}: ComponentProps<typeof SliderPrimitive.Root> & { valueText?: string }) {
  return (
    <SliderPrimitive.Root
      data-slot="slider"
      className={cn("relative flex h-[17px] touch-none items-center select-none data-[disabled]:opacity-45", className)}
      {...props}
    >
      <SliderPrimitive.Track className="relative h-1 grow overflow-hidden rounded-full bg-border-strong">
        <SliderPrimitive.Range className="absolute h-full bg-primary" />
      </SliderPrimitive.Track>
      <SliderPrimitive.Thumb
        aria-label={label}
        aria-valuetext={valueText}
        className="block size-[15px] rounded-full bg-knob shadow-[0_1px_2px_rgba(0,0,0,0.2),0_0_0_0.5px_rgba(0,0,0,0.12)] outline-none focus-visible:ring-3 focus-visible:ring-primary-soft"
      />
    </SliderPrimitive.Root>
  );
}

export { Slider };
