// Unit tests only — Playwright specs in e2e/ run via playwright.config.ts.
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["src/**/*.test.ts"],
  },
});
