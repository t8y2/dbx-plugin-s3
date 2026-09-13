import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";
import { resolve } from "node:path";
import { defineConfig } from "vite";

export default defineConfig({
  resolve: { alias: { src: resolve(__dirname, "src") } },
  plugins: [
    svelte(),
    tailwindcss(),
    { name: "dbx-build-signal", writeBundle() { console.log("DBX_UI_BUILD_SUCCESS"); } },
  ],
  build: { outDir: "ui", emptyOutDir: true },
});
