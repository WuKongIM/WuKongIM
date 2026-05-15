import {
  createContext,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useSyncExternalStore,
  type PropsWithChildren,
} from "react"

import {
  applyThemeToRoot,
  DARK_MODE_QUERY,
  getThemeSnapshot,
  setThemePreference,
  subscribeTheme,
  syncSystemTheme,
  type ThemePreference,
  type ThemeSnapshot,
} from "@/app/theme-store"

type ThemeContextValue = ThemeSnapshot & {
  setThemePreference: (preference: ThemePreference) => void
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

export function ThemeProvider({ children }: PropsWithChildren) {
  const snapshot = useSyncExternalStore(subscribeTheme, getThemeSnapshot, getThemeSnapshot)

  useLayoutEffect(() => {
    applyThemeToRoot(snapshot.resolvedTheme)
  }, [snapshot.resolvedTheme])

  useEffect(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
      return undefined
    }

    const mediaQueryList = window.matchMedia(DARK_MODE_QUERY)
    const handleSystemThemeChange = () => syncSystemTheme()

    handleSystemThemeChange()

    if (typeof mediaQueryList.addEventListener === "function") {
      mediaQueryList.addEventListener("change", handleSystemThemeChange)

      return () => {
        mediaQueryList.removeEventListener("change", handleSystemThemeChange)
      }
    }

    mediaQueryList.addListener(handleSystemThemeChange)

    return () => {
      mediaQueryList.removeListener(handleSystemThemeChange)
    }
  }, [])

  const value = useMemo<ThemeContextValue>(
    () => ({
      ...snapshot,
      setThemePreference,
    }),
    [snapshot],
  )

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme() {
  const context = useContext(ThemeContext)

  if (!context) {
    throw new Error("useTheme must be used within ThemeProvider")
  }

  return context
}
