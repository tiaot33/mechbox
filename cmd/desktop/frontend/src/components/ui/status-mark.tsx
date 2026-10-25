// 状态圈(照 React Bits 的 StatusMark 改写，只留图形):pending 是虚线圈,running 是转动的弧,
// done / failed 由弧合成整圈再画出对勾 / 叉。状态之间是形变过渡，不是换图;各状态的颜色与对勾、叉的描画在 index.css 的 .status-mark 里。
import { useEffect, useLayoutEffect, useRef } from "react";
import { animate, useMotionValue, useReducedMotion } from "motion/react";
import { cn } from "@/lib/utils";

export type MarkStatus = "pending" | "running" | "done" | "failed";

const UI = { type: "spring", duration: 0.3, bounce: 0 } as const;
const MORPH = { duration: 0.3, ease: [0.77, 0, 0.175, 1] } as const;
const CHECK = "M7.5 12.25 10.5 15.25 16.75 8.75";
const CROSS = "M8.5 8.5 15.5 15.5M15.5 8.5 8.5 15.5";
// 虚线圈里每一段的实线占比。
const IDLE_DASH = 0.3;

export function StatusMark({
  status,
  size = 14,
  strokeWidth = 2,
  dashes = 8,
  spinMs = 1100,
  arcLength = 0.68,
  className,
}: {
  status: MarkStatus;
  size?: number;
  strokeWidth?: number; // 按 24×24 的 viewBox 计
  dashes?: number;
  spinMs?: number;
  arcLength?: number; // running 时弧占整圈的比例
  className?: string;
}) {
  const reduce = useReducedMotion();
  const r = 10 - strokeWidth / 2;
  const C = 2 * Math.PI * r;
  const P = C / Math.max(1, dashes);
  const running = status === "running";
  const solid = status !== "pending";
  const targetArc = running ? arcLength : 1;

  // mode 从虚线圈(0)形变到实线弧(1);arc 是弧长占比;travel 是 dashoffset,转圈就是让它一直减。
  const mode = useMotionValue(solid ? 1 : 0);
  const arc = useMotionValue(targetArc);
  const travel = useMotionValue(0);
  const ring = useRef<SVGCircleElement>(null);
  const geo = useRef({ C, P });
  geo.current = { C, P };
  const gen = useRef(0);

  const writeDash = () => {
    const { C, P } = geo.current;
    const m = mode.get();
    const a = arc.get();
    const dash = IDLE_DASH * P + (a * C - IDLE_DASH * P) * m;
    const gap = (1 - IDLE_DASH) * P + ((1 - a) * C - (1 - IDLE_DASH) * P) * m;
    ring.current?.setAttribute("stroke-dasharray", `${Math.max(0, dash)} ${Math.max(0, gap)}`);
  };
  useLayoutEffect(() => {
    writeDash();
    ring.current?.setAttribute("stroke-dashoffset", String(travel.get()));
  }, [C, P]);
  useEffect(() => {
    const offs = [
      mode.on("change", writeDash),
      arc.on("change", writeDash),
      travel.on("change", (v) => ring.current?.setAttribute("stroke-dashoffset", String(v))),
    ];
    return () => {
      offs.forEach((off) => off());
      mode.stop();
      arc.stop();
      travel.stop();
    };
  }, []);

  useEffect(() => {
    const g = ++gen.current;
    if (reduce) {
      mode.jump(solid ? 1 : 0);
      arc.jump(targetArc);
      travel.jump(0);
      return;
    }
    if (mode.get() === 0) arc.jump(targetArc);
    animate(mode, solid ? 1 : 0, MORPH);
    animate(arc, targetArc, UI);
    if (running) {
      const t0 = travel.get();
      animate(travel, [t0, t0 - C], { duration: spinMs / 1000, ease: "linear", repeat: Infinity });
      return;
    }
    // 停转时转到最近的一段的起点再归零，看不出跳动。
    const to = Math.floor(travel.get() / P) * P;
    void animate(travel, to, UI).then(() => {
      if (gen.current === g) travel.jump(0);
    });
  }, [status, targetArc, reduce, C, P, spinMs]);

  return (
    <svg
      data-slot="status-mark"
      data-status={status}
      data-indeterminate={running ? "" : undefined}
      className={cn("status-mark shrink-0", className)}
      viewBox="0 0 24 24"
      width={size}
      height={size}
      aria-hidden
      style={{ "--sm-stroke": strokeWidth } as React.CSSProperties}
    >
      <circle className="status-mark-track" cx="12" cy="12" r={r} transform="rotate(-90 12 12)" />
      <circle ref={ring} className="status-mark-ring" cx="12" cy="12" r={r} transform="rotate(-90 12 12)" />
      <path className="status-mark-check" d={CHECK} pathLength="1" />
      <path className="status-mark-cross" d={CROSS} pathLength="1" />
    </svg>
  );
}
