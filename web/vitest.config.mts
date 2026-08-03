import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],

  // Reads the "@/..." aliases from tsconfig.json, so a test imports a module by
  // the same specifier the application does — the import in the test stays
  // identical to the import under test.
  //
  // Next's own guide still recommends the vite-tsconfig-paths plugin for this.
  // Vite resolves it natively now and says so on startup; the plugin is one
  // dependency for something already in the tool.
  resolve: { tsconfigPaths: true },

  test: {
    environment: "jsdom",
    setupFiles: ["./vitest.setup.ts"],

    // No globals. `describe` and `expect` are imported like anything else:
    // a reader can tell where they come from, and the editor can too.
    globals: false,

    // lib/api.ts reads NEXT_PUBLIC_API_URL when the module is first evaluated
    // and throws without it. Next inlines the value at build time; under Vitest
    // there is no Next build, so it is supplied here. The host is deliberately
    // not localhost — a request that escaped the fetch stub would fail loudly
    // rather than hit whatever happens to be listening on this machine.
    env: {
      NEXT_PUBLIC_API_URL: "http://api.test",
    },

    // The Next app is a separate build; its output must never be scanned for
    // tests.
    exclude: ["node_modules/**", ".next/**"],
  },
});
