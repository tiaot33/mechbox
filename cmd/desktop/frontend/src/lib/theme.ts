// 外观：按设置(跟随系统、浅色、深色)定下实际用浅色还是深色，写在根元素的 data-theme 上(index.css 按它换颜色),
// 并把那个外观的毛玻璃浓度写上去。跟随系统时看 prefers-color-scheme,系统切换深浅色时立即跟着变。
// 窗口背后的系统材质(macOS 的毛玻璃、Windows 的 Mica 与标题栏)由 Go 端在保存设置时按外观切换(AppService.applyAppearance)。
import { useSyncExternalStore } from "react";
import { Events } from "@wailsio/runtime";
import { Appearance, AppService, type Settings } from "@bindings/cmd/desktop";
import { applyGlassTint } from "@/lib/glass";

export type Theme = "light" | "dark";

export type ThemeSettings = Pick<Settings, "appearance" | "glass_tint" | "glass_tint_dark">;

// themeOf 是实际用的外观：选了浅色或深色的按设置，跟随系统的看系统是否在深色模式。
export function themeOf(appearance: Appearance | undefined, systemDark: boolean): Theme {
  if (appearance === Appearance.AppearanceLight) return "light";
  if (appearance === Appearance.AppearanceDark) return "dark";
  return systemDark ? "dark" : "light";
}

// glassTintOf 是某个外观下的毛玻璃浓度：浅色、深色各存一个值。
export function glassTintOf(settings: ThemeSettings, theme: Theme): number {
  return theme === "dark" ? settings.glass_tint_dark : settings.glass_tint;
}

const darkQuery = "(prefers-color-scheme: dark)";
const listeners = new Set<() => void>();
let current: ThemeSettings = { appearance: Appearance.AppearanceSystem, glass_tint: 1, glass_tint_dark: 1 };

function systemDark() {
  return matchMedia(darkQuery).matches;
}

function currentTheme(): Theme {
  return themeOf(current.appearance, systemDark());
}

// apply 写上实际用的外观与它的毛玻璃浓度。换外观时浓度从原来的值平滑过渡过去(根元素上 --glass-tint 的过渡);
// instant 时不过渡(启动时，免得先按默认值画再变过去)。
function apply(instant = false) {
  const theme = currentTheme();
  const root = document.documentElement;
  if (root.dataset.theme !== theme) {
    root.dataset.theme = theme;
    listeners.forEach((f) => f());
  }
  applyGlassTint(glassTintOf(current, theme), instant);
}

// setThemeSettings 换成这组设置并立即生效：设置页显示的设置一变就调用(包括保存失败退回原设置时)。
export function setThemeSettings(settings: ThemeSettings, instant = false) {
  current = { appearance: settings.appearance, glass_tint: settings.glass_tint, glass_tint_dark: settings.glass_tint_dark };
  apply(instant);
}

// previewGlassTint 是拖动浓度滑块时的预览：只改现在这个外观下界面上的浓度，不改设置;松手保存后经 setThemeSettings 定下。
export function previewGlassTint(tint: number) {
  applyGlassTint(tint);
}

// installTheme 在渲染前调用一次：按保存的设置写上外观与浓度(不过渡),之后跟着系统切换深浅色，
// 拉回(settings-reloaded)后按拉回来的设置重新写。读不到设置时按默认(跟随系统、浓度 100%)。
export async function installTheme() {
  matchMedia(darkQuery).addEventListener("change", () => apply());
  Events.On("settings-reloaded", () => {
    AppService.Settings().then((s) => setThemeSettings(s), () => {});
  });
  const settings = await AppService.Settings().catch(() => current);
  setThemeSettings(settings, true);
}

// useTheme 是实际用的外观，系统或设置切换时跟着变。
export function useTheme(): Theme {
  return useSyncExternalStore((changed) => {
    listeners.add(changed);
    return () => listeners.delete(changed);
  }, currentTheme);
}
