// 当前平台，给按平台换说法的文案、Windows 上路径的分隔符(见 model.ts 的 nativePath)与顶栏是否给红绿灯留位置(见 parts.tsx 的 TrafficLightSpace)用。在调用时读:Wails 注入的环境信息到了以后才准;认不出的按 macOS。
import { System } from "@wailsio/runtime";

export type Platform = "mac" | "windows" | "linux";

export const platform = (): Platform => (System.IsWindows() ? "windows" : System.IsLinux() ? "linux" : "mac");
