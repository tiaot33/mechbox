// 毛玻璃表面：设置里的浓度、Chromium 上不透明的背景，以及静止的表面边缘的折射(镜边)。
// 材质本身(底色、滤镜、边缘的光、表面上的字色)在 index.css 的 glass 工具类里;这里只做 CSS 写不出来的几件事。
//
// 不透明的背景：窗口背后是透明的(透出 Mica 或 macOS 的毛玻璃),网页里的背景本身是半透明的;Chromium 把模糊后的背景叠在原内容上面，
// 结果半透明，没模糊的原内容就从玻璃底下透出来(右栏直接铺在窗口底色上，最明显)。所以在 backdrop-filter 最后再接一个
// 只把透明度设为 1 的滤镜(--glass-solid),镜边滤镜的输出同样是不透明的。
//
// 镜边：表面边缘一圈像厚玻璃一样把背后的内容往里折，中间不动。做法是一张位移图(红是横向、绿是纵向,128 不动)
// 交给 SVG 滤镜的 feDisplacementMap,接在 backdrop-filter 的最后。只有 Chromium(Windows 的 WebView2)画 backdrop-filter 里的 url(),
// 其他内核(macOS 的 WKWebView、Linux 的 WebKitGTK)保留只用 CSS 的毛玻璃。
// 位移图按圆角和镜边宽度切成九块：四个角、四条边(一个像素宽，拉伸铺满)和中间(不动),同一组圆角与镜边只生成一次;
// 表面大小变了只挪各块的位置，不重新生成。
//
// 不透明的底：系统要求减少透明度或开了强制颜色时，毛玻璃换成同样颜色的不透明底(index.css)。macOS 的 WKWebView 不认
// prefers-reduced-transparency,改问 Go 端(AppService.ReduceTransparency),在根元素上加 data-reduce-transparency。
import { useSyncExternalStore } from "react";
import { AppService } from "@bindings/cmd/desktop";

// applyGlassTint 把设置里的浓度(0–1.5,1 是设计的样子)写到根元素上，所有毛玻璃表面跟着平滑过渡;
// instant 时不过渡(启动时写入保存的浓度，免得先按 100% 画再变过去)。
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

// rimWidth 是镜边的宽度：卡片上 44px,窄的表面取短边的 1/3.5,不小于 4px。
export function rimWidth(width: number, height: number) {
  return Math.max(4, Math.min(44, Math.min(width, height) / 3.5));
}

// maxShift 是镜边最远往里看多远(贴着边缘时):1.1 倍镜边宽。位移曲线、位移图的编码和滤镜的 scale 都按它算。
function maxShift(rim: number) {
  return 1.1 * rim;
}

// lensOffset 是离边缘 d 的一点往里看多远：贴边时最远(maxShift),到镜边内沿减到 0,像厚玻璃的边。
export function lensOffset(d: number, rim: number) {
  if (d >= rim) return 0;
  return maxShift(rim) * Math.pow(1 - Math.max(0, d) / rim, 2.6);
}

// tileSize 是角上那块的边长：圆角与镜边取大的。
export function tileSize(radius: number, rim: number) {
  return Math.ceil(Math.max(radius, rim));
}

export type LensTile = "tl" | "tr" | "bl" | "br" | "l" | "r" | "t" | "b";

const lensTiles: LensTile[] = ["tl", "tr", "bl", "br", "l", "r", "t", "b"];

// inward 返回一点到表面边缘的距离与往里的方向;x、y 是到左边、上边的距离。圆角外面(被裁掉的像素)返回 undefined。
function inward(x: number, y: number, radius: number): [number, number, number] | undefined {
  if (x < radius && y < radius) {
    const cx = radius - x;
    const cy = radius - y;
    const len = Math.hypot(cx, cy);
    if (len > radius) return undefined;
    if (len === 0) return [radius, 0, 0];
    return [radius - len, cx / len, cy / len];
  }
  return x < y ? [x, 1, 0] : [y, 0, 1];
}

