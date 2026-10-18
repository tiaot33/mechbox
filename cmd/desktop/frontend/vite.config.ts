import path from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";

export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "src"),
      // Wails 绑定生成器按 Go 包路径输出，别名省掉冗长的前缀。
      "@bindings": path.resolve(import.meta.dirname, "bindings/github.com/tiaot33/mechbox"),
    },
  },
  plugins: [react(), tailwindcss(), wails("./bindings")],
});
