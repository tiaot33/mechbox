// 外观：按设置(跟随系统、浅色、深色)定下实际用浅色还是深色，写在根元素的 data-theme 上(index.css 按它换颜色)。
// 跟随系统时看 prefers-color-scheme,系统切换深浅色时立即跟着变。
// 窗口背后的系统材质(macOS 的毛玻璃、Windows 的 Mica 与标题栏)由 Go 端在保存设置时按外观切换(AppService.applyAppearance)。
import { useSyncExternalStore } from "react";
import { Events } from "@wailsio/runtime";
import { Appearance, AppService } from "@bindings/cmd/desktop";

export type Theme = "light" | "dark";

// themeOf 是实际用的外观：选了浅色或深色的按设置，跟随系统的看系统是否在深色模式。
export function themeOf(appearance: Appearance | undefined, systemDark: boolean): Theme {
  if (appearance === Appearance.AppearanceLight) return "light";
  if (appearance === Appearance.AppearanceDark) return "dark";
  return systemDark ? "dark" : "light";
}

const darkQuery = "(prefers-color-scheme: dark)";
const listeners = new Set<() => void>();
let current: Appearance | undefined = Appearance.AppearanceSystem;

function systemDark() {
  return matchMedia(darkQuery).matches;
}

function currentTheme(): Theme {
  return themeOf(current, systemDark());
}

// apply 写上实际用的外观。
function apply() {
  const theme = currentTheme();
  const root = document.documentElement;
  if (root.dataset.theme !== theme) {
    root.dataset.theme = theme;
    listeners.forEach((f) => f());
  }
}

// setAppearance 换成这个外观设置并立即生效：设置页显示的设置一变就调用(包括保存失败退回原设置时)。
export function setAppearance(appearance: Appearance | undefined) {
  current = appearance;
  apply();
}

// installTheme 在渲染前调用一次：按保存的设置写上外观，之后跟着系统切换深浅色，
// 拉回(settings-reloaded)后按拉回来的设置重新写。读不到设置时按默认(跟随系统)。
export async function installTheme() {
  matchMedia(darkQuery).addEventListener("change", () => apply());
  Events.On("settings-reloaded", () => {
    AppService.Settings().then((s) => setAppearance(s.appearance), () => {});
  });
  const settings = await AppService.Settings().catch(() => null);
  setAppearance(settings ? settings.appearance : current);
}

// useTheme 是实际用的外观，系统或设置切换时跟着变。
export function useTheme(): Theme {
  return useSyncExternalStore((changed) => {
    listeners.add(changed);
    return () => listeners.delete(changed);
  }, currentTheme);
}
