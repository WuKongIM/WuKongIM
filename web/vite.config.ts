import path from "node:path"

import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig, loadEnv, type ConfigEnv, type UserConfig } from "vite"

const defaultManagerProxyTarget = "http://127.0.0.1:5311"

function getManagerProxyTarget(env: Record<string, string | undefined>) {
  const raw = env.VITE_MANAGER_API_TARGET?.trim() ?? ""
  if (!raw) {
    return defaultManagerProxyTarget
  }

  return raw.replace(/\/+$/, "")
}

export function createViteConfig(
  configEnv: Pick<ConfigEnv, "mode">,
  env?: Record<string, string | undefined>,
): UserConfig {
  const resolvedEnv = env ?? { ...loadEnv(configEnv.mode, process.cwd(), ""), ...process.env }

  return {
    plugins: [react(), tailwindcss()],
    build: {
      outDir: path.resolve(__dirname, "../internal/access/manager/webui/dist"),
      emptyOutDir: true,
      rolldownOptions: {
        output: {
          codeSplitting: {
            groups: [
              {
                name: "react-core",
                test: /node_modules[\\/](?:react|react-dom|scheduler)[\\/]/,
                tags: ["$initial"],
              },
              {
                name: "router",
                test: /node_modules[\\/]react-router(?:-dom)?[\\/]/,
                tags: ["$initial"],
              },
              {
                name: "internationalization",
                test: /node_modules[\\/](?:(?:react-intl|intl-messageformat)[\\/]|@formatjs[\\/])/,
                tags: ["$initial"],
              },
              {
                name: "ui-primitives",
                test: /node_modules[\\/](?:radix-ui[\\/]|@radix-ui[\\/])/,
                tags: ["$initial"],
              },
              {
                name: "icons",
                test: /node_modules[\\/]lucide-react[\\/]/,
                tags: ["$initial"],
              },
              {
                name: "utilities",
                test: /node_modules[\\/](?:class-variance-authority|clsx|tailwind-merge|zustand)[\\/]/,
                tags: ["$initial"],
              },
            ],
          },
        },
      },
    },
    server: {
      proxy: {
        "/manager": {
          target: getManagerProxyTarget(resolvedEnv),
          changeOrigin: true,
        },
      },
    },
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
  }
}

export default defineConfig((configEnv) => createViteConfig(configEnv))
