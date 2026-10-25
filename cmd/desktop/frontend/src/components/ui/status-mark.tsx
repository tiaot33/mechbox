// 状态圈(照 React Bits 的 StatusMark 改写，只留图形):running 是转动的弧,done / failed 由弧合成整圈再画出对勾 / 叉。
// 状态之间是形变过渡，不是换图;各状态的颜色与对勾、叉的描画在 index.css 的 .status-mark 里。
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { animate, useMotionValue, useReducedMotion } from "motion/react";
import { cn } from "@/lib/utils";

export type MarkStatus = "running" | "done" | "failed";

// Settled 是一次结束的结果;id 每次结束都换，用来只闪一下对勾或叉。
export interface Settled {
  id: number;
  failed: boolean;
}

// useMarkStatus 把"进行中"与最近一次结束的结果换成状态圈该画的状态：进行中转圈，结束后画对勾或叉，停一下再收起(null)。
export function useMarkStatus(running: boolean, settled: Settled | null): MarkStatus | null {
  const [dismissed, setDismissed] = useState<number | null>(null);
  useEffect(() => {
    if (running || !settled) return;
    const timer = window.setTimeout(() => setDismissed(settled.id), 1100);
    return () => window.clearTimeout(timer);
  }, [running, settled]);
  if (running) return "running";
  return settled && settled.id !== dismissed ? (settled.failed ? "failed" : "done") : null;
}

const UI = { type: "spring", duration: 0.3, bounce: 0 } as const;
const CHECK = "M7.5 12.25 10.5 15.25 16.75 8.75";
const CROSS = "M8.5 8.5 15.5 15.5M15.5 8.5 8.5 15.5";
const ARC = 0.68; // running 时弧占整圈的比例
const SPIN_S = 1.1; // 转一圈的秒数
const STOP = 1 / 8; // 停转时减速转到下一个八分之一圈

export function StatusMark({
  status,
  size = 14,
  strokeWidth = 2,
  className,
}: {
  status: MarkStatus;
  size?: number;
  strokeWidth?: number; // 按 24×24 的 viewBox 计
  className?: string;
}) {
  const reduce = useReducedMotion();
  const r = 10 - strokeWidth / 2;
  const C = 2 * Math.PI * r;
  const running = status === "running";
  const targetArc = running ? ARC : 1;

  // arc 是弧长占比;travel 是 dashoffset,转圈就是让它一直减。
  const arc = useMotionValue(targetArc);
  const travel = useMotionValue(0);
  const ring = useRef<SVGCircleElement>(null);
  const circumference = useRef(C);
  circumference.current = C;
  const gen = useRef(0);

  const writeDash = () => {
    const c = circumference.current;
    const a = arc.get();
    ring.current?.setAttribute("stroke-dasharray", `${a * c} ${(1 - a) * c}`);
  };
  useLayoutEffect(() => {
    writeDash();
    ring.current?.setAttribute("stroke-dashoffset", String(travel.get()));
  }, [C]);
  useEffect(() => {
    const offs = [arc.on("change", writeDash), travel.on("change", (v) => ring.current?.setAttribute("stroke-dashoffset", String(v)))];
    return () => {
      offs.forEach((off) => off());
      arc.stop();
      travel.stop();
    };
  }, []);

  useEffect(() => {
    const g = ++gen.current;
    if (reduce) {
      arc.jump(targetArc);
      travel.jump(0);
      return;
    }
    animate(arc, targetArc, UI);
    if (running) {
      const t0 = travel.get();
      animate(travel, [t0, t0 - C], { duration: SPIN_S, ease: "linear", repeat: Infinity });
      return;
    }
    const step = C * STOP;
    void animate(travel, Math.floor(travel.get() / step) * step, UI).then(() => {
      if (gen.current === g) travel.jump(0);
    });
  }, [status, targetArc, reduce, C]);

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