// lensTile 生成位移图的一块(RGBA):四个角是边长 tileSize 的方块，左右两条边是一行、上下两条边是一列，铺的时候拉伸。
export function lensTile(tile: LensTile, radius: number, rim: number) {
  const k = tileSize(radius, rim);
  const width = tile === "t" || tile === "b" ? 1 : k;
  const height = tile === "l" || tile === "r" ? 1 : k;
  const right = tile === "tr" || tile === "br" || tile === "r";
  const bottom = tile === "bl" || tile === "br" || tile === "b";
  const max = maxShift(rim);
  const data = new Uint8ClampedArray(width * height * 4);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const ex = tile === "t" || tile === "b" ? Infinity : (right ? width - 1 - x : x) + 0.5;
      const ey = tile === "l" || tile === "r" ? Infinity : (bottom ? height - 1 - y : y) + 0.5;
      const hit = inward(ex, ey, radius);
      const off = hit ? lensOffset(hit[0], rim) : 0;
      const dx = hit ? hit[1] * off * (right ? -1 : 1) : 0;
      const dy = hit ? hit[2] * off * (bottom ? -1 : 1) : 0;
      const i = (y * width + x) * 4;
      data[i] = Math.round(128 + (127 * dx) / max);
      data[i + 1] = Math.round(128 + (127 * dy) / max);
      data[i + 2] = 128;
      data[i + 3] = 255;
    }
  }
  return { width, height, data };
}

// 只有 Chromium 画 backdrop-filter 里的 url();Firefox、WebKit 能解析但什么也不画，所以另看内核。
function lensSupported() {
  return CSS.supports("backdrop-filter", "url(#a)") && /Chrome\//.test(navigator.userAgent);
}

const opaque = "1 0 0 0 0  0 1 0 0 0  0 0 1 0 0  0 0 0 0 1";

// installGlass 在启动时调用一次：问一次系统是否要求减少透明度，窗口重新获得焦点时再问(去系统设置里切了开关再回来);
// 在 Chromium 上建 --glass-solid 用的滤镜，只调透明度、不用 feImage,有动画的表面也能用。
export function installGlass() {
  syncReduceTransparency();
  window.addEventListener("focus", syncReduceTransparency);
  if (!lensSupported()) return;
  const svg = svgElement("svg", { width: 0, height: 0, "aria-hidden": "true" }) as SVGSVGElement;
  svg.style.position = "absolute";
  const filter = svgElement("filter", { id: "glass-solid", "color-interpolation-filters": "sRGB" }, svgElement("defs", {}, svg));
  svgElement("feColorMatrix", { type: "matrix", values: opaque }, filter);
  document.body.append(svg);
  document.documentElement.style.setProperty("--glass-solid", "url(#glass-solid)");
}

const maps = new Map<string, Promise<Record<LensTile, string>>>();

// lensMaps 生成一组圆角与镜边的九块位移图，等图片解码完再交出去(没解码完就指向滤镜，表面会画成没有模糊的样子)。
function lensMaps(radius: number, rim: number) {
  const key = `${radius}:${rim}`;
  let made = maps.get(key);
  if (!made) {
    made = Promise.all(
      lensTiles.map(async (name) => {
        const tile = lensTile(name, radius, rim);
        const canvas = document.createElement("canvas");
        canvas.width = tile.width;
        canvas.height = tile.height;
        canvas.getContext("2d")!.putImageData(new ImageData(tile.data, tile.width, tile.height), 0, 0);
        const url = canvas.toDataURL("image/png");
        const img = new Image();
        img.src = url;
        await img.decode();
        return [name, url] as const;
      }),
    ).then((urls) => Object.fromEntries(urls) as Record<LensTile, string>);
    maps.set(key, made);
  }
  return made;
}

const svgNS = "http://www.w3.org/2000/svg";
let lensCount = 0;

function svgElement(name: string, attrs: Record<string, string | number>, parent?: Element) {
  const el = document.createElementNS(svgNS, name);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, String(v));
  parent?.append(el);
  return el;
}

// 色散：红色多折 15%、蓝色少折 15%,各自只留自己的通道，再用 screen 叠回去。
// 三道都当不透明的叠：背景本身是半透明的，按透明度叠 screen 会把颜色冲淡成灰;输出不透明，也和 --glass-solid 一样盖住底下的原内容。
const channels = [
  { scale: 1.15, keep: "1 0 0 0 0  0 0 0 0 0  0 0 0 0 0  0 0 0 0 1" },
  { scale: 1, keep: "0 0 0 0 0  0 1 0 0 0  0 0 0 0 0  0 0 0 0 1" },
  { scale: 0.85, keep: "0 0 0 0 0  0 0 0 0 0  0 0 1 0 0  0 0 0 0 1" },
];

