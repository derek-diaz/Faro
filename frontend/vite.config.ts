import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "");
  const apiProxyTarget = env.VITE_API_PROXY_TARGET || "http://localhost:8080";

  return {
    plugins: [react()],
    server: {
      port: 5173,
      watch: {
        usePolling: env.VITE_USE_POLLING === "true"
      },
      proxy: {
        "/api": apiProxyTarget,
        "/healthz": apiProxyTarget,
        "/metrics": apiProxyTarget
      }
    }
  };
});
