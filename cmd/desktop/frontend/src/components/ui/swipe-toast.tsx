// 可下滑关掉的 toast(照 React Bits 的 SwipeToast 改写):从底部升起，底边一条引线按剩余时间烧短，烧完自己收起;
// 鼠标悬停、按住、聚焦或窗口在后台时引线停烧。往下拖过 swipeDistance 或往下一甩就关掉，拖得不够弹回原位，往上拖有阻尼。
// 显隐阶段的样式(升起、收起、引线)在 index.css 的 .swipe-toast 里。
import { useEffect, useRef, useState, type ReactNode } from "react";
import { animate, motion, useMotionValue, useReducedMotion, useTransform } from "motion/react";

export type ToastCloseReason = "timeout" | "swipe" | "action" | "escape";

const EASE_OUT = [0.23, 1, 0.32, 1] as const;
const FLICK = 0.11; // 往下甩的速度(px/ms)超过它就关掉
const DEAD_ZONE = 3;
const RESIST_PX = 24;
const SLIDE_MS = 400;
const EXIT = 0.7;
const BURN = [{ transform: "scaleX(1)" }, { transform: "scaleX(0)" }];
const HAS_STARTING_STYLE = typeof window !== "undefined" && "CSSStartingStyleRule" in window;

const rubberband = (over: number, dim: number, c = 0.55) => (over * dim * c) / (dim + c * Math.abs(over));
const velocityOf = (hist: [number, number][]) => {
  if (hist.length < 2) return 0;
  const [t0, y0] = hist[0];
  const [t1, y1] = hist[hist.length - 1];
  return performance.now() - t1 > 100 ? 0 : (y1 - y0) / Math.max(1, t1 - t0);
};

interface Drag {
  id: number;
  startY: number;
  grab: number | null;
  moved: boolean;
  hist: [number, number][];
}

