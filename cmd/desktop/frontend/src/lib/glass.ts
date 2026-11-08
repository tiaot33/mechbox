// 毛玻璃表面：设置里的浓度，以及 Chromium 上不透明的背景。
// 材质本身(底色、滤镜、边缘、表面上的字色)在 index.css 的 glass 工具类里;这里只做 CSS 写不出来的几件事。
//
// 不透明的背景：窗口背后是透明的(透出 Mica 或 macOS 的毛玻璃),网页里的背景本身是半透明的;Chromium 把模糊后的背景叠在原内容上面，
// 结果半透明，没模糊的原内容就从玻璃底下透出来(右栏直接铺在窗口底色上，最明显)。所以在 backdrop-filter 最后再接一个
// 只把透明度设为 1 的滤镜(--glass-solid)。
//
// 不透明的底：系统要求减少透明度或开了强制颜色时，毛玻璃换成同样颜色的不透明底(index.css)。macOS 的 WKWebView 不认
// prefers-reduced-transparency,改问 Go 端(AppService.ReduceTransparency),在根元素上加 data-reduce-transparency。
import { useSyncExternalStore } from "react";
import { AppService } from "@bindings/cmd/desktop";

// applyGlassTint 把浓度(0–1.5,1 是设计的样子)写到根元素上，所有毛玻璃表面跟着平滑过渡;instant 时不过渡。
// 用哪个外观的浓度由 lib/theme.ts 定。
export function applyGlassTint(tint: number, instant = false) {
  const root = document.documentElement;
  if (instant) root.style.transition = "none";
  root.style.setProperty("--glass-tint", String(tint));
  if (instant) {
    void getComputedStyle(root).getPropertyValue("--glass-tint");
    root.style.removeProperty("transition");
  }
}

const opaqueQuery = "(prefers-reduced-transparency: reduce), (forced-colors: active)";
const opaqueListeners = new Set<() => void>();

function syncReduceTransparency() {
  AppService.ReduceTransparency().then(
    (on) => {
      const root = document.documentElement;
      if (root.hasAttribute("data-reduce-transparency") === on) return;
      root.toggleAttribute("data-reduce-transparency", on);
      opaqueListeners.forEach((f) => f());
    },
    () => {},
  );
}

function glassOpaque() {
  return matchMedia(opaqueQuery).matches || document.documentElement.hasAttribute("data-reduce-transparency");
}

// useGlassOpaque 是毛玻璃现在是否显示为不透明的底(系统要求减少透明度或开了强制颜色),这时浓度不起作用。
export function useGlassOpaque() {
  return useSyncExternalStore((changed) => {
    const query = matchMedia(opaqueQuery);
    query.addEventListener("change", changed);
    opaqueListeners.add(changed);
    return () => {
      query.removeEventListener("change", changed);
      opaqueListeners.delete(changed);
    };
  }, glassOpaque);
}

// 只有 Chromium 画 backdrop-filter 里的 url();Firefox、WebKit 能解析但什么也不画，所以另看内核。
function solidSupported() {
  return CSS.supports("backdrop-filter", "url(#a)") && /Chrome\//.test(navigator.userAgent);
}

const opaque = "1 0 0 0 0  0 1 0 0 0  0 0 1 0 0  0 0 0 0 1";

// installGlass 在启动时调用一次：问一次系统是否要求减少透明度，窗口重新获得焦点时再问(去系统设置里切了开关再回来);
// 在 Chromium 上建 --glass-solid 用的滤镜，只调透明度、不用 feImage,有动画的表面也能用。
export function installGlass() {
  syncReduceTransparency();
  window.addEventListener("focus", syncReduceTransparency);
  if (!solidSupported()) return;
  const svg = svgElement("svg", { width: 0, height: 0, "aria-hidden": "true" }) as SVGSVGElement;
  svg.style.position = "absolute";
  const filter = svgElement("filter", { id: "glass-solid", "color-interpolation-filters": "sRGB" }, svgElement("defs", {}, svg));
  svgElement("feColorMatrix", { type: "matrix", values: opaque }, filter);
  document.body.append(svg);
  document.documentElement.style.setProperty("--glass-solid", "url(#glass-solid)");
}

const svgNS = "http://www.w3.org/2000/svg";

function svgElement(name: string, attrs: Record<string, string | number>, parent?: Element) {
  const el = document.createElementNS(svgNS, name);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, String(v));
  parent?.append(el);
  return el;
}
