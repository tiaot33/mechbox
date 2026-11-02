// 当前平台，给按平台换说法的文案、Windows 上路径的分隔符(见 model.ts 的 nativePath)与顶栏画不画窗口按钮(见 titlebar.tsx)用。在调用时读:Wails 注入的环境信息到了以后才准;认不出的按 macOS。
import { System, Window } from "@wailsio/runtime";

export type Platform = "mac" | "windows" | "linux";

export const platform = (): Platform => (System.IsWindows() ? "windows" : System.IsLinux() ? "linux" : "mac");

// toggleMaximiseOnCaption 给 Windows 上没有系统标题栏的窗口用(顶栏与弹窗遮罩的顶边):双击落在拖动区上(不是按钮、输入框)时
// 最大化或还原，和系统标题栏一样。
export function toggleMaximiseOnCaption(e: React.MouseEvent) {
  if (getComputedStyle(e.target as Element).getPropertyValue("--wails-draggable").trim() === "drag") void Window.ToggleMaximise();
}
