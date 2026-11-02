// 窗口的标题栏行为，顶栏(components/app/titlebar.tsx)与弹窗遮罩顶上的拖动条(components/ui/dialog.tsx)共用。
// macOS 上标题栏是原生的隐形标题栏(main.go 的 InvisibleTitleBarHeight),这里只标出拖动区;Windows 上窗口没有系统标题栏
// (window_windows.go),系统标题栏的几样行为由这里补上。
import type { CSSProperties, HTMLAttributes, MouseEvent } from "react";
import { Window } from "@wailsio/runtime";
import { platform } from "./platform";

// 顶栏高度，和 main.go 里 macOS 的 InvisibleTitleBarHeight 一样;弹窗遮罩顶上的拖动条也是这么高。
export const captionHeight = 50;

const onCaption = (e: MouseEvent) =>
  getComputedStyle(e.target as Element).getPropertyValue("--wails-draggable").trim() === "drag";

// captionProps 是拖动区元素的属性：整块是窗口拖动区(--wails-draggable 会继承，里面能点的元素要显式 no-drag),高度是 captionHeight。
// Windows 上和系统标题栏一样：按下拖动区不拿走焦点(输入框里的光标还在),双击最大化或还原;落在按钮、输入框上的不管。
export function captionProps(): Pick<HTMLAttributes<HTMLElement>, "style" | "onMouseDown" | "onDoubleClick"> {
  const style = { "--wails-draggable": "drag", height: captionHeight } as CSSProperties;
  if (platform() !== "windows") return { style };
  return {
    style,
    onMouseDown: (e) => onCaption(e) && e.preventDefault(),
    onDoubleClick: (e) => onCaption(e) && void Window.ToggleMaximise(),
  };
}

// setEdgeResize 开关 Wails 前端运行时在窗口内侧边缘调整大小的那几像素：Windows 上无边框窗口外侧没有系统的调整区，只能靠它。
// 用运行时内部的 _wails.setResizable,不用公开的 Window.SetResizable:后者在 Go 端连带去掉 WS_THICKFRAME,窗口的阴影、贴靠跟着变。
// 升级 Wails 时核对这个钩子还在。
export function setEdgeResize(on: boolean) {
  (window as { _wails?: { setResizable?: (on: boolean) => void } })._wails?.setResizable?.(on);
}
