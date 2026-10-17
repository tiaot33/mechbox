import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { CancelledRejectionError } from "@wailsio/runtime";
import App from "./App";

// 前端取消一个绑定调用时，Go 端可能已经返回了错误、还在路上;它晚于取消到达，Wails 会把它报成未处理的 rejection。
// 取消了的调用不再关心结果，和 Go 端 settle 吞掉取消后的错误一样，这里不再上报。
window.addEventListener("unhandledrejection", (e) => {
  if (e.reason instanceof CancelledRejectionError) e.preventDefault();
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
