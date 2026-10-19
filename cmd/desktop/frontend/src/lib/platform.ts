// 当前平台，给按平台换说法的文案用。在调用时读:Wails 注入的环境信息到了以后才准;认不出的按 macOS。
import { System } from "@wailsio/runtime";

export type Platform = "mac" | "windows" | "linux";

export const platform = (): Platform => (System.IsWindows() ? "windows" : System.IsLinux() ? "linux" : "mac");