export function SwipeToast({
  children,
  tone = "ok",
  actionLabel,
  onAction,
  onClose,
  duration = 4000,
  swipeDistance = 40,
}: {
  children: ReactNode;
  tone?: "ok" | "error";
  actionLabel?: string;
  onAction?: () => void;
  onClose: (reason: ToastCloseReason) => void;
  duration?: number;
  swipeDistance?: number;
}) {
  const reduce = useReducedMotion();
  const [phase, setPhase] = useState<"open" | "closing" | "gone">("open");
  const [instant, setInstant] = useState(false);
  const [mounted, setMounted] = useState(HAS_STARTING_STYLE);
  const card = useRef<HTMLDivElement>(null);
  const fuse = useRef<HTMLElement>(null);
  const burn = useRef<Animation | null>(null);
  const drag = useRef<Drag | null>(null);
  // 引线停烧的几种原因，任一成立就停。
  const flags = useRef({ hover: false, interacting: false, focus: false, hidden: false });
  const lastInput = useRef<"pointer" | "keyboard">("pointer");
  const pendingClose = useRef<ToastCloseReason | null>(null);
  const closeTimer = useRef<number | undefined>(undefined);
  const leaving = useRef(false);
  const phaseRef = useRef(phase);
  phaseRef.current = phase;
  const latest = useRef({ onClose, onAction });
  latest.current = { onClose, onAction };

  const y = useMotionValue(0);
  const fade = useMotionValue(1);
  const transform = useTransform(y, (v) => `translateY(${v}px)`);

  const syncFuse = () => {
    const a = burn.current;
    if (!a) return;
    const f = flags.current;
    if (f.hover || f.interacting || f.focus || f.hidden) a.pause();
    else if (a.playState === "paused") a.play();
  };
  const finish = (why: ToastCloseReason) => {
    setPhase("gone");
    leaving.current = false;
    latest.current.onClose(why);
  };
  const close = (why: ToastCloseReason) => {
    if (phaseRef.current !== "open" || leaving.current) return;
    // 正拖着时等松手再关。
    if (drag.current) {
      pendingClose.current = why;
      return;
    }
    burn.current?.pause();
    const now = why === "escape" || (why === "action" && lastInput.current === "keyboard");
    setInstant(now);
    setPhase("closing");
    window.clearTimeout(closeTimer.current);
    closeTimer.current = window.setTimeout(() => finish(why), now ? 0 : SLIDE_MS * EXIT + 60);
  };
  // 收起途中又按住了：停下收起，回到打开。
  const rescue = () => {
    window.clearTimeout(closeTimer.current);
    setInstant(false);
    y.set(0);
    fade.set(1);
    setPhase("open");
  };

  useEffect(() => {
    if (!HAS_STARTING_STYLE) requestAnimationFrame(() => setMounted(true));
  }, []);
  useEffect(() => {
    if (phase !== "open" || duration <= 0 || !fuse.current) return undefined;
    burn.current?.cancel();
    const a = fuse.current.animate(BURN, { duration, easing: "linear", fill: "forwards" });
    a.onfinish = () => close("timeout");
    burn.current = a;
    syncFuse();
    return () => {
      a.onfinish = null;
      a.pause();
    };
  }, [phase, duration]);
  useEffect(() => {
    const onVisibility = () => {
      flags.current.hidden = document.hidden;
      syncFuse();
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      window.clearTimeout(closeTimer.current);
      burn.current?.cancel();
    };
  }, []);

  const swipeOut = (dy: number, v: number) => {
    burn.current?.pause();
    leaving.current = true;
    pendingClose.current = null;
    if (!reduce && card.current) {
      void animate(y, dy + card.current.offsetHeight, { type: "spring", duration: 0.3, bounce: 0, velocity: v * 1000 });
    }
    void animate(fade, 0, { duration: 0.2, ease: EASE_OUT }).then(() => finish("swipe"));
  };
  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    lastInput.current = "pointer";
    if (e.button !== 0 || drag.current || leaving.current || (e.target as Element).closest("button")) return;
    if (phaseRef.current === "closing") rescue();
    try {
      card.current?.setPointerCapture(e.pointerId);
    } catch {
      // 指针已经不在了：照样开始拖，松手时一并收尾。
    }
    y.stop();
    drag.current = { id: e.pointerId, startY: e.clientY, grab: null, moved: false, hist: [[performance.now(), y.get()]] };
    flags.current.interacting = true;
    syncFuse();
  };
  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d || d.id !== e.pointerId) return;
    if (d.grab === null) {
      if (Math.abs(e.clientY - d.startY) < DEAD_ZONE) return;
      d.grab = e.clientY - y.get();
      if (card.current) card.current.dataset.swiping = "";
    }
    const raw = e.clientY - d.grab;
    const next = raw >= 0 ? raw : rubberband(raw, RESIST_PX);
    y.set(next);
    d.moved = true;
    d.hist.push([performance.now(), next]);
    if (d.hist.length > 4) d.hist.shift();
  };
  const onPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d || d.id !== e.pointerId) return;
    drag.current = null;
    if (card.current) delete card.current.dataset.swiping;
    try {
      card.current?.releasePointerCapture(e.pointerId);
    } catch {
      // 指针已释放。
    }
    flags.current.interacting = false;
    const dy = y.get();
    const v = velocityOf(d.hist);
    if (dy > 0 && (v > FLICK || (dy >= swipeDistance && v >= 0))) {
      swipeOut(dy, v);
      return;
    }
    if (d.moved) {
      void animate(y, 0, reduce ? { duration: 0.2, ease: EASE_OUT } : { type: "spring", duration: 0.5, bounce: 0.2, velocity: v * 1000 });
    }
    const queued = pendingClose.current;
    pendingClose.current = null;
    if (queued) close(queued);
    else syncFuse();
  };

  return (
    <div
      data-slot="toast"
      data-state={phase === "open" ? "open" : "closed"}
      data-phase={phase}
      data-tone={tone}
      data-fuse={duration > 0 ? "" : undefined}
      data-instant={instant ? "" : undefined}
      data-mounted={mounted}
      className="swipe-toast pointer-events-none fixed bottom-[66px] left-1/2 z-[101] w-max max-w-[min(640px,calc(100vw-48px))] -translate-x-1/2"
    >
      <div className="swipe-toast-lift">
        <motion.div
          ref={card}
          role="status"
          aria-live="polite"
          aria-atomic="true"
          tabIndex={0}
          className="swipe-toast-card pointer-events-auto relative flex cursor-grab touch-none items-center gap-2.5 overflow-hidden rounded-[14px] border border-border bg-white px-4 py-2 text-[12.5px] break-words whitespace-pre-line shadow-toast outline-none select-none data-[swiping]:cursor-grabbing"
          style={{ transform, opacity: fade }}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerUp}
          onPointerEnter={(e) => {
            if (e.pointerType !== "mouse") return;
            flags.current.hover = true;
            syncFuse();
          }}
          onPointerLeave={(e) => {
            if (e.pointerType !== "mouse") return;
            flags.current.hover = false;
            syncFuse();
          }}
          onFocus={() => {
            flags.current.focus = true;
            syncFuse();
          }}
          onBlur={(e) => {
            if (e.currentTarget.contains(e.relatedTarget)) return;
            flags.current.focus = false;
            syncFuse();
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") lastInput.current = "keyboard";
            if (e.key === "Escape") {
              e.stopPropagation();
              close("escape");
            }
          }}
        >
          <span className="min-w-0 grow text-center">{children}</span>
          {actionLabel && (
            <button
              type="button"
              className="shrink-0 font-medium text-primary transition-transform hover:underline active:scale-[0.97]"
              onClick={() => {
                latest.current.onAction?.();
                close("action");
              }}
            >
              {actionLabel}
            </button>
          )}
          <i ref={fuse} className="swipe-toast-fuse" aria-hidden />
        </motion.div>
      </div>
    </div>
  );
}