// buildFilter 在自己的 <svg> 里建一个滤镜：九块位移图拼成整张，再分三个颜色折射。
function buildFilter(svg: SVGSVGElement, id: string, urls: Record<LensTile, string>, rim: number) {
  svg.replaceChildren();
  const filter = svgElement(
    "filter",
    { id, filterUnits: "userSpaceOnUse", primitiveUnits: "userSpaceOnUse", "color-interpolation-filters": "sRGB" },
    svgElement("defs", {}, svg),
  );
  svgElement("feFlood", { "flood-color": "rgb(128,128,128)", result: "flat" }, filter);
  const images = {} as Record<LensTile, Element>;
  for (const name of lensTiles) {
    images[name] = svgElement("feImage", { href: urls[name], preserveAspectRatio: "none", result: name }, filter);
  }
  const merge = svgElement("feMerge", { result: "map" }, filter);
  for (const name of ["flat", ...lensTiles]) svgElement("feMergeNode", { in: name }, merge);
  const scale = 2 * maxShift(rim);
  channels.forEach((c, i) => {
    svgElement(
      "feDisplacementMap",
      { in: "SourceGraphic", in2: "map", scale: scale * c.scale, xChannelSelector: "R", yChannelSelector: "G", result: `d${i}` },
      filter,
    );
    svgElement("feColorMatrix", { in: `d${i}`, type: "matrix", values: c.keep, result: `c${i}` }, filter);
  });
  svgElement("feBlend", { in: "c0", in2: "c1", mode: "screen", result: "c01" }, filter);
  svgElement("feBlend", { in: "c01", in2: "c2", mode: "screen" }, filter);
  return { filter, images };
}

// placeFilter 按表面现在的大小挪九块位移图。
function placeFilter(filter: Element, images: Record<LensTile, Element>, width: number, height: number, k: number) {
  const set = (el: Element, x: number, y: number, w: number, h: number) => {
    el.setAttribute("x", String(x));
    el.setAttribute("y", String(y));
    el.setAttribute("width", String(Math.max(0, w)));
    el.setAttribute("height", String(Math.max(0, h)));
  };
  set(filter, 0, 0, width, height);
  set(images.tl, 0, 0, k, k);
  set(images.tr, width - k, 0, k, k);
  set(images.bl, 0, height - k, k, k);
  set(images.br, width - k, height - k, k, k);
  set(images.l, 0, k, k, height - 2 * k);
  set(images.r, width - k, k, k, height - 2 * k);
  set(images.t, k, 0, width - 2 * k, k);
  set(images.b, k, height - k, width - 2 * k, k);
}

// glassLens 给一个静止的毛玻璃表面加镜边，当作 ref 用(<div ref={glassLens}>,React 19 在卸载时调用返回的清理函数)。
// 只在 Chromium 上，表面短边不小于 32px,且没有 will-change 或动画(这时 Chrome 可能画出位移图本身而不是折过的背景)。
// 表面用 glass 工具类，滤镜经 --glass-lens 接在 backdrop-filter 最后。
export function glassLens(el: HTMLElement | null) {
  if (!el || !lensSupported()) return;
  const id = `glass-lens-${++lensCount}`;
  const svg = svgElement("svg", { width: 0, height: 0, "aria-hidden": "true" }) as SVGSVGElement;
  svg.style.position = "absolute";
  document.body.append(svg);
  let built: { key: string; filter: Element; images: Record<LensTile, Element> } | undefined;
  let pass = 0;
  const off = () => el.style.removeProperty("--glass-lens");
  const place = async () => {
    const mine = ++pass;
    const style = getComputedStyle(el);
    const width = el.offsetWidth;
    const height = el.offsetHeight;
    if (Math.min(width, height) < 32 || style.willChange !== "auto" || style.animationName !== "none") return off();
    const radius = Math.min(parseFloat(style.borderTopLeftRadius) || 0, Math.min(width, height) / 2);
    const rim = Math.round(rimWidth(width, height));
    const key = `${radius}:${rim}`;
    if (built?.key !== key) {
      const urls = await lensMaps(radius, rim);
      if (mine !== pass) return;
      built = { key, ...buildFilter(svg, id, urls, rim) };
    }
    placeFilter(built.filter, built.images, width, height, tileSize(radius, rim));
    el.style.setProperty("--glass-lens", `url(#${id})`);
  };
  const observer = new ResizeObserver(() => void place());
  observer.observe(el);
  return () => {
    pass++;
    observer.disconnect();
    svg.remove();
    off();
  };
}
