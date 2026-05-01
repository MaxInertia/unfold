import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// During `bun run dev`, Vite serves the frontend on its own port and proxies
// /api/* to the local Go server. The Go server's address is read from the
// UNFOLD_API env var (default: http://127.0.0.1:7777).
const apiTarget = process.env.UNFOLD_API ?? "http://127.0.0.1:7777";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: false,
    proxy: {
      "/api": { target: apiTarget, changeOrigin: false },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
