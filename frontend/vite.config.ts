import preact from "@preact/preset-vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [preact()],
  build: { outDir: "../web/dist", emptyOutDir: true, assetsInlineLimit: 0 },
  server: {
    // `npm run dev` proxies the API to a local `lookout` binary (or LOOKOUT_URL).
    // Keep the Host header: Lookout refuses writes whose Origin doesn't match it.
    proxy: {
      "/api": { target: process.env.LOOKOUT_URL ?? "http://localhost:8080", changeOrigin: false },
    },
  },
  test: { environment: "jsdom" },
});
