import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { CancelledRejectionError } from "@wailsio/runtime";
import { AppService } from "@bindings/cmd/desktop";
import { I18nProvider, type Language } from "@/lib/i18n";
import App from "./App";

// 前端取消一个绑定调用时，Go 端可能已经返回了错误、还在路上;它晚于取消到达，Wails 会把它报成未处理的 rejection。
// 取消了的调用不再关心结果，和 Go 端 settle 吞掉取消后的错误一样，这里不再上报。
window.addEventListener("unhandledrejection", (e) => {
  if (e.reason instanceof CancelledRejectionError) e.preventDefault();
});

// 等 Go 端定下界面语言再渲染，免得先闪一下另一种语言;读不到时用英文，和 Go 端认不出系统语言时一样。
void AppService.Language()
  .then((language): Language => (language === "zh" ? "zh" : "en"))
  .catch((): Language => "en")
  .then((language) =>
    createRoot(document.getElementById("root")!).render(
      <StrictMode>
        <I18nProvider initial={language}>
          <App />
        </I18nProvider>
      </StrictMode>,
    ),
  );
