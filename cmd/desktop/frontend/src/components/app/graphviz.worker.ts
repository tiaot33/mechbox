// Graphviz 在 Worker 里把 dot 源码画成 SVG:renderString 是同步的，复杂的图能算很久，放在主线程会卡住整个界面。
// 收到 dot 源码，回 SVG;画不出来时回 null。每张图一个 Worker,超时与不再需要时由主线程结束(见 markdown.tsx 的 renderDot)。
import { instance } from "@viz-js/viz";

const viz = instance();
const font = { fontname: "Helvetica,Arial,sans-serif" };
const scope = self as unknown as {
  onmessage: ((e: MessageEvent<string>) => void) | null;
  postMessage(svg: string | null): void;
};

scope.onmessage = async ({ data: source }) => {
  try {
    scope.postMessage((await viz).renderString(source, { format: "svg", graphAttributes: font, nodeAttributes: font, edgeAttributes: font }));
  } catch {
    scope.postMessage(null);
  }
};
