import { Button } from "@/components/ui/button"
import { LOCALE_LABELS } from "@/i18n/constants"
import { useLocale } from "@/i18n/use-locale"

export function LocaleSwitcher() {
  const { locale, setLocale } = useLocale()

  return (
    <div aria-label="Language switcher" className="inline-flex items-center gap-1">
      <Button
        aria-pressed={locale === "zh-CN"}
        onClick={() => setLocale("zh-CN")}
        size="sm"
        variant={locale === "zh-CN" ? "default" : "outline"}
      >
        {LOCALE_LABELS["zh-CN"]}
      </Button>
      <Button
        aria-pressed={locale === "en"}
        onClick={() => setLocale("en")}
        size="sm"
        variant={locale === "en" ? "default" : "outline"}
      >
        {LOCALE_LABELS.en}
      </Button>
    </div>
  )
}
