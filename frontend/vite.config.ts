import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "");
  const apiProxyTarget = env.VITE_API_PROXY_TARGET || "http://localhost:8080";
  const apiProxy = () => ({
    target: apiProxyTarget,
    changeOrigin: false,
    xfwd: true
  });

  return {
    plugins: [react(), tailwindcss()],
    resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
    server: {
      port: 1787,
      strictPort: true,
      watch: {
        usePolling: env.VITE_USE_POLLING === "true"
      },
      proxy: {
        "/api": apiProxy(),
        "/healthz": apiProxy(),
        "/metrics": apiProxy()
      }
    }
  };
});
