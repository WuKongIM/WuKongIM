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
