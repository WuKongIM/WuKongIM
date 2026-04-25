import { useEffect } from "react"

import { useAuthStore } from "@/auth/auth-store"
import { TooltipProvider } from "@/components/ui/tooltip"
import { I18nProvider } from "@/i18n/provider"
import { configureManagerAuth } from "@/lib/manager-api"

type AppProvidersProps = {
  children: React.ReactNode
}

export function AppProviders({ children }: AppProvidersProps) {
  useEffect(() => {
    configureManagerAuth({
      getAccessToken: () => useAuthStore.getState().accessToken,
      onUnauthorized: () => useAuthStore.getState().handleUnauthorized(),
    })

    if (useAuthStore.getState().isHydrated) {
      return
    }

    const timer = window.setTimeout(() => {
      useAuthStore.getState().restoreSession()
    }, 0)

    return () => {
      window.clearTimeout(timer)
    }
  }, [])

  return (
    <I18nProvider>
      <TooltipProvider>{children}</TooltipProvider>
    </I18nProvider>
  )
}
