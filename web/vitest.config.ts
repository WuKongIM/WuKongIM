import { mergeConfig } from "vite"
import { defineConfig } from "vitest/config"

import { createViteConfig } from "./vite.config"

export default defineConfig((configEnv) =>
  mergeConfig(
    createViteConfig(configEnv),
    defineConfig({
      test: {
        environment: "jsdom",
        globals: true,
        setupFiles: ["./src/test/setup.ts"],
      },
    }),
  ),
)
