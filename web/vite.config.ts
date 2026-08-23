import { defineConfig } from "vite"
import react from "@vitejs/plugin-react-swc"
import tailwindcss from "@tailwindcss/vite"
import path from "node:path"

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  build: {
    // Built into the Go package that embeds it. go:embed cannot reference a
    // path outside its own package directory, so the bundle has to land there
    // rather than in web/dist.
    outDir: path.resolve(__dirname, "../internal/admin/dist"),
    emptyOutDir: true,
  },
  server: {
    host: "localhost",
    port: 5173,
  },
})
