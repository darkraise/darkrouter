import { defineConfig } from "vitest/config"
import react from "@vitejs/plugin-react-swc"
import path from "node:path"

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": path.resolve(__dirname, "./src") } },
  test: {
    environment: "jsdom",
    // vmThreads builds jsdom once per worker and gives each file a fresh VM
    // context on it. The default pool built a whole jsdom per file, which was
    // about 40% of the suite's runtime. src/test/setup.ts fills in the web
    // streams the VM context leaves out.
    pool: "vmThreads",
    setupFiles: ["./src/test/setup.ts"],
    globals: true,
  },
})
