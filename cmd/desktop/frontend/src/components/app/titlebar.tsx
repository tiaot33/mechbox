// 顶栏的外框，主界面(toolbar.tsx)与设置页共用。整条是窗口拖动区(拖动、双击与焦点见 lib/window.ts),能点的元素必须显式 no-drag,
// 否则 mousedown 被当成拖窗口，输入框拿不到焦点。titlebar 类名是测试钩子。
// macOS 上左端给红绿灯留位置;Windows 上窗口没有系统标题栏(见 window_windows.go),最右端画最小化、最大化、关闭三个按钮;
// Linux 上有系统标题栏，两样都不要。
// 模态弹窗的遮罩(z-50)盖住整个窗口(含顶栏):Windows 上窗口按钮用 z-[60] 浮在遮罩上面，遮罩顶上一条是拖动区(见 dialog.tsx),
// 弹窗开着时也能拖动、最大化、最小化、关闭窗口，和系统标题栏一样;按下这些地方不算点了弹窗外面，弹窗不关。
import { useEffect, useState, type ReactNode } from "react";
import { Window } from "@wailsio/runtime";
import { useT } from "@/lib/i18n";
import { platform } from "@/lib/platform";
import { cn } from "@/lib/utils";
import { captionProps, setEdgeResize } from "@/lib/window";

export function Titlebar({ left, right }: { left: ReactNode; right?: ReactNode }) {
  const windows = platform() === "windows";
  return (
    <header
      className={cn(
        "titlebar grid shrink-0 grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-3 px-3.5 [&_:is(button,input,label,select,[data-no-drag])]:[--wails-draggable:no-drag]",
        windows && "pr-0",
      )}
      {...captionProps()}
    >
      <div className="flex min-w-0 items-center gap-2">
        {platform() === "mac" && <div className="w-16" />}
        {left}
      </div>
      <div />
      <div className="flex min-w-0 items-center justify-end gap-2 self-stretch">
        {right}
        {windows && <WindowControls />}
      </div>
    </header>
  );
}

// 按钮照 Windows 11 的标题栏画：46 像素宽、占满顶栏高度，字形用系统的 Segoe Fluent Icons(Windows 10 上是 Segoe MDL2 Assets,码位相同),
// 窗口不在前台时字形变淡;按下不拿走焦点(输入框里的光标还在),也不进 Tab 顺序。最大化后边缘不再能拖动调整大小，
// 从顶边往下拖是把窗口拖回原来大小，最右上角点下去是关闭。
function WindowControls() {
  const t = useT().toolbar;
  const [maximised, setMaximised] = useState(false);
  const [active, setActive] = useState(() => document.hasFocus());
  useEffect(() => {
    const sync = () =>
      void Window.IsMaximised().then((max) => {
        setMaximised(max);
        setEdgeResize(!max);
      });
    const focus = () => setActive(true);
    const blur = () => setActive(false);
    sync();
    window.addEventListener("resize", sync);
    window.addEventListener("focus", focus);
    window.addEventListener("blur", blur);
    return () => {
      window.removeEventListener("resize", sync);
      window.removeEventListener("focus", focus);
      window.removeEventListener("blur", blur);
    };
  }, []);
  const button =
    "grid w-[46px] place-items-center font-['Segoe_Fluent_Icons','Segoe_MDL2_Assets'] text-[10px] leading-none transition-colors duration-100";
  const plain = cn(button, "hover:bg-black/[0.0373] active:bg-black/[0.0241] active:text-muted-foreground");
  return (
    <div
      data-no-drag
      className={cn("pointer-events-auto relative z-[60] flex self-stretch", active ? "text-foreground" : "text-subtle")}
      onPointerDown={(e) => e.stopPropagation()}
      onMouseDown={(e) => e.preventDefault()}
    >
      <button type="button" tabIndex={-1} className={plain} title={t.minimise} onClick={() => void Window.Minimise()}>
        {"\uE921"}
      </button>
      <button type="button" tabIndex={-1} className={plain} title={maximised ? t.restore : t.maximise} onClick={() => void Window.ToggleMaximise()}>
        {maximised ? "\uE923" : "\uE922"}
      </button>
      <button
        type="button"
        tabIndex={-1}
        className={cn(button, "hover:bg-[#c42b1c] hover:text-white active:bg-[#c42b1c]/90 active:text-white/70")}
        title={t.close}
        onClick={() => void Window.Close()}
      >
        {"\uE8BB"}
      </button>
    </div>
  );
}
