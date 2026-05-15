import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { ThemeProvider, useTheme } from "@/app/theme-provider"
import { resetThemePreference, THEME_STORAGE_KEY } from "@/app/theme-store"

type MatchMediaController = {
  setMatches: (matches: boolean) => void
}

function installMatchMedia(matches: boolean): MatchMediaController {
  let currentMatches = matches
  const listeners = new Set<(event: MediaQueryListEvent) => void>()

  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    writable: true,
    value: vi.fn((query: string) => ({
      matches: currentMatches,
      media: query,
      onchange: null,
      addEventListener: (type: string, listener: (event: MediaQueryListEvent) => void) => {
        if (type === "change") {
          listeners.add(listener)
        }
      },
      removeEventListener: (type: string, listener: (event: MediaQueryListEvent) => void) => {
        if (type === "change") {
          listeners.delete(listener)
        }
      },
      addListener: (listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
      removeListener: (listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
      dispatchEvent: () => true,
    })),
  })

  return {
    setMatches: (nextMatches: boolean) => {
      currentMatches = nextMatches
      listeners.forEach((listener) => {
        listener({ matches: nextMatches, media: "(prefers-color-scheme: dark)" } as MediaQueryListEvent)
      })
    },
  }
}

function ThemeProbe() {
  const { preference, resolvedTheme, setThemePreference } = useTheme()

  return (
    <div>
      <div data-testid="theme-preference">{preference}</div>
      <div data-testid="resolved-theme">{resolvedTheme}</div>
      <button onClick={() => setThemePreference("light")} type="button">
        Light
      </button>
      <button onClick={() => setThemePreference("system")} type="button">
        System
      </button>
    </div>
  )
}

beforeEach(() => {
  localStorage.clear()
  document.documentElement.classList.remove("dark", "light")
  document.documentElement.style.colorScheme = ""
  resetThemePreference()
})

test("defaults to the system theme and reacts to system changes", async () => {
  const media = installMatchMedia(true)

  render(
    <ThemeProvider>
      <ThemeProbe />
    </ThemeProvider>,
  )

  expect(screen.getByTestId("theme-preference")).toHaveTextContent("system")
  expect(screen.getByTestId("resolved-theme")).toHaveTextContent("dark")
  expect(document.documentElement).toHaveClass("dark")

  media.setMatches(false)

  await waitFor(() => {
    expect(screen.getByTestId("resolved-theme")).toHaveTextContent("light")
  })
  expect(document.documentElement).toHaveClass("light")
  expect(document.documentElement).not.toHaveClass("dark")
})

test("persists an explicit light override instead of following system changes", async () => {
  const media = installMatchMedia(true)
  const user = userEvent.setup()

  render(
    <ThemeProvider>
      <ThemeProbe />
    </ThemeProvider>,
  )

  await user.click(screen.getByRole("button", { name: "Light" }))

  expect(screen.getByTestId("theme-preference")).toHaveTextContent("light")
  expect(screen.getByTestId("resolved-theme")).toHaveTextContent("light")
  expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("light")
  expect(document.documentElement).toHaveClass("light")

  media.setMatches(false)

  expect(screen.getByTestId("resolved-theme")).toHaveTextContent("light")
})
