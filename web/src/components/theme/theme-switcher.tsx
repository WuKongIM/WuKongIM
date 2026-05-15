import { Monitor, Moon, Sun } from "lucide-react"
import { useIntl } from "react-intl"

import { useTheme } from "@/app/theme-provider"
import type { ThemePreference } from "@/app/theme-store"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

const themeOptions: Array<{
  preference: ThemePreference
  labelMessageId: string
  ariaMessageId: string
  icon: typeof Monitor
}> = [
  {
    preference: "system",
    labelMessageId: "theme.system",
    ariaMessageId: "theme.systemAria",
    icon: Monitor,
  },
  {
    preference: "light",
    labelMessageId: "theme.light",
    ariaMessageId: "theme.lightAria",
    icon: Sun,
  },
  {
    preference: "dark",
    labelMessageId: "theme.dark",
    ariaMessageId: "theme.darkAria",
    icon: Moon,
  },
]

export function ThemeSwitcher() {
  const intl = useIntl()
  const { preference, setThemePreference } = useTheme()

  return (
    <div
      aria-label={intl.formatMessage({ id: "theme.switcher" })}
      className="inline-flex items-center gap-1 rounded-full border border-border/80 bg-card/70 p-1"
      role="group"
    >
      {themeOptions.map((option) => {
        const active = option.preference === preference
        const Icon = option.icon

        return (
          <Button
            aria-label={intl.formatMessage({ id: option.ariaMessageId })}
            aria-pressed={active}
            className={cn(
              "h-7 rounded-full px-2 text-xs",
              active ? "shadow-[0_0_14px_rgba(101,216,138,0.16)]" : "text-muted-foreground",
            )}
            key={option.preference}
            onClick={() => setThemePreference(option.preference)}
            size="sm"
            variant={active ? "default" : "ghost"}
          >
            <Icon aria-hidden className="size-3.5" />
            <span className="hidden 2xl:inline">{intl.formatMessage({ id: option.labelMessageId })}</span>
          </Button>
        )
      })}
    </div>
  )
}
