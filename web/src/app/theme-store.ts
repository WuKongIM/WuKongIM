export type ThemePreference = "system" | "light" | "dark"

export type ResolvedTheme = "light" | "dark"

export type ThemeSnapshot = {
  preference: ThemePreference
  resolvedTheme: ResolvedTheme
}

type ThemeListener = () => void

export const THEME_STORAGE_KEY = "wukongim_manager_theme"

export const DARK_MODE_QUERY = "(prefers-color-scheme: dark)"

const themePreferences = new Set<ThemePreference>(["system", "light", "dark"])

const listeners = new Set<ThemeListener>()

let currentSnapshot: ThemeSnapshot | null = null

function isThemePreference(value: string | null): value is ThemePreference {
  return Boolean(value && themePreferences.has(value as ThemePreference))
}

function notifyListeners() {
  listeners.forEach((listener) => listener())
}

function readPersistedThemePreference(): ThemePreference {
  if (typeof window === "undefined") {
    return "system"
  }

  const persistedPreference = window.localStorage.getItem(THEME_STORAGE_KEY)
  if (!persistedPreference) {
    return "system"
  }

  if (isThemePreference(persistedPreference)) {
    return persistedPreference
  }

  window.localStorage.removeItem(THEME_STORAGE_KEY)
  return "system"
}

function persistThemePreference(preference: ThemePreference) {
  if (typeof window === "undefined") {
    return
  }

  if (preference === "system") {
    window.localStorage.removeItem(THEME_STORAGE_KEY)
    return
  }

  window.localStorage.setItem(THEME_STORAGE_KEY, preference)
}

export function getSystemTheme(): ResolvedTheme {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return "light"
  }

  return window.matchMedia(DARK_MODE_QUERY).matches ? "dark" : "light"
}

function resolveTheme(preference: ThemePreference): ResolvedTheme {
  return preference === "system" ? getSystemTheme() : preference
}

function createThemeSnapshot(preference: ThemePreference): ThemeSnapshot {
  return {
    preference,
    resolvedTheme: resolveTheme(preference),
  }
}

export function getThemeSnapshot() {
  if (!currentSnapshot) {
    currentSnapshot = createThemeSnapshot(readPersistedThemePreference())
  }

  return currentSnapshot
}

export function setThemePreference(preference: ThemePreference) {
  persistThemePreference(preference)
  currentSnapshot = createThemeSnapshot(preference)
  notifyListeners()
}

export function syncSystemTheme() {
  const snapshot = getThemeSnapshot()
  if (snapshot.preference !== "system") {
    return
  }

  const resolvedTheme = getSystemTheme()
  if (resolvedTheme === snapshot.resolvedTheme) {
    return
  }

  currentSnapshot = {
    ...snapshot,
    resolvedTheme,
  }
  notifyListeners()
}

export function resetThemePreference() {
  currentSnapshot = null

  if (typeof window !== "undefined") {
    window.localStorage.removeItem(THEME_STORAGE_KEY)
  }

  notifyListeners()
}

export function subscribeTheme(listener: ThemeListener) {
  listeners.add(listener)

  return () => {
    listeners.delete(listener)
  }
}

export function applyThemeToRoot(theme: ResolvedTheme, root?: HTMLElement) {
  const targetRoot = root ?? (typeof document === "undefined" ? null : document.documentElement)
  if (!targetRoot) {
    return
  }

  targetRoot.classList.toggle("dark", theme === "dark")
  targetRoot.classList.toggle("light", theme === "light")
  targetRoot.style.colorScheme = theme
}
